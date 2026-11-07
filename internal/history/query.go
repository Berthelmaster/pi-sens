package history

// Reading history back.
//
// A query reads one table, picked by the length of the range: raw samples up
// to RawUpTo, 1 minute rollups up to MinuteUpTo and 15 minute rollups beyond
// that. SQL then groups the rows into buckets of Step seconds, a multiple of
// the table's own resolution, so a series never has more than Points points.
// Buckets start at multiples of Step since the Unix epoch, so the same range
// queried again a little later gives the same buckets and a chart that
// refreshes does not jitter. The first bucket can therefore start before
// From, but it only holds rows from From on.

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	// RawUpTo is the longest range read from samples_raw.
	RawUpTo = 6 * time.Hour
	// MinuteUpTo is the longest range read from samples_1m. Longer ranges
	// read samples_15m.
	MinuteUpTo = 7 * 24 * time.Hour
)

// Resolution names the table a query read.
type Resolution string

const (
	ResolutionRaw     Resolution = "raw"
	ResolutionMinute  Resolution = "1m"
	ResolutionQuarter Resolution = "15m"
)

// Query asks for the values of some series in [From, To).
type Query struct {
	Keys     []string
	From, To time.Time
	// Points is the most points returned per series, at least 2.
	Points int
}

// Series is the points of one series, as parallel slices. T is the start of
// each bucket in unix seconds. For raw samples read without grouping, Avg,
// Min and Max are all the sample value.
type Series struct {
	T   []int64   `json:"t"`
	Avg []float64 `json:"avg"`
	Min []float64 `json:"min"`
	Max []float64 `json:"max"`
}

// Result is the answer to a Query. Every key asked for is in Series, with
// empty slices if it has no data in the range.
type Result struct {
	Resolution Resolution         `json:"resolution"`
	Step       int64              `json:"step"` // bucket size in seconds
	Series     map[string]*Series `json:"series"`
}

// source is a table a query can read and how to group its rows. ?1 is the
// series key, ?2 <= ts < ?3 the range and ?4 the bucket size. The CROSS JOIN
// makes SQLite look up the key first and then range-search the primary key.
type source struct {
	resolution Resolution
	bucket     int64 // the table's own resolution in seconds
	query      string
}

var (
	rawSource = source{ResolutionRaw, 1, `
		SELECT r.ts - r.ts % ?4 AS b, AVG(r.value), MIN(r.value), MAX(r.value)
		FROM series s CROSS JOIN samples_raw r
		WHERE s.key = ?1 AND r.series_id = s.id AND r.ts >= ?2 AND r.ts < ?3
		GROUP BY b ORDER BY b`}
	minuteSource  = source{ResolutionMinute, 60, rollupQuery("samples_1m")}
	quarterSource = source{ResolutionQuarter, 900, rollupQuery("samples_15m")}
)

// rollupQuery weights each row's average by its sample count, like the
// rollups themselves do.
func rollupQuery(table string) string {
	return fmt.Sprintf(`
		SELECT r.ts - r.ts %% ?4 AS b, SUM(r.avg * r.n) / SUM(r.n), MIN(r.min), MAX(r.max)
		FROM series s CROSS JOIN %s r
		WHERE s.key = ?1 AND r.series_id = s.id AND r.ts >= ?2 AND r.ts < ?3
		GROUP BY b ORDER BY b`, table)
}

// plan picks the table for [from, to) and the smallest bucket size that
// keeps the number of epoch-aligned buckets within points. It needs
// from < to and points >= 2.
func plan(from, to int64, points int) (source, int64) {
	span := to - from
	src := quarterSource
	switch {
	case span <= seconds(RawUpTo):
		src = rawSource
	case span <= seconds(MinuteUpTo):
		src = minuteSource
	}
	// ceil(span/step) buckets fit when aligned to from; aligned to the epoch
	// the range can straddle one more.
	n := int64(points)
	step := ceilTo(ceilDiv(span, n), src.bucket)
	if buckets(from, to, step) > n {
		step = ceilTo(ceilDiv(span, n-1), src.bucket)
	}
	return src, step
}

// buckets counts the epoch-aligned buckets of size step that [from, to)
// touches.
func buckets(from, to, step int64) int64 {
	return (floorTo(to-1, step)-floorTo(from, step))/step + 1
}

func ceilDiv(a, b int64) int64 { return (a + b - 1) / b }

// Query returns the series in q, grouped so each has at most q.Points
// points.
func (s *Store) Query(ctx context.Context, q Query) (Result, error) {
	from, to := q.From.Unix(), q.To.Unix()
	if from < 0 || from >= to {
		return Result{}, errors.New("history: query range is empty or before 1970")
	}
	if q.Points < 2 {
		return Result{}, errors.New("history: query needs at least 2 points")
	}
	src, step := plan(from, to, q.Points)
	res := Result{Resolution: src.resolution, Step: step, Series: make(map[string]*Series, len(q.Keys))}
	for _, key := range q.Keys {
		if _, ok := res.Series[key]; ok {
			continue
		}
		ser, err := s.querySeries(ctx, src, key, from, to, step)
		if err != nil {
			return Result{}, fmt.Errorf("history: query %q: %w", key, err)
		}
		res.Series[key] = ser
	}
	return res, nil
}

func (s *Store) querySeries(ctx context.Context, src source, key string, from, to, step int64) (*Series, error) {
	ser := &Series{T: []int64{}, Avg: []float64{}, Min: []float64{}, Max: []float64{}}
	rows, err := s.db.QueryContext(ctx, src.query, key, from, to, step)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var t int64
		var avg, lo, hi float64
		if err := rows.Scan(&t, &avg, &lo, &hi); err != nil {
			return nil, err
		}
		ser.T = append(ser.T, t)
		ser.Avg = append(ser.Avg, avg)
		ser.Min = append(ser.Min, lo)
		ser.Max = append(ser.Max, hi)
	}
	return ser, rows.Err()
}

// SeriesKeys returns the keys of every series ever stored that start with
// prefix, sorted. An empty prefix returns them all.
func (s *Store) SeriesKeys(ctx context.Context, prefix string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT key FROM series WHERE substr(key, 1, length(?1)) = ?1 ORDER BY key", prefix)
	if err != nil {
		return nil, fmt.Errorf("history: list series: %w", err)
	}
	defer rows.Close()
	keys := []string{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("history: list series: %w", err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("history: list series: %w", err)
	}
	return keys, nil
}
