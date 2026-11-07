package history

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"testing"
	"time"
)

// measureEnv turns on TestMeasureRawSize. It takes about a minute on a Pi 5,
// so it does not run with the other tests:
//
//	PISENS_MEASURE_DB=1 go test -run TestMeasureRawSize -v ./internal/history
const measureEnv = "PISENS_MEASURE_DB"

// TestMeasureRawSize measures how much space samples_raw takes once it holds
// its full retention, the figure behind the "Database size" table in the
// README. It simulates pisens with the defaults: 50 series sampled every 2
// seconds, written in batches of 5 like the Writer with a 10 second history
// interval, and the Downsampler run every minute on a fake clock. After 24
// hours the table is full, and a few more hours of deletes and inserts let
// SQLite reuse freed pages, which is the state a running Pi stays in. The
// sizes come from the dbstat virtual table.
func TestMeasureRawSize(t *testing.T) {
	if os.Getenv(measureEnv) == "" {
		t.Skipf("set %s=1 to measure the database size", measureEnv)
	}

	const (
		series   = 50
		interval = 2 * time.Second  // --interval
		flush    = 10 * time.Second // --history-interval, 5 samples per Insert
		fill     = 24 * time.Hour   // raw retention
		steady   = 3 * time.Hour    // deletes and page reuse after the table is full
	)
	ret := Retention{Raw: fill, Minute: 7 * 24 * time.Hour, Quarter: 90 * 24 * time.Hour}

	ctx := context.Background()
	s := openTest(t, t.TempDir())
	clock := &fakeClock{now: q0}
	d := newTestDownsampler(t, s, clock, ret, flush+2*interval)

	keys := make([]string, series)
	for i := range keys {
		keys[i] = fmt.Sprintf("test.series%02d_value", i)
	}
	rng := rand.New(rand.NewSource(1))

	var batch []Sample
	nextRun := q0.Add(RunEvery)
	simulate := func(until time.Time) {
		for clock.now.Before(until) {
			clock.now = clock.now.Add(interval)
			values := make(map[string]float64, series)
			for _, k := range keys {
				values[k] = rng.Float64() * 100
			}
			batch = append(batch, Sample{Time: clock.now, Values: values})
			if len(batch) == int(flush/interval) {
				if err := s.Insert(ctx, batch); err != nil {
					t.Fatal(err)
				}
				batch = batch[:0]
			}
			if !clock.now.Before(nextRun) {
				if err := d.RunOnce(ctx); err != nil {
					t.Fatal(err)
				}
				nextRun = nextRun.Add(RunEvery)
			}
		}
	}

	start := time.Now()
	simulate(q0.Add(fill))
	full := measureTables(t, s)
	simulate(clock.now.Add(steady))
	t.Logf("simulated %v in %v", clock.now.Sub(q0), time.Since(start).Round(time.Second))

	logSizes(t, fmt.Sprintf("after %v, before any raw row expired", fill), full, series)
	logSizes(t, fmt.Sprintf("after %v more of deletes and page reuse", steady), measureTables(t, s), series)

	var pages, free, pageSize int64
	for pragma, v := range map[string]*int64{"page_count": &pages, "freelist_count": &free, "page_size": &pageSize} {
		if err := s.db.QueryRow("PRAGMA " + pragma).Scan(v); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("database: %d pages of %d bytes, %.1f MB, %d free pages", pages, pageSize, mb(pages*pageSize), free)
}

// tableSize is what dbstat reports for one table.
type tableSize struct {
	name   string
	rows   int64
	pages  int64
	bytes  int64 // pages times page size
	unused int64 // bytes of those pages that hold no data
}

// measureTables returns the size of every sample table.
func measureTables(t *testing.T, s *Store) []tableSize {
	t.Helper()
	var sizes []tableSize
	for _, table := range []string{"samples_raw", "samples_1m", "samples_15m"} {
		ts := tableSize{name: table}
		err := s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(pgsize), 0), COALESCE(SUM(unused), 0)
			FROM dbstat WHERE name = ?`, table).Scan(&ts.pages, &ts.bytes, &ts.unused)
		if err != nil {
			t.Fatalf("dbstat %s: %v", table, err)
		}
		if err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&ts.rows); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		sizes = append(sizes, ts)
	}
	return sizes
}

func logSizes(t *testing.T, when string, sizes []tableSize, series int) {
	t.Helper()
	t.Logf("%s:", when)
	for _, ts := range sizes {
		perRow := 0.0
		if ts.rows > 0 {
			perRow = float64(ts.bytes) / float64(ts.rows)
		}
		t.Logf("  %-11s %9d rows (%d per series), %6d pages, %6.1f MB, %.2f MB per series, %.1f bytes per row, %.0f%% unused",
			ts.name, ts.rows, ts.rows/int64(series), ts.pages, mb(ts.bytes), mb(ts.bytes)/float64(series),
			perRow, 100*float64(ts.unused)/float64(max(ts.bytes, 1)))
	}
}

func mb(bytes int64) float64 { return float64(bytes) / 1e6 }
