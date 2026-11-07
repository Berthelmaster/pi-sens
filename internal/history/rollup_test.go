package history

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"
)

// q0 is a multiple of 15 minutes, so it starts a bucket at every level.
var q0 = time.Unix(1_700_000_100, 0)

// fakeClock is the time a Downsampler sees.
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

// bucket is one rollup row, with TS as an offset from q0.
type bucket struct {
	TS            time.Duration
	Avg, Min, Max float64
	N             int64
}

func (b bucket) String() string {
	return fmt.Sprintf("{+%v avg=%v min=%v max=%v n=%d}", b.TS, b.Avg, b.Min, b.Max, b.N)
}

func newTestDownsampler(t *testing.T, s *Store, clock *fakeClock, ret Retention, delay time.Duration) *Downsampler {
	t.Helper()
	return NewDownsampler(s, DownsamplerOptions{
		Retention: ret,
		Delay:     delay,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:       clock.Now,
	})
}

// longRetention keeps everything the tests write.
var longRetention = Retention{Raw: 24 * time.Hour, Minute: 7 * 24 * time.Hour, Quarter: 90 * 24 * time.Hour}

// insertAt stores one sample per offset from q0.
func insertAt(t *testing.T, s *Store, key string, values map[time.Duration]float64) {
	t.Helper()
	var samples []Sample
	for d, v := range values {
		samples = append(samples, Sample{Time: q0.Add(d), Values: map[string]float64{key: v}})
	}
	if err := s.Insert(context.Background(), samples); err != nil {
		t.Fatal(err)
	}
}

// readBuckets returns the rows of a rollup table grouped by series key.
func readBuckets(t *testing.T, s *Store, table string) map[string][]bucket {
	t.Helper()
	rows, err := s.db.Query(`SELECT series.key, r.ts, r.avg, r.min, r.max, r.n
		FROM ` + table + ` r JOIN series ON series.id = r.series_id
		ORDER BY series.key, r.ts`)
	if err != nil {
		t.Fatalf("query %s: %v", table, err)
	}
	defer rows.Close()
	got := map[string][]bucket{}
	for rows.Next() {
		var key string
		var ts int64
		var b bucket
		if err := rows.Scan(&key, &ts, &b.Avg, &b.Min, &b.Max, &b.N); err != nil {
			t.Fatalf("scan: %v", err)
		}
		b.TS = time.Duration(ts-q0.Unix()) * time.Second
		got[key] = append(got[key], b)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return got
}

func runOnce(t *testing.T, d *Downsampler) {
	t.Helper()
	if err := d.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
}

func checkBuckets(t *testing.T, s *Store, table string, want map[string][]bucket) {
	t.Helper()
	if got := readBuckets(t, s, table); !reflect.DeepEqual(got, want) {
		t.Errorf("%s =\n  %v\nwant\n  %v", table, got, want)
	}
}

func TestDownsampleAggregates(t *testing.T) {
	s := openTest(t, t.TempDir())
	clock := &fakeClock{}
	d := newTestDownsampler(t, s, clock, longRetention, 10*time.Second)
	sec, m := time.Second, time.Minute

	insertAt(t, s, "a", map[time.Duration]float64{
		0: 1, 20 * sec: 2, 40 * sec: 6, // first minute
		m: 10, m + 10*sec: 20, // second minute
		15*m + 10*sec: 100, // first minute of the second quarter
		16*m + 15*sec: 7,   // not complete at the first run
	})
	insertAt(t, s, "b", map[time.Duration]float64{30 * sec: 5})

	// 16:30 in, minus the 10s delay: the minute starting at 16:00 is still
	// open, and so is the quarter starting at 15:00.
	clock.now = q0.Add(16*m + 30*sec)
	runOnce(t, d)
	checkBuckets(t, s, "samples_1m", map[string][]bucket{
		"a": {{0, 3, 1, 6, 3}, {m, 15, 10, 20, 2}, {15 * m, 100, 100, 100, 1}},
		"b": {{0, 5, 5, 5, 1}},
	})
	// The quarter average weights each minute by its sample count:
	// (3*3 + 15*2) / 5.
	checkBuckets(t, s, "samples_15m", map[string][]bucket{
		"a": {{0, 7.8, 1, 20, 5}},
		"b": {{0, 5, 5, 5, 1}},
	})

	// Once the second quarter is over, its minutes and the quarter itself
	// are aggregated too.
	clock.now = q0.Add(30*m + 10*sec)
	runOnce(t, d)
	checkBuckets(t, s, "samples_1m", map[string][]bucket{
		"a": {{0, 3, 1, 6, 3}, {m, 15, 10, 20, 2}, {15 * m, 100, 100, 100, 1}, {16 * m, 7, 7, 7, 1}},
		"b": {{0, 5, 5, 5, 1}},
	})
	checkBuckets(t, s, "samples_15m", map[string][]bucket{
		"a": {{0, 7.8, 1, 20, 5}, {15 * m, 53.5, 7, 100, 2}},
		"b": {{0, 5, 5, 5, 1}},
	})
}

func TestDownsampleRerunDoesNotDuplicate(t *testing.T) {
	s := openTest(t, t.TempDir())
	clock := &fakeClock{now: q0.Add(2 * time.Hour)}
	d := newTestDownsampler(t, s, clock, longRetention, 0)

	values := map[time.Duration]float64{}
	for i := range 90 * 6 { // 90 minutes, every 10s
		values[time.Duration(i)*10*time.Second] = float64(i % 17)
	}
	insertAt(t, s, "a", values)
	insertAt(t, s, "b", values)

	runOnce(t, d)
	want1m, want15m := readBuckets(t, s, "samples_1m"), readBuckets(t, s, "samples_15m")
	if len(want1m["a"]) != 90 || len(want15m["a"]) != 6 {
		t.Fatalf("got %d minute and %d quarter rows for a, want 90 and 6", len(want1m["a"]), len(want15m["a"]))
	}

	// The same downsampler again, a fresh one as after a restart, and one
	// that has lost its progress and aggregates everything again.
	runOnce(t, d)
	checkBuckets(t, s, "samples_1m", want1m)
	checkBuckets(t, s, "samples_15m", want15m)

	runOnce(t, newTestDownsampler(t, s, clock, longRetention, 0))
	checkBuckets(t, s, "samples_1m", want1m)
	checkBuckets(t, s, "samples_15m", want15m)

	if _, err := s.db.Exec("DELETE FROM rollup_state"); err != nil {
		t.Fatal(err)
	}
	runOnce(t, newTestDownsampler(t, s, clock, longRetention, 0))
	checkBuckets(t, s, "samples_1m", want1m)
	checkBuckets(t, s, "samples_15m", want15m)
}

func TestDownsampleLateSample(t *testing.T) {
	s := openTest(t, t.TempDir())
	clock := &fakeClock{now: q0.Add(20 * time.Minute)}
	d := newTestDownsampler(t, s, clock, longRetention, 0)
	sec, m := time.Second, time.Minute

	insertAt(t, s, "a", map[time.Duration]float64{0: 1, 20 * sec: 2, 40 * sec: 6, m: 10, m + 10*sec: 20})
	runOnce(t, d)
	checkBuckets(t, s, "samples_15m", map[string][]bucket{"a": {{0, 7.8, 1, 20, 5}}})

	// A sample for the first minute arrives after both levels covered it,
	// say because the writer was retrying.
	insertAt(t, s, "a", map[time.Duration]float64{50 * sec: 0})
	clock.now = clock.now.Add(time.Minute)
	runOnce(t, d)
	checkBuckets(t, s, "samples_1m", map[string][]bucket{"a": {{0, 2.25, 0, 6, 4}, {m, 15, 10, 20, 2}}})
	checkBuckets(t, s, "samples_15m", map[string][]bucket{"a": {{0, 6.5, 0, 20, 6}}})
}

func TestDownsampleSkipsGaps(t *testing.T) {
	s := openTest(t, t.TempDir())
	clock := &fakeClock{now: q0.Add(time.Hour)}
	d := newTestDownsampler(t, s, clock, Retention{Raw: 100_000 * time.Hour, Minute: 100_000 * time.Hour, Quarter: 100_000 * time.Hour}, 0)

	// A sample from a clock that was years behind, then the real ones. The
	// run must jump over the gap instead of walking it chunk by chunk.
	years := -5 * 365 * 24 * time.Hour
	insertAt(t, s, "a", map[time.Duration]float64{years: 4, 0: 1})

	start := time.Now()
	runOnce(t, d)
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("RunOnce took %v", took)
	}
	checkBuckets(t, s, "samples_1m", map[string][]bucket{"a": {{years, 4, 4, 4, 1}, {0, 1, 1, 1, 1}}})
}

func TestDownsampleClockSetBack(t *testing.T) {
	s := openTest(t, t.TempDir())
	clock := &fakeClock{now: q0.Add(time.Hour)}
	d := newTestDownsampler(t, s, clock, longRetention, 10*time.Second)

	// A run while the clock is an hour ahead marks both levels done up to
	// then. NTP sets the clock back and samples arrive every 10s, with a
	// run every minute.
	runOnce(t, d)
	for i := range 20*6 + 1 {
		clock.now = q0.Add(time.Duration(i) * 10 * time.Second)
		insertAt(t, s, "a", map[time.Duration]float64{clock.now.Sub(q0): 1})
		if i%6 == 0 {
			runOnce(t, d)
		}
	}

	// At 20:00 with a 10s delay, the minutes up to 19:00 are complete, and
	// so is the first quarter.
	var want1m []bucket
	for m := range 19 {
		want1m = append(want1m, bucket{time.Duration(m) * time.Minute, 1, 1, 1, 6})
	}
	checkBuckets(t, s, "samples_1m", map[string][]bucket{"a": want1m})
	checkBuckets(t, s, "samples_15m", map[string][]bucket{"a": {{0, 1, 1, 1, 90}}})
}

// count returns the number of rows in table and the oldest ts as an offset
// from q0.
func count(t *testing.T, s *Store, table string) (int, time.Duration) {
	t.Helper()
	var n int
	var oldest int64
	if err := s.db.QueryRow("SELECT COUNT(*), COALESCE(MIN(ts), 0) FROM "+table).Scan(&n, &oldest); err != nil {
		t.Fatal(err)
	}
	return n, time.Duration(oldest-q0.Unix()) * time.Second
}

func TestRetention(t *testing.T) {
	s := openTest(t, t.TempDir())
	clock := &fakeClock{}
	ret := Retention{Raw: time.Hour, Minute: 2 * time.Hour, Quarter: 4 * time.Hour}
	d := newTestDownsampler(t, s, clock, ret, 0)

	// Six hours every 10s for two series: more old rows per series than one
	// delete batch.
	values := map[time.Duration]float64{}
	for i := range 6 * 360 {
		values[time.Duration(i)*10*time.Second] = 1
	}
	insertAt(t, s, "a", values)
	insertAt(t, s, "b", values)

	clock.now = q0.Add(6 * time.Hour)
	runOnce(t, d)

	tests := []struct {
		table  string
		rows   int // per series
		oldest time.Duration
	}{
		{"samples_raw", 360, 5 * time.Hour},
		{"samples_1m", 120, 4 * time.Hour},
		{"samples_15m", 16, 2 * time.Hour},
	}
	for _, tt := range tests {
		n, oldest := count(t, s, tt.table)
		if n != 2*tt.rows || oldest != tt.oldest {
			t.Errorf("%s has %d rows from +%v, want %d from +%v", tt.table, n, oldest, 2*tt.rows, tt.oldest)
		}
	}

	// Every bucket was aggregated before its source rows went.
	got := readBuckets(t, s, "samples_15m")["a"]
	if len(got) == 0 || got[0] != (bucket{2 * time.Hour, 1, 1, 1, 90}) {
		t.Errorf("oldest quarter of a = %v, want 90 samples at +2h", got)
	}

	// An hour later the windows have moved by an hour.
	clock.now = clock.now.Add(time.Hour)
	runOnce(t, d)
	for _, tt := range tests {
		if _, oldest := count(t, s, tt.table); tt.table != "samples_raw" && oldest != tt.oldest+time.Hour {
			t.Errorf("%s oldest row at +%v, want +%v", tt.table, oldest, tt.oldest+time.Hour)
		}
	}
	if n, _ := count(t, s, "samples_raw"); n != 0 {
		t.Errorf("samples_raw has %d rows, want 0: all of them are older than an hour", n)
	}
}

func TestRetentionKeepsUnaggregatedRows(t *testing.T) {
	s := openTest(t, t.TempDir())
	clock := &fakeClock{now: q0.Add(3 * time.Hour)}
	// Raw rows are only aggregated two hours after the fact, but kept for
	// one hour. Retention must wait for the aggregation.
	d := newTestDownsampler(t, s, clock, Retention{Raw: time.Hour, Minute: time.Hour, Quarter: 24 * time.Hour}, 2*time.Hour)

	values := map[time.Duration]float64{}
	for i := range 3 * 60 {
		values[time.Duration(i)*time.Minute] = 1
	}
	insertAt(t, s, "a", values)
	runOnce(t, d)

	if n, oldest := count(t, s, "samples_raw"); n != 120 || oldest != time.Hour {
		t.Errorf("samples_raw has %d rows from +%v, want 120 from +1h", n, oldest)
	}
	// The minutes of the first hour are aggregated into quarters, so they
	// may go, but the quarters keep them.
	if n, oldest := count(t, s, "samples_1m"); n != 0 {
		t.Errorf("samples_1m has %d rows from +%v, want 0", n, oldest)
	}
	if n, _ := count(t, s, "samples_15m"); n != 4 {
		t.Errorf("samples_15m has %d rows, want 4", n)
	}
}

func TestRetentionDropsExpiredSeries(t *testing.T) {
	s := openTest(t, t.TempDir())
	clock := &fakeClock{now: q0.Add(30 * time.Minute)}
	ret := Retention{Raw: time.Hour, Minute: 2 * time.Hour, Quarter: 4 * time.Hour}
	d := newTestDownsampler(t, s, clock, ret, 0)
	ctx := context.Background()
	h := time.Hour

	insertAt(t, s, "gone", map[time.Duration]float64{0: 1})
	insertAt(t, s, "kept", map[time.Duration]float64{0: 2, 5*h - time.Minute: 3})
	runOnce(t, d)
	if keys, err := s.SeriesKeys(ctx, ""); err != nil || !reflect.DeepEqual(keys, []string{"gone", "kept"}) {
		t.Fatalf("SeriesKeys() = %v, %v; want [gone kept] while gone still has rows", keys, err)
	}

	// Five hours on, every window has passed the only sample of gone.
	clock.now = q0.Add(5 * h)
	runOnce(t, d)
	if keys, err := s.SeriesKeys(ctx, ""); err != nil || !reflect.DeepEqual(keys, []string{"kept"}) {
		t.Errorf("SeriesKeys() = %v, %v; want [kept]", keys, err)
	}
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM series WHERE key = 'gone'").Scan(&n); err != nil || n != 0 {
		t.Errorf("series rows for gone = %d, %v; want 0", n, err)
	}
	s.mu.Lock()
	_, cached := s.ids["gone"]
	s.mu.Unlock()
	if cached {
		t.Error("gone is still in Store.ids")
	}

	// The key comes back: it gets a new row and is stored, downsampled and
	// queried like any other.
	insertAt(t, s, "gone", map[time.Duration]float64{5*h + 10*time.Second: 4})
	clock.now = q0.Add(5*h + 2*time.Minute)
	runOnce(t, d)
	if keys, err := s.SeriesKeys(ctx, ""); err != nil || !reflect.DeepEqual(keys, []string{"gone", "kept"}) {
		t.Errorf("SeriesKeys() = %v, %v; want [gone kept] after gone came back", keys, err)
	}
	res, err := s.Query(ctx, Query{Keys: []string{"gone"}, From: q0.Add(5 * h), To: q0.Add(5*h + time.Minute), Points: 60})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Series["gone"]; got == nil || !reflect.DeepEqual(got.Avg, []float64{4}) {
		t.Errorf("Query(gone) = %+v, want the single value 4", got)
	}
	if got := readBuckets(t, s, "samples_1m")["gone"]; !reflect.DeepEqual(got, []bucket{{5 * h, 4, 4, 4, 1}}) {
		t.Errorf("samples_1m of gone = %v, want one bucket at +5h", got)
	}
}

// TestQueriesUseIndex guards the queries the job runs every minute against
// scanning a whole sample table.
func TestQueriesUseIndex(t *testing.T) {
	s := openTest(t, t.TempDir())
	queries := []string{
		`DELETE FROM samples_raw WHERE series_id = ?1 AND ts IN (
			SELECT ts FROM samples_raw WHERE series_id = ?1 AND ts < ?2 ORDER BY ts LIMIT ?3)`,
		`SELECT MIN((SELECT MIN(ts) FROM samples_raw WHERE series_id = s.id AND ts >= ?1)) FROM series s`,
		dropEmptySeries,
	}
	for _, lv := range levels {
		queries = append(queries, lv.insert)
	}
	for _, q := range queries {
		rows, err := s.db.Query("EXPLAIN QUERY PLAN "+q, 0, 0, 0)
		if err != nil {
			t.Fatalf("explain %q: %v", q, err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		rows.Close()
		// The only table that may be scanned is series, aliased s.
		for _, step := range plan {
			if strings.HasPrefix(step, "SCAN") && step != "SCAN s" {
				t.Errorf("plan for\n%s\nscans a sample table:\n%s", q, strings.Join(plan, "\n"))
			}
		}
	}
}

func TestDownsamplerRunStopsOnCancel(t *testing.T) {
	s := openTest(t, t.TempDir())
	d := newTestDownsampler(t, s, &fakeClock{now: q0}, longRetention, 0)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.Run(ctx)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}
