package history

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestPlanPicksTableByRange(t *testing.T) {
	from := q0.Unix()
	tests := []struct {
		span     time.Duration
		points   int
		want     Resolution
		wantStep int64
	}{
		{time.Hour, 500, ResolutionRaw, 8},
		{time.Hour, 3600, ResolutionRaw, 1},
		{time.Hour, 100_000, ResolutionRaw, 1},
		{RawUpTo, 500, ResolutionRaw, 44},
		{RawUpTo + time.Second, 500, ResolutionMinute, 60},
		{2 * 24 * time.Hour, 500, ResolutionMinute, 360},
		{MinuteUpTo, 500, ResolutionMinute, 1260},
		{MinuteUpTo + time.Second, 500, ResolutionQuarter, 1800},
		{30 * 24 * time.Hour, 500, ResolutionQuarter, 5400},
		{30 * 24 * time.Hour, 100_000, ResolutionQuarter, 900},
	}
	for _, tt := range tests {
		src, step := plan(from, from+seconds(tt.span), tt.points)
		if src.resolution != tt.want || step != tt.wantStep {
			t.Errorf("plan(%v, %d points) = %s, step %d; want %s, step %d",
				tt.span, tt.points, src.resolution, step, tt.want, tt.wantStep)
		}
	}
}

func TestPlanNeverExceedsPoints(t *testing.T) {
	spans := []int64{1, 2, 59, 61, 3599, 3600, 21_599, 21_600, 21_601, 86_400, 604_800, 604_801, 2_592_000, 7_776_000}
	offsets := []int64{0, 1, 7, 59, 449, 899, 1799, 86_399}
	for _, points := range []int{2, 3, 7, 100, 500, 1000} {
		for _, span := range spans {
			for _, off := range offsets {
				from := q0.Unix() + off
				src, step := plan(from, from+span, points)
				if step%src.bucket != 0 {
					t.Errorf("plan(+%d, %ds, %d) step %d is not a multiple of %d", off, span, points, step, src.bucket)
				}
				if n := buckets(from, from+span, step); n > int64(points) {
					t.Errorf("plan(+%d, %ds, %d) step %d gives %d buckets", off, span, points, step, n)
				}
			}
		}
	}
}

// seedQuery stores one sample of "a" every 10s for the first 40 minutes
// after q0, with value i % 7 for the i-th sample, and downsamples them.
func seedQuery(t *testing.T) *Store {
	t.Helper()
	s := openTest(t, t.TempDir())
	values := map[time.Duration]float64{}
	for i := range 240 {
		values[time.Duration(i)*10*time.Second] = float64(i % 7)
	}
	insertAt(t, s, "a", values)
	insertAt(t, s, "cpu.b", map[time.Duration]float64{0: 1})
	runOnce(t, newTestDownsampler(t, s, &fakeClock{now: q0.Add(time.Hour)}, longRetention, 0))
	return s
}

func TestQueryGroupsRawSamples(t *testing.T) {
	s := seedQuery(t)
	res, err := s.Query(context.Background(), Query{
		Keys: []string{"a", "a", "missing"}, From: q0.Add(-5 * time.Second), To: q0.Add(55 * time.Second), Points: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 60s in at most 3 points. 20s buckets aligned to the epoch would make
	// 4, so they are 30s: one from q0-30 without samples, then the samples
	// at 0..20s and at 30..50s.
	q := q0.Unix()
	want := Result{Resolution: ResolutionRaw, Step: 30, Series: map[string]*Series{
		"a": {
			T:   []int64{q, q + 30},
			Avg: []float64{1, 4}, Min: []float64{0, 3}, Max: []float64{2, 5},
		},
		"missing": {T: []int64{}, Avg: []float64{}, Min: []float64{}, Max: []float64{}},
	}}
	if !reflect.DeepEqual(res, want) {
		t.Errorf("Query() = %+v %+v %+v, want %+v %+v %+v", res, res.Series["a"], res.Series["missing"],
			want, want.Series["a"], want.Series["missing"])
	}
}

func TestQueryReadsRollups(t *testing.T) {
	s := seedQuery(t)
	// More than 6h reads the minute rollups. Bucket 0 holds samples 0..5,
	// values 0..5, and bucket 1 holds 6..11, values 6, 0, 1, 2, 3, 4.
	res, err := s.Query(context.Background(), Query{
		Keys: []string{"a"}, From: q0, To: q0.Add(7 * time.Hour), Points: 10_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	a := res.Series["a"]
	if res.Resolution != ResolutionMinute || res.Step != 60 || len(a.T) != 40 {
		t.Fatalf("Query(7h) = %s, step %d, %d points; want 1m, step 60, 40 points", res.Resolution, res.Step, len(a.T))
	}
	if a.T[1] != q0.Unix()+60 || a.Avg[0] != 2.5 || a.Min[1] != 0 || a.Max[1] != 6 {
		t.Errorf("first points = t %v avg %v min %v max %v", a.T[:2], a.Avg[:2], a.Min[:2], a.Max[:2])
	}

	// More than 7 days reads the quarter rollups: two full quarters and the
	// 10 minutes of the third.
	res, err = s.Query(context.Background(), Query{
		Keys: []string{"a"}, From: q0, To: q0.Add(8 * 24 * time.Hour), Points: 10_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	a = res.Series["a"]
	if res.Resolution != ResolutionQuarter || res.Step != 900 || len(a.T) != 3 {
		t.Fatalf("Query(8d) = %s, step %d, %d points; want 15m, step 900, 3 points", res.Resolution, res.Step, len(a.T))
	}
	if a.T[2] != q0.Unix()+1800 || a.Min[0] != 0 || a.Max[0] != 6 {
		t.Errorf("points = %+v", a)
	}
}

func TestQueryRejectsBadInput(t *testing.T) {
	s := openTest(t, t.TempDir())
	for _, q := range []Query{
		{Keys: []string{"a"}, From: q0, To: q0, Points: 10},
		{Keys: []string{"a"}, From: q0, To: q0.Add(-time.Second), Points: 10},
		{Keys: []string{"a"}, From: time.Unix(-10, 0), To: q0, Points: 10},
		{Keys: []string{"a"}, From: q0, To: q0.Add(time.Hour), Points: 1},
	} {
		if _, err := s.Query(context.Background(), q); err == nil {
			t.Errorf("Query(%+v) error = nil", q)
		}
	}
}

func TestSeriesKeys(t *testing.T) {
	s := seedQuery(t)
	insertAt(t, s, "cpu.a", map[time.Duration]float64{0: 1})
	insertAt(t, s, "cpux", map[time.Duration]float64{0: 1})
	for prefix, want := range map[string][]string{
		"":     {"a", "cpu.a", "cpu.b", "cpux"},
		"cpu.": {"cpu.a", "cpu.b"},
		"cpu":  {"cpu.a", "cpu.b", "cpux"},
		"%":    {},
	} {
		got, err := s.SeriesKeys(context.Background(), prefix)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("SeriesKeys(%q) = %q, want %q", prefix, got, want)
		}
	}
}

// BenchmarkQuery30Days reads 30 days of 10 series from a database holding
// the default 7 days of minute and 90 days of quarter rollups for 50 series.
func BenchmarkQuery30Days(b *testing.B) {
	s, err := Open(context.Background(), b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	end := q0.Unix()
	seedRollups(b, s, 50, end)

	keys := make([]string, 10)
	for i := range keys {
		keys[i] = fmt.Sprintf("series.%02d", i*5)
	}
	q := Query{Keys: keys, From: time.Unix(end-30*86_400, 0), To: time.Unix(end, 0), Points: 500}
	b.ResetTimer()
	for range b.N {
		res, err := s.Query(context.Background(), q)
		if err != nil {
			b.Fatal(err)
		}
		if n := len(res.Series[keys[0]].T); n < 400 {
			b.Fatalf("got %d points", n)
		}
	}
}

// seedRollups fills samples_1m and samples_15m of n series up to end with
// the default retention of 7 and 90 days.
func seedRollups(tb testing.TB, s *Store, n int, end int64) {
	tb.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		tb.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for i := range n {
		if _, err := tx.Exec("INSERT INTO series (key) VALUES (?)", fmt.Sprintf("series.%02d", i)); err != nil {
			tb.Fatal(err)
		}
	}
	for _, t := range []struct {
		table      string
		step, rows int64
	}{{"samples_1m", 60, 7 * 1440}, {"samples_15m", 900, 90 * 96}} {
		_, err := tx.Exec(fmt.Sprintf(`WITH RECURSIVE k(i) AS (SELECT 0 UNION ALL SELECT i + 1 FROM k WHERE i + 1 < ?3)
			INSERT INTO %s (series_id, ts, avg, min, max, n)
			SELECT s.id, ?1 - (k.i + 1) * ?2, 50, abs(random() %% 50), 50 + abs(random() %% 50), ?2 / 2
			FROM series s CROSS JOIN k`, t.table), floorTo(end, t.step), t.step, t.rows)
		if err != nil {
			tb.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		tb.Fatal(err)
	}
}
