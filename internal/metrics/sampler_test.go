package metrics

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

// funcCollector adapts a function to the Collector interface.
type funcCollector struct {
	name string
	fn   func(ctx context.Context, env *Env) (Result, error)
}

func (f funcCollector) Name() string { return f.name }
func (f funcCollector) Collect(ctx context.Context, env *Env) (Result, error) {
	return f.fn(ctx, env)
}

func static(name string, data any, series map[string]float64) Collector {
	return funcCollector{name: name, fn: func(context.Context, *Env) (Result, error) {
		return Result{Data: data, Series: series}, nil
	}}
}

func failing(name string, err error) Collector {
	return funcCollector{name: name, fn: func(context.Context, *Env) (Result, error) {
		return Result{}, err
	}}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestSampler(timeout time.Duration, cs ...Collector) *Sampler {
	return NewSampler(Options{
		Interval:   time.Hour,
		Timeout:    timeout,
		Collectors: cs,
		FS:         fstest.MapFS{},
		Runner:     &FakeRunner{},
		Logger:     discardLogger(),
	})
}

// section decodes a collector's section of snap.
func section(t *testing.T, snap Snapshot, name string) map[string]any {
	t.Helper()
	v, ok := snap.Collectors[name]
	if !ok {
		t.Fatalf("snapshot has no %q section: %v", name, snap.Collectors)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode %q section: %v", name, err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %q section %s: %v", name, raw, err)
	}
	return out
}

func TestSampleMergesCollectors(t *testing.T) {
	s := newTestSampler(time.Second,
		static("a", map[string]int{"x": 1}, map[string]float64{"a.x_count": 1}),
		static("b", nil, map[string]float64{"b.y_pct": 2.5}),
	)

	if _, ok := s.Latest(); ok {
		t.Fatal("Latest() ok = true before the first sample")
	}
	snap := s.Sample(context.Background())

	if got := section(t, snap, "a"); got["x"] != 1.0 {
		t.Errorf("section a = %v, want x=1", got)
	}
	if got := section(t, snap, "b"); len(got) != 0 {
		t.Errorf("section b = %v, want {} for nil data", got)
	}
	want := map[string]float64{"a.x_count": 1, "b.y_pct": 2.5}
	if len(snap.Series) != len(want) || snap.Series["a.x_count"] != 1 || snap.Series["b.y_pct"] != 2.5 {
		t.Errorf("series = %v, want %v", snap.Series, want)
	}

	latest, ok := s.Latest()
	if !ok || !latest.Timestamp.Equal(snap.Timestamp) {
		t.Errorf("Latest() = %v, %v, want the sampled snapshot", latest.Timestamp, ok)
	}
	if _, err := json.Marshal(latest); err != nil {
		t.Errorf("snapshot does not encode: %v", err)
	}
}

func TestFailingCollectorDoesNotBreakOthers(t *testing.T) {
	s := newTestSampler(time.Second,
		failing("bad", errors.New("boom")),
		funcCollector{name: "panics", fn: func(context.Context, *Env) (Result, error) { panic("oops") }},
		static("unencodable", map[string]any{"f": func() {}}, nil),
		static("good", map[string]int{"v": 7}, map[string]float64{"good.v_count": 7, "good.nan_count": math.NaN()}),
	)

	snap := s.Sample(context.Background())

	if got := section(t, snap, "bad"); got["error"] != "boom" {
		t.Errorf("bad section = %v, want error boom", got)
	}
	if got := section(t, snap, "panics"); !strings.Contains(got["error"].(string), "oops") {
		t.Errorf("panics section = %v, want the panic message", got)
	}
	if got := section(t, snap, "unencodable"); got["error"] == nil {
		t.Errorf("unencodable section = %v, want an error", got)
	}
	if got := section(t, snap, "good"); got["v"] != 7.0 {
		t.Errorf("good section = %v, want v=7", got)
	}
	if len(snap.Series) != 1 || snap.Series["good.v_count"] != 7 {
		t.Errorf("series = %v, want only good.v_count (NaN dropped)", snap.Series)
	}
	if _, err := json.Marshal(snap); err != nil {
		t.Errorf("snapshot does not encode: %v", err)
	}
}

func TestHangingCollectorTimesOut(t *testing.T) {
	release := make(chan struct{})
	var calls sync.WaitGroup
	calls.Add(1)
	hang := funcCollector{name: "hang", fn: func(context.Context, *Env) (Result, error) {
		defer calls.Done()
		<-release // ignores ctx on purpose
		return Result{Data: map[string]bool{"late": true}}, nil
	}}
	t.Cleanup(func() {
		close(release)
		calls.Wait()
	})

	s := newTestSampler(50*time.Millisecond, hang, static("good", map[string]int{"v": 1}, map[string]float64{"good.v_count": 1}))

	start := time.Now()
	snap := s.Sample(context.Background())
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Sample() took %v, want about the 50ms timeout", elapsed)
	}
	if got := section(t, snap, "hang"); !strings.Contains(got["error"].(string), "timed out") {
		t.Errorf("hang section = %v, want a timeout error", got)
	}
	if got := section(t, snap, "good"); got["v"] != 1.0 {
		t.Errorf("good section = %v, want v=1", got)
	}

	// The hung call is still running, so the next round must not start a
	// second concurrent Collect on the same collector.
	snap = s.Sample(context.Background())
	if got := section(t, snap, "hang"); !strings.Contains(got["error"].(string), "still running") {
		t.Errorf("hang section on second round = %v, want still running", got)
	}
	if snap.Series["good.v_count"] != 1 {
		t.Errorf("series = %v, want good.v_count", snap.Series)
	}
}

func TestCollectorRespectingContextIsCancelled(t *testing.T) {
	slow := funcCollector{name: "slow", fn: func(ctx context.Context, _ *Env) (Result, error) {
		<-ctx.Done()
		return Result{}, ctx.Err()
	}}
	s := newTestSampler(20*time.Millisecond, slow)

	snap := s.Sample(context.Background())
	if got := section(t, snap, "slow"); got["error"] == nil {
		t.Errorf("slow section = %v, want an error", got)
	}
	// Once the call has returned the collector is free for the next round.
	deadline := time.Now().Add(2 * time.Second)
	for s.states[0].busy.Load() {
		if time.Now().After(deadline) {
			t.Fatal("collector still marked busy")
		}
		time.Sleep(time.Millisecond)
	}
	snap = s.Sample(context.Background())
	if got := section(t, snap, "slow"); !strings.Contains(got["error"].(string), "timed out") {
		t.Errorf("slow section on second round = %v, want a timeout", got)
	}
}

func TestCancelledRoundIsDiscarded(t *testing.T) {
	block := false
	started := make(chan struct{}, 1)
	c := funcCollector{name: "slow", fn: func(ctx context.Context, _ *Env) (Result, error) {
		if !block {
			return Result{Data: map[string]int{"v": 1}}, nil
		}
		started <- struct{}{}
		<-ctx.Done()
		return Result{}, ctx.Err()
	}}
	var buf bytes.Buffer
	s := NewSampler(Options{
		Interval: time.Hour, Timeout: time.Minute,
		Collectors: []Collector{c, static("good", nil, map[string]float64{"good.v_count": 1})},
		FS:         fstest.MapFS{}, Runner: &FakeRunner{},
		Logger: slog.New(slog.NewTextHandler(&buf, nil)),
	})

	first := s.Sample(context.Background())
	ch, unsubscribe := s.Subscribe()
	defer unsubscribe()

	block = true
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()
	s.Sample(ctx)

	latest, ok := s.Latest()
	if !ok || !latest.Timestamp.Equal(first.Timestamp) {
		t.Errorf("Latest() = %v, %v, want the first round's snapshot", latest.Timestamp, ok)
	}
	if got := section(t, latest, "slow"); got["v"] != 1.0 {
		t.Errorf("Latest slow section = %v, want v=1", got)
	}
	select {
	case snap := <-ch:
		t.Errorf("subscriber received the cancelled round %v", snap.Collectors)
	default:
	}
	if strings.Contains(buf.String(), "collector failed") {
		t.Errorf("cancelled round logged a failure:\n%s", buf.String())
	}
}

func TestEnvTimes(t *testing.T) {
	var envs []Env
	c := funcCollector{name: "c", fn: func(_ context.Context, env *Env) (Result, error) {
		envs = append(envs, *env)
		return Result{}, nil
	}}
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := t0
	s := NewSampler(Options{
		Interval: time.Second, Collectors: []Collector{c}, Logger: discardLogger(),
		Now: func() time.Time { return now },
	})

	s.Sample(context.Background())
	now = t0.Add(2 * time.Second)
	s.Sample(context.Background())

	if len(envs) != 2 {
		t.Fatalf("collected %d times, want 2", len(envs))
	}
	if !envs[0].Prev.IsZero() || !envs[0].Now.Equal(t0) {
		t.Errorf("first env Now=%v Prev=%v, want Now=%v Prev=zero", envs[0].Now, envs[0].Prev, t0)
	}
	if !envs[1].Prev.Equal(t0) || !envs[1].Now.Equal(now) {
		t.Errorf("second env Now=%v Prev=%v, want Now=%v Prev=%v", envs[1].Now, envs[1].Prev, now, t0)
	}
	if envs[0].FS == nil || envs[0].Runner == nil {
		t.Error("env FS or Runner is nil, want defaults")
	}
}

func TestErrorsAreLoggedOncePerChange(t *testing.T) {
	var errMsg string
	c := funcCollector{name: "flaky", fn: func(context.Context, *Env) (Result, error) {
		if errMsg == "" {
			return Result{}, nil
		}
		return Result{}, errors.New(errMsg)
	}}
	var buf bytes.Buffer
	s := NewSampler(Options{
		Interval: time.Second, Collectors: []Collector{c},
		Logger: slog.New(slog.NewTextHandler(&buf, nil)),
	})

	for _, msg := range []string{"", "e1", "e1", "e1", "e2", "e2", "", ""} {
		errMsg = msg
		s.Sample(context.Background())
	}

	out := buf.String()
	if n := strings.Count(out, "collector failed"); n != 2 {
		t.Errorf("logged %d failures, want 2:\n%s", n, out)
	}
	if n := strings.Count(out, "collector recovered"); n != 1 {
		t.Errorf("logged %d recoveries, want 1:\n%s", n, out)
	}
}

func TestSubscribersReceiveEverySnapshot(t *testing.T) {
	s := newTestSampler(time.Second, static("a", nil, nil))
	ch, unsubscribe := s.Subscribe()
	defer unsubscribe()

	for i := 0; i < 3; i++ {
		want := s.Sample(context.Background())
		select {
		case got := <-ch:
			if !got.Timestamp.Equal(want.Timestamp) {
				t.Errorf("round %d: got snapshot %v, want %v", i, got.Timestamp, want.Timestamp)
			}
		case <-time.After(time.Second):
			t.Fatalf("round %d: no snapshot received", i)
		}
	}
}

func TestSlowSubscriberDoesNotBlock(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tick := 0
	s := NewSampler(Options{
		Interval: time.Second, Collectors: []Collector{static("a", nil, nil)}, Logger: discardLogger(),
		Now: func() time.Time { tick++; return t0.Add(time.Duration(tick) * time.Second) },
	})
	slow, unsubSlow := s.Subscribe()
	defer unsubSlow()
	fast, unsubFast := s.Subscribe()
	defer unsubFast()

	const rounds = SubscriberBuffer * 3
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < rounds; i++ {
			s.Sample(context.Background())
			select {
			case <-fast:
			case <-time.After(time.Second):
				t.Errorf("fast subscriber missed round %d", i)
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("sampler blocked on a slow subscriber")
	}

	// The slow subscriber keeps the newest snapshots and lost the oldest.
	var got []time.Time
	for len(got) < SubscriberBuffer {
		got = append(got, (<-slow).Timestamp)
	}
	select {
	case snap := <-slow:
		t.Fatalf("slow subscriber has more than %d buffered snapshots, got extra %v", SubscriberBuffer, snap.Timestamp)
	default:
	}
	if last := t0.Add(rounds * time.Second); !got[len(got)-1].Equal(last) {
		t.Errorf("last buffered snapshot = %v, want the newest %v", got[len(got)-1], last)
	}
}

func TestUnsubscribeAndRunCloseChannels(t *testing.T) {
	s := NewSampler(Options{Interval: 10 * time.Millisecond, Collectors: []Collector{static("a", nil, nil)}, Logger: discardLogger()})

	ch1, unsub1 := s.Subscribe()
	unsub1()
	unsub1() // idempotent
	if _, ok := <-ch1; ok {
		t.Error("channel still open after unsubscribe")
	}

	ch2, unsub2 := s.Subscribe()
	defer unsub2()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	// Run samples straight away.
	select {
	case <-ch2:
	case <-time.After(2 * time.Second):
		t.Fatal("no snapshot from Run")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not return after cancel")
	}
	for range ch2 {
	}
	ch3, _ := s.Subscribe()
	if _, ok := <-ch3; ok {
		t.Error("Subscribe after Run returned an open channel")
	}
}

func TestRunRejectsBadInterval(t *testing.T) {
	s := NewSampler(Options{Collectors: []Collector{}, Logger: discardLogger()})
	if err := s.Run(context.Background()); err == nil {
		t.Fatal("Run() with zero interval error = nil, want an error")
	}
}

func TestRegister(t *testing.T) {
	registry.Lock()
	saved := registry.collectors
	registry.collectors = map[string]Collector{}
	registry.Unlock()
	t.Cleanup(func() {
		registry.Lock()
		registry.collectors = saved
		registry.Unlock()
	})

	Register(static("b", nil, nil))
	Register(static("a", nil, nil))
	got := Registered()
	if len(got) != 2 || got[0].Name() != "a" || got[1].Name() != "b" {
		t.Errorf("Registered() = %v, want [a b]", got)
	}

	for _, name := range []string{"a", ""} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("Register(%q) did not panic", name)
				}
			}()
			Register(static(name, nil, nil))
		}()
	}
}

func TestFakeRunner(t *testing.T) {
	f := &FakeRunner{Responses: map[string]FakeResponse{
		"vcgencmd measure_temp":  {Output: "temp=48.3'C\n"},
		"vcgencmd get_throttled": {Err: errors.New("denied")},
	}}
	ctx := context.Background()

	out, err := f.Run(ctx, "vcgencmd", "measure_temp")
	if err != nil || string(out) != "temp=48.3'C\n" {
		t.Errorf("Run(measure_temp) = %q, %v", out, err)
	}
	if _, err := f.Run(ctx, "vcgencmd", "get_throttled"); err == nil {
		t.Error("Run(get_throttled) error = nil, want denied")
	}
	if _, err := f.Run(ctx, "unknown"); err == nil {
		t.Error("Run(unknown) error = nil, want an error")
	}
	if calls := f.Calls(); len(calls) != 3 || calls[0] != "vcgencmd measure_temp" {
		t.Errorf("Calls() = %v", calls)
	}
}

func TestExecRunner(t *testing.T) {
	ctx := context.Background()
	out, err := ExecRunner{}.Run(ctx, "sh", "-c", "echo hi; echo oops >&2")
	if err != nil || string(out) != "hi\n" {
		t.Errorf("Run(echo) = %q, %v, want hi", out, err)
	}
	_, err = ExecRunner{}.Run(ctx, "sh", "-c", "echo bad >&2; exit 3")
	if err == nil || !strings.Contains(err.Error(), "bad") {
		t.Errorf("Run(exit 3) error = %v, want it to include stderr", err)
	}
}
