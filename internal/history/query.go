package history

// Reading history back.
//
// A query reads one table, picked by the length of the range: raw samples up
// to RawUpTo, 1 minute rollups up to MinuteUpTo and 15 minute rollups beyond
// that. If the range starts before the retention of that table (see
// Store.SetRetention), the rows it needs are gone, so the query steps up to
// the next coarser table that still has them. The rows are then grouped into
// buckets of Step seconds, a multiple of the table's own resolution, so a
// series never has more than Points points. Buckets start at multiples of
// Step since the Unix epoch, so the same range queried again a little later
// gives the same buckets and a chart that refreshes does not jitter. The
// first bucket can therefore start before From, but it only holds rows from
// From on.

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"math"
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

// source is a table a query can read. Its query returns the rows of series
// ?1 with ?2 <= ts < ?3 in ts order: ts and value for raw samples, ts, avg,
// min, max and n for rollups. The CROSS JOIN makes SQLite look up the key
// first and then range-search the primary key, which also gives the order,
// so SQLite streams the rows without sorting them. Go folds them into
// buckets (see fold).
type source struct {
	resolution Resolution
	bucket     int64 // the table's own resolution in seconds
	rollup     bool  // rows carry avg, min, max and n rather than one value
	query      string
}

var (
	rawSource = source{ResolutionRaw, 1, false, `
		SELECT r.ts, r.value
		FROM series s CROSS JOIN samples_raw r
		WHERE s.key = ?1 AND r.series_id = s.id AND r.ts >= ?2 AND r.ts < ?3
		ORDER BY r.ts`}
	minuteSource  = source{ResolutionMinute, 60, true, rollupQuery("samples_1m")}
	quarterSource = source{ResolutionQuarter, 900, true, rollupQuery("samples_15m")}
)

func rollupQuery(table string) string {
	return fmt.Sprintf(`
		SELECT r.ts, r.avg, r.min, r.max, r.n
		FROM series s CROSS JOIN %s r
		WHERE s.key = ?1 AND r.series_id = s.id AND r.ts >= ?2 AND r.ts < ?3
		ORDER BY r.ts`, table)
}

// plan picks the table for [from, to) and the smallest bucket size that
// keeps the number of epoch-aligned buckets within points. It needs
// from < to and points >= 2. A table whose retention ret has already
// expired from at time now is skipped for the next coarser one. The 15
// minute table is the last, so a range older than its retention still
// reads it.
func plan(from, to int64, points int, now int64, ret Retention) (source, int64) {
	span := to - from
	i := 2
	switch {
	case span <= seconds(RawUpTo):
		i = 0
	case span <= seconds(MinuteUpTo):
		i = 1
	}
	keep := []time.Duration{ret.Raw, ret.Minute}
	for i < len(keep) && keep[i] > 0 && from < now-seconds(keep[i]) {
		i++
	}
	src := []source{rawSource, minuteSource, quarterSource}[i]
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
	src, step := plan(from, to, q.Points, s.now().Unix(), s.retention)
	res := Result{Resolution: src.resolution, Step: step, Series: make(map[string]*Series, len(q.Keys))}
	// The rows are read through the driver: going through database/sql's
	// Scan costs more per row than SQLite takes to find it, and a query
	// reads up to ~10k rows per series.
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("history: query: %w", err)
	}
	defer conn.Close()
	err = conn.Raw(func(dc any) error {
		stmt, err := prepare(ctx, dc, src.query)
		if err != nil {
			return fmt.Errorf("history: query: %w", err)
		}
		defer stmt.Close()
		for _, key := range q.Keys {
			if _, ok := res.Series[key]; ok {
				continue
			}
			ser, err := querySeries(ctx, stmt, src, key, from, to, step)
			if err != nil {
				return fmt.Errorf("history: query %q: %w", key, err)
			}
			res.Series[key] = ser
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return res, nil
}

// queryStmt is a prepared driver statement that can run with a context.
type queryStmt interface {
	driver.Stmt
	driver.StmtQueryContext
}

// prepare prepares query on the driver connection dc.
func prepare(ctx context.Context, dc any, query string) (queryStmt, error) {
	c, ok := dc.(driver.ConnPrepareContext)
	if !ok {
		return nil, fmt.Errorf("driver connection %T cannot prepare with a context", dc)
	}
	st, err := c.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	q, ok := st.(queryStmt)
	if !ok {
		st.Close()
		return nil, fmt.Errorf("driver statement %T cannot query with a context", st)
	}
	return q, nil
}

func querySeries(ctx context.Context, stmt queryStmt, src source, key string, from, to, step int64) (*Series, error) {
	rows, err := stmt.QueryContext(ctx, []driver.NamedValue{
		{Ordinal: 1, Value: key}, {Ordinal: 2, Value: from}, {Ordinal: 3, Value: to},
	})
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	f := newFold(step)
	dest := make([]driver.Value, len(rows.Columns()))
	for {
		if err := rows.Next(dest); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		ts, ok := dest[0].(int64)
		avg, ok1 := toFloat(dest[1])
		if !ok || !ok1 {
			return nil, fmt.Errorf("unexpected row %v", dest)
		}
		lo, hi, n := avg, avg, int64(1)
		if src.rollup {
			var ok2, ok3, ok4 bool
			lo, ok2 = toFloat(dest[2])
			hi, ok3 = toFloat(dest[3])
			n, ok4 = dest[4].(int64)
			if !ok2 || !ok3 || !ok4 {
				return nil, fmt.Errorf("unexpected row %v", dest)
			}
		}
		f.add(ts, avg, lo, hi, n)
	}
	return f.series(), nil
}

// toFloat reads a REAL column. SQLite hands those out as floats even when
// it stored an integer, but an integer converts the same way it would in
// SQL.
func toFloat(v driver.Value) (float64, bool) {
	switch v := v.(type) {
	case float64:
		return v, true
	case int64:
		return float64(v), true
	}
	return 0, false
}

// fold groups rows that arrive in ts order into buckets of step seconds
// that start at multiples of step. Each bucket's average is the average of
// the rows' averages weighted by their sample counts, which for raw samples
// (n = 1) is the plain average. The result matches, down to the last bit,
//
//	SELECT ts - ts % step AS b, SUM(avg * n) / SUM(n), MIN(min), MAX(max)
//	... GROUP BY b ORDER BY b
//
// (with AVG(value) for raw samples), which is how queries used to group in
// SQL. SQLite cannot tell that b follows the primary key order, so it sorted
// every row in a temporary B-tree first.
type fold struct {
	step int64
	ser  *Series

	open   bool // a bucket has rows
	t      int64
	sum    kbnSum
	n      int64
	lo, hi float64
}

func newFold(step int64) *fold {
	return &fold{step: step, ser: &Series{T: []int64{}, Avg: []float64{}, Min: []float64{}, Max: []float64{}}}
}

func (f *fold) add(ts int64, avg, lo, hi float64, n int64) {
	if b := ts - ts%f.step; !f.open || b != f.t {
		f.flush()
		f.open, f.t, f.sum, f.n, f.lo, f.hi = true, b, kbnSum{}, 0, lo, hi
	} else {
		// Strict comparisons keep the first of equal values, as SQLite does.
		if lo < f.lo {
			f.lo = lo
		}
		if hi > f.hi {
			f.hi = hi
		}
	}
	// The conversion rounds the product before it is added, as SQLite does;
	// without it Go may fuse the multiply and add into one rounding.
	f.sum.add(float64(avg * float64(n)))
	f.n += n
}

func (f *fold) flush() {
	if !f.open {
		return
	}
	f.ser.T = append(f.ser.T, f.t)
	f.ser.Avg = append(f.ser.Avg, f.sum.value()/float64(f.n))
	f.ser.Min = append(f.ser.Min, f.lo)
	f.ser.Max = append(f.ser.Max, f.hi)
	f.open = false
}

// series returns the buckets folded so far.
func (f *fold) series() *Series {
	f.flush()
	return f.ser
}

// kbnSum adds floats the way SQLite's SUM and AVG do since 3.43, with
// Kahan-Babuska-Neumaier compensation, so a sum folded in Go matches one
// from SQL bit for bit.
type kbnSum struct{ sum, err float64 }

func (k *kbnSum) add(r float64) {
	s := k.sum
	t := s + r
	if math.Abs(s) > math.Abs(r) {
		k.err += s - t + r
	} else {
		k.err += r - t + s
	}
	k.sum = t
}

func (k kbnSum) value() float64 {
	if math.IsInf(k.err, 0) || math.IsNaN(k.err) {
		return k.sum
	}
	return k.sum + k.err
}

// SeriesKeys returns the keys of every series with stored rows that start
// with prefix, sorted. A series is listed until retention has expired all
// of its rows. An empty prefix returns them all.
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
