package history

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

func newTestWriter(t *testing.T, interval time.Duration) (*Writer, *Store) {
	t.Helper()
	s := openTest(t, t.TempDir())
	return NewWriter(s, interval, slog.New(slog.NewTextHandler(io.Discard, nil))), s
}

func snapAt(d time.Duration, series map[string]float64) metrics.Snapshot {
	return metrics.Snapshot{Timestamp: t0.Add(d), Series: series}
}

// timestamps returns the distinct stored timestamps as offsets from t0.
func timestamps(t *testing.T, s *Store) []time.Duration {
	t.Helper()
	rows, err := s.db.Query("SELECT DISTINCT ts FROM samples_raw ORDER BY ts")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := []time.Duration{}
	for rows.Next() {
		var ts int64
		if err := rows.Scan(&ts); err != nil {
			t.Fatal(err)
		}
		out = append(out, time.Duration(ts-t0.Unix())*time.Second)
	}
	return out
}

func TestWriterThrottle(t *testing.T) {
	w, s := newTestWriter(t, 10*time.Second)
	ctx := context.Background()
	sec := time.Second
	v := map[string]float64{"cpu.usage_pct": 1}

	steps := []struct {
		at   time.Duration
		want []time.Duration // stored timestamps after this snapshot
	}{
		// The first snapshot is written right away.
		{0, []time.Duration{0}},
		// Then nothing until an interval has passed since the last write.
		{2 * sec, []time.Duration{0}},
		{4 * sec, []time.Duration{0}},
		{8 * sec, []time.Duration{0}},
		// The snapshot that completes the interval writes the whole batch.
		{10 * sec, []time.Duration{0, 2 * sec, 4 * sec, 8 * sec, 10 * sec}},
		{12 * sec, []time.Duration{0, 2 * sec, 4 * sec, 8 * sec, 10 * sec}},
		// A gap longer than the interval writes on the next snapshot.
		{45 * sec, []time.Duration{0, 2 * sec, 4 * sec, 8 * sec, 10 * sec, 12 * sec, 45 * sec}},
	}
	for _, step := range steps {
		w.add(ctx, snapAt(step.at, v))
		if got := timestamps(t, s); !reflect.DeepEqual(got, step.want) {
			t.Fatalf("after snapshot at +%v stored %v, want %v", step.at, got, step.want)
		}
	}
	if len(w.pending) != 0 {
		t.Errorf("pending = %d samples, want 0", len(w.pending))
	}
}

func TestWriterClockBackwards(t *testing.T) {
	w, s := newTestWriter(t, 10*time.Second)
	ctx := context.Background()
	v := map[string]float64{"a": 1}

	w.add(ctx, snapAt(time.Hour, v))
	w.add(ctx, snapAt(0, v)) // the clock was set back an hour
	if got, want := timestamps(t, s), []time.Duration{0, time.Hour}; !reflect.DeepEqual(got, want) {
		t.Errorf("stored %v, want %v", got, want)
	}
}

func TestWriterNewKeys(t *testing.T) {
	w, s := newTestWriter(t, 10*time.Second)
	ctx := context.Background()

	w.add(ctx, snapAt(0, map[string]float64{"cpu.usage_pct": 5}))
	w.add(ctx, snapAt(5*time.Second, map[string]float64{"cpu.usage_pct": 6, "disk.sda.read_bps": 100}))
	w.add(ctx, snapAt(10*time.Second, map[string]float64{"disk.sda.read_bps": 200}))

	base := t0.Unix()
	want := map[string][]point{
		"cpu.usage_pct":     {{base, 5}, {base + 5, 6}},
		"disk.sda.read_bps": {{base + 5, 100}, {base + 10, 200}},
	}
	if got := readAll(t, s); !reflect.DeepEqual(got, want) {
		t.Errorf("samples = %v, want %v", got, want)
	}
}

func TestWriterRunFlushesWhenSourceCloses(t *testing.T) {
	w, s := newTestWriter(t, time.Hour)
	snaps := make(chan metrics.Snapshot, 3)
	snaps <- snapAt(0, map[string]float64{"a": 1})
	snaps <- snapAt(2*time.Second, map[string]float64{"a": 2})
	snaps <- snapAt(4*time.Second, map[string]float64{"a": 3})
	close(snaps)

	if err := w.Run(context.Background(), snaps); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	base := t0.Unix()
	want := map[string][]point{"a": {{base, 1}, {base + 2, 2}, {base + 4, 3}}}
	if got := readAll(t, s); !reflect.DeepEqual(got, want) {
		t.Errorf("samples = %v, want %v", got, want)
	}
}

func TestWriterRunFlushesOnCancel(t *testing.T) {
	w, s := newTestWriter(t, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	snaps := make(chan metrics.Snapshot) // never closed, like a live sampler
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx, snaps) }()

	snaps <- snapAt(0, map[string]float64{"a": 1})
	snaps <- snapAt(2*time.Second, map[string]float64{"a": 2}) // pending: the interval is an hour
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	base := t0.Unix()
	want := map[string][]point{"a": {{base, 1}, {base + 2, 2}}}
	if got := readAll(t, s); !reflect.DeepEqual(got, want) {
		t.Errorf("samples = %v, want %v", got, want)
	}
}

func TestWriterKeepsPendingWhenWriteFails(t *testing.T) {
	w, s := newTestWriter(t, 10*time.Second)
	ctx := context.Background()
	s.Close() // every write fails from now on

	for i := range maxPending + 5 {
		w.add(ctx, snapAt(time.Duration(i)*10*time.Second, map[string]float64{"a": float64(i)}))
	}
	if len(w.pending) != maxPending {
		t.Fatalf("pending = %d samples, want the cap %d", len(w.pending), maxPending)
	}
	if got, want := w.pending[0].Time, t0.Add(5*10*time.Second); !got.Equal(want) {
		t.Errorf("oldest pending sample at %v, want %v (the oldest are dropped)", got, want)
	}
	if !w.failing {
		t.Error("failing = false, want true")
	}
	if err := w.final(ctx); err == nil {
		t.Error("final() error = nil, want the write error")
	}
}

func TestWriterSkipsEmptySeries(t *testing.T) {
	w, _ := newTestWriter(t, 10*time.Second)
	w.queue(snapAt(0, nil))
	if len(w.pending) != 0 {
		t.Errorf("pending = %d samples, want 0", len(w.pending))
	}
}
