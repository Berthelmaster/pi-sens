package history

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"reflect"
	"slices"
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
		src, step := plan(from, from+seconds(tt.span), tt.points, from+seconds(tt.span), Retention{})
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
				src, step := plan(from, from+span, points, from+span, longRetention)
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

func TestPlanStepsUpPastRetention(t *testing.T) {
	now := q0.Unix()
	short := Retention{Raw: time.Hour, Minute: 2 * time.Hour, Quarter: 24 * time.Hour}
	tests := []struct {
		name     string
		ret      Retention
		from, to time.Duration // before now
		want     Resolution
	}{
		{"recent 6h reads raw", longRetention, 6 * time.Hour, 0, ResolutionRaw},
		{"raw kept exactly to from", longRetention, 24 * time.Hour, 23 * time.Hour, ResolutionRaw},
		{"6h three days ago", longRetention, 3 * 24 * time.Hour, 3*24*time.Hour - 6*time.Hour, ResolutionMinute},
		{"1h two days ago", longRetention, 2 * 24 * time.Hour, 47 * time.Hour, ResolutionMinute},
		{"3 days a month ago", longRetention, 30 * 24 * time.Hour, 27 * 24 * time.Hour, ResolutionQuarter},
		{"recent 3 days reads 1m", longRetention, 3 * 24 * time.Hour, 0, ResolutionMinute},
		{"older than every retention", longRetention, 200 * 24 * time.Hour, 199 * 24 * time.Hour, ResolutionQuarter},
		{"unknown retention", Retention{}, 30 * 24 * time.Hour, 30*24*time.Hour - time.Hour, ResolutionRaw},
		{"short raw, last 30m", short, 30 * time.Minute, 0, ResolutionRaw},
		{"short raw, last 3h", short, 3 * time.Hour, 0, ResolutionQuarter},
		{"short raw, 90m ago", short, 90 * time.Minute, time.Hour, ResolutionMinute},
		{"short 1m, last 2 days", short, 2 * 24 * time.Hour, 0, ResolutionQuarter},
	}
	for _, tt := range tests {
		src, step := plan(now-seconds(tt.from), now-seconds(tt.to), 500, now, tt.ret)
		if src.resolution != tt.want {
			t.Errorf("%s: plan() = %s, want %s", tt.name, src.resolution, tt.want)
		}
		if step%src.bucket != 0 {
			t.Errorf("%s: step %d is not a multiple of %d", tt.name, step, src.bucket)
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

// checkQuery runs a query of "a" over [from, to) after q0 and checks the
// table it read and the number of points.
func checkQuery(t *testing.T, s *Store, from, to time.Duration, want Resolution, wantPoints int) *Series {
	t.Helper()
	res, err := s.Query(context.Background(), Query{
		Keys: []string{"a"}, From: q0.Add(from), To: q0.Add(to), Points: 10_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	a := res.Series["a"]
	if res.Resolution != want || len(a.T) != wantPoints {
		t.Fatalf("Query(+%v, +%v) = %s, %d points; want %s, %d points", from, to, res.Resolution, len(a.T), want, wantPoints)
	}
	return a
}

func TestQueryReadsRollupsPastRetention(t *testing.T) {
	s := seedQuery(t)
	clock := &fakeClock{now: q0.Add(3 * 24 * time.Hour)}
	s.SetRetention(longRetention)
	s.now = clock.Now
	d := newTestDownsampler(t, s, clock, longRetention, 0)

	// Three days on, the raw samples have expired, so an hour of them is
	// read from the minute rollups.
	runOnce(t, d)
	if n := countRows(t, s, "samples_raw"); n != 0 {
		t.Fatalf("samples_raw has %d rows after retention, want 0", n)
	}
	a := checkQuery(t, s, 0, time.Hour, ResolutionMinute, 40)
	if a.Avg[0] != 2.5 || a.Min[1] != 0 || a.Max[1] != 6 {
		t.Errorf("first points = avg %v min %v max %v", a.Avg[:2], a.Min[:2], a.Max[:2])
	}

	// A month on, the minute rollups have expired too, so 3 days read the
	// quarter rollups.
	clock.now = q0.Add(30 * 24 * time.Hour)
	runOnce(t, d)
	a = checkQuery(t, s, 0, 3*24*time.Hour, ResolutionQuarter, 3)
	if a.Min[0] != 0 || a.Max[0] != 6 {
		t.Errorf("points = %+v", a)
	}
}

func TestQueryShortRetention(t *testing.T) {
	s := seedQuery(t)
	ret := Retention{Raw: time.Hour, Minute: 2 * time.Hour, Quarter: 24 * time.Hour}
	clock := &fakeClock{now: q0.Add(2 * time.Hour)}
	s.SetRetention(ret)
	s.now = clock.Now
	d := newTestDownsampler(t, s, clock, ret, 0)

	// The last 2h are under RawUpTo, but raw samples are only kept for 1h.
	runOnce(t, d)
	checkQuery(t, s, 0, 2*time.Hour, ResolutionMinute, 40)

	// The last 5h are past the 2h of minute rollups as well.
	clock.now = q0.Add(5 * time.Hour)
	runOnce(t, d)
	checkQuery(t, s, 0, 5*time.Hour, ResolutionQuarter, 3)
}

func countRows(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
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

// oldQueries are the queries that grouped rows in SQL before Query folded
// them in Go, by table. ?1 is the series key, ?2 <= ts < ?3 the range and ?4
// the bucket size.
var oldQueries = map[Resolution]string{
	ResolutionRaw: `
		SELECT r.ts - r.ts % ?4 AS b, AVG(r.value), MIN(r.value), MAX(r.value)
		FROM series s CROSS JOIN samples_raw r
		WHERE s.key = ?1 AND r.series_id = s.id AND r.ts >= ?2 AND r.ts < ?3
		GROUP BY b ORDER BY b`,
	ResolutionMinute:  oldRollupQuery("samples_1m"),
	ResolutionQuarter: oldRollupQuery("samples_15m"),
}

func oldRollupQuery(table string) string {
	return fmt.Sprintf(`
		SELECT r.ts - r.ts %% ?4 AS b, SUM(r.avg * r.n) / SUM(r.n), MIN(r.min), MAX(r.max)
		FROM series s CROSS JOIN %s r
		WHERE s.key = ?1 AND r.series_id = s.id AND r.ts >= ?2 AND r.ts < ?3
		GROUP BY b ORDER BY b`, table)
}

func TestQueryMatchesSQLGrouping(t *testing.T) {
	s := openTest(t, t.TempDir())
	rng := rand.New(rand.NewPCG(1, 2))
	// Values of very different sizes and signs, with repeats, so float
	// rounding, the order of the sum and ties in min and max all show.
	value := func() float64 {
		if rng.IntN(4) == 0 {
			return float64(rng.IntN(5) - 2)
		}
		return rng.NormFloat64() * math.Pow(10, float64(rng.IntN(16)-6))
	}
	end := q0.Unix()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for id, key := range []string{"a", "b"} {
		if _, err := tx.Exec("INSERT INTO series (id, key) VALUES (?, ?)", id+1, key); err != nil {
			t.Fatal(err)
		}
		// Rows with random gaps over a little more than each table's range.
		for _, tbl := range []struct {
			name       string
			step, span int64
		}{{"samples_raw", 1, 7 * 3600}, {"samples_1m", 60, 8 * 86_400}, {"samples_15m", 900, 40 * 86_400}} {
			for ts := floorTo(end-tbl.span, tbl.step); ts < end; ts += tbl.step * int64(1+rng.IntN(3)) {
				if tbl.name == "samples_raw" {
					_, err = tx.Exec("INSERT INTO samples_raw (series_id, ts, value) VALUES (?, ?, ?)", id+1, ts, value())
				} else {
					lo, hi := value(), value()
					_, err = tx.Exec(fmt.Sprintf("INSERT INTO %s (series_id, ts, avg, min, max, n) VALUES (?, ?, ?, ?, ?, ?)", tbl.name),
						id+1, ts, value(), min(lo, hi), max(lo, hi), 1+rng.IntN(1000))
				}
				if err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	spans := []int64{60, 3600, 6 * 3600, 6*3600 + 1, 86_400, 7 * 86_400, 7*86_400 + 1, 30 * 86_400}
	seen := map[Resolution]int{}
	for i := range 200 {
		span := spans[i%len(spans)]
		if i >= len(spans) {
			span = 1 + rng.Int64N(span)
		}
		to := end - rng.Int64N(3600)
		from := to - span
		points := 2 + rng.IntN(1000)
		q := Query{Keys: []string{"a", "b"}, From: time.Unix(from, 0), To: time.Unix(to, 0), Points: points}
		res, err := s.Query(context.Background(), q)
		if err != nil {
			t.Fatal(err)
		}
		seen[res.Resolution]++
		for _, key := range q.Keys {
			want := oldQuery(t, s, res.Resolution, key, from, to, res.Step)
			if got := res.Series[key]; !sameSeries(got, want) {
				t.Fatalf("Query(%s, [%d, %d), %d points) = %s step %d\n got %+v\nwant %+v",
					key, from, to, points, res.Resolution, res.Step, got, want)
			}
			if len(want.T) == 0 {
				t.Fatalf("Query(%s, [%d, %d)) found no rows, the test data misses the range", key, from, to)
			}
		}
	}
	for _, r := range []Resolution{ResolutionRaw, ResolutionMinute, ResolutionQuarter} {
		if seen[r] == 0 {
			t.Errorf("no query read %s", r)
		}
	}
}

// sameSeries reports whether a and b hold the same points, comparing floats
// bit for bit.
func sameSeries(a, b *Series) bool {
	same := func(x, y []float64) bool {
		return slices.EqualFunc(x, y, func(u, v float64) bool { return math.Float64bits(u) == math.Float64bits(v) })
	}
	return slices.Equal(a.T, b.T) && same(a.Avg, b.Avg) && same(a.Min, b.Min) && same(a.Max, b.Max)
}

// oldQuery groups the rows of key in [from, to) in SQL, the way Query did
// before.
func oldQuery(t *testing.T, s *Store, r Resolution, key string, from, to, step int64) *Series {
	t.Helper()
	rows, err := s.db.Query(oldQueries[r], key, from, to, step)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ser := &Series{T: []int64{}, Avg: []float64{}, Min: []float64{}, Max: []float64{}}
	for rows.Next() {
		var b int64
		var avg, lo, hi float64
		if err := rows.Scan(&b, &avg, &lo, &hi); err != nil {
			t.Fatal(err)
		}
		ser.T = append(ser.T, b)
		ser.Avg = append(ser.Avg, avg)
		ser.Min = append(ser.Min, lo)
		ser.Max = append(ser.Max, hi)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ser
}

// BenchmarkQuery reads 10 series over the longest range of each table from
// a database holding the default retention for 50 series: a day of raw
// samples every 2s, 7 days of minute and 90 days of quarter rollups.
func BenchmarkQuery(b *testing.B) {
	s, err := Open(context.Background(), b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	end := q0.Unix()
	seedRollups(b, s, 50, end)
	seedRaw(b, s, end, 2, 24*3600)

	keys := make([]string, 10)
	for i := range keys {
		keys[i] = fmt.Sprintf("series.%02d", i*5)
	}
	for _, bb := range []struct {
		name string
		span time.Duration
		want Resolution
	}{
		{"6h", RawUpTo, ResolutionRaw},
		{"7d", MinuteUpTo, ResolutionMinute},
		{"30d", 30 * 24 * time.Hour, ResolutionQuarter},
	} {
		b.Run(bb.name, func(b *testing.B) {
			q := Query{Keys: keys, From: time.Unix(end-seconds(bb.span), 0), To: time.Unix(end, 0), Points: 500}
			for range b.N {
				res, err := s.Query(context.Background(), q)
				if err != nil {
					b.Fatal(err)
				}
				if n := len(res.Series[keys[0]].T); res.Resolution != bb.want || n < 400 {
					b.Fatalf("got %s with %d points", res.Resolution, n)
				}
			}
		})
	}
}

// seedRaw fills samples_raw of every series with a sample every step
// seconds for span seconds up to end.
func seedRaw(tb testing.TB, s *Store, end, step, span int64) {
	tb.Helper()
	_, err := s.db.Exec(`WITH RECURSIVE k(i) AS (SELECT 0 UNION ALL SELECT i + 1 FROM k WHERE i + 1 < ?3)
		INSERT INTO samples_raw (series_id, ts, value)
		SELECT s.id, ?1 - (k.i + 1) * ?2, abs(random() % 10000) / 100.0
		FROM series s CROSS JOIN k`, end, step, span/step)
	if err != nil {
		tb.Fatal(err)
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
