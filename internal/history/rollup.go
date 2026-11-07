package history

// Downsampling and retention.
//
// Raw samples are rolled up into 1 minute buckets (samples_1m) and those into
// 15 minute buckets (samples_15m). A bucket row holds the average, minimum
// and maximum of the values in it, plus n, the number of raw samples behind
// it, so the 15 minute average weights each minute by how many samples it
// had. ts is the start of the bucket.
//
// rollup_state keeps, per rollup table, the end of the last aggregated
// bucket. A run aggregates from there up to the last completed bucket, one
// transaction per chunk of chunkBuckets buckets that also moves done_until
// forward, so an interrupted run resumes where it stopped and a repeated run
// finds nothing to do. Bucket rows are upserted, and only rewritten when a
// value changed, so aggregating a bucket twice leaves the same single row.
//
// A raw bucket counts as complete once Delay has passed after its end, which
// gives the Writer time to store its last samples. Samples that still arrive
// after their bucket was aggregated (the writer was failing and retried, or
// the clock was set back) are noticed by Store.Insert, and the next run
// aggregates those buckets, and the 15 minute buckets above them, again.
//
// Retention then deletes rows older than their table's retention, per series
// in batches of deleteBatch rows, so no single statement holds the write lock
// for long. Rows the next level has not aggregated yet are kept.
//
// There is no PRAGMA incremental_vacuum. In steady state retention deletes
// about as many rows a minute as the writer adds, and SQLite reuses the freed
// pages, so the file stops growing once every table holds its full retention.
// Handing pages back to the filesystem would only make SQLite grow the file
// again minutes later, which is extra writes on an SD card for nothing.
// incremental_vacuum also needs auto_vacuum=INCREMENTAL, which a database
// created without it only gets from a full VACUUM. After lowering a retention
// a lot, stop pisens and run `sqlite3 pisens.db VACUUM` to shrink the file.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

const (
	// RunEvery is how often Downsampler.Run aggregates and expires.
	RunEvery = time.Minute
	// chunkBuckets is how many buckets one transaction aggregates.
	chunkBuckets = 60
	// deleteBatch is how many rows one retention statement deletes.
	deleteBatch = 500
)

// Retention is how long each table keeps its rows.
type Retention struct {
	Raw     time.Duration // samples_raw
	Minute  time.Duration // samples_1m
	Quarter time.Duration // samples_15m
}

// level is one rollup step.
type level struct {
	table  string // rollup table
	source string // table it is aggregated from
	bucket int64  // bucket size in seconds
	insert string // aggregates source rows with ?1 <= ts < ?2 into table
}

// upsert ends both insert statements. The WHERE skips rows whose values did
// not change, so aggregating a bucket again writes nothing.
const upsert = ` ON CONFLICT (series_id, ts) DO UPDATE SET
	avg = excluded.avg, min = excluded.min, max = excluded.max, n = excluded.n
	WHERE avg IS NOT excluded.avg OR min IS NOT excluded.min
		OR max IS NOT excluded.max OR n IS NOT excluded.n`

// The CROSS JOIN makes SQLite loop over the few series and range-search the
// primary key of the sample table for each, instead of scanning all of it.
var levels = []level{
	{
		table: "samples_1m", source: "samples_raw", bucket: 60,
		insert: `INSERT INTO samples_1m (series_id, ts, avg, min, max, n)
			SELECT r.series_id, r.ts - r.ts % 60, AVG(r.value), MIN(r.value), MAX(r.value), COUNT(*)
			FROM series s CROSS JOIN samples_raw r
			WHERE r.series_id = s.id AND r.ts >= ?1 AND r.ts < ?2
			GROUP BY r.series_id, r.ts - r.ts % 60` + upsert,
	},
	{
		table: "samples_15m", source: "samples_1m", bucket: 900,
		insert: `INSERT INTO samples_15m (series_id, ts, avg, min, max, n)
			SELECT r.series_id, r.ts - r.ts % 900, SUM(r.avg * r.n) / SUM(r.n), MIN(r.min), MAX(r.max), SUM(r.n)
			FROM series s CROSS JOIN samples_1m r
			WHERE r.series_id = s.id AND r.ts >= ?1 AND r.ts < ?2
			GROUP BY r.series_id, r.ts - r.ts % 900` + upsert,
	},
}

// DownsamplerOptions configures a Downsampler.
type DownsamplerOptions struct {
	Retention Retention
	// Delay is how long after its timestamp a sample may still be on its way
	// to the database. A raw bucket is aggregated once Delay has passed after
	// its end.
	Delay time.Duration
	// Logger defaults to slog.Default().
	Logger *slog.Logger
	// Now defaults to time.Now. Tests replace it with a fake clock.
	Now func() time.Time
}

// Downsampler aggregates raw samples into the rollup tables and deletes rows
// that are past their retention.
type Downsampler struct {
	store *Store
	opts  DownsamplerOptions

	failing bool
}

// NewDownsampler creates a downsampler for store.
func NewDownsampler(store *Store, opts DownsamplerOptions) *Downsampler {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Downsampler{store: store, opts: opts}
}

// Run calls RunOnce right away and then every RunEvery until ctx is done.
// Errors are logged, and the next run tries again.
func (d *Downsampler) Run(ctx context.Context) {
	tick := time.NewTicker(RunEvery)
	defer tick.Stop()
	for {
		err := d.RunOnce(ctx)
		if ctx.Err() != nil {
			return // shutting down, the error is just the cancellation
		}
		d.report(err)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// report logs a failed run once, and once more when runs work again.
func (d *Downsampler) report(err error) {
	switch {
	case err != nil && !d.failing:
		d.opts.Logger.Error("history downsampling failed", "err", err)
	case err == nil && d.failing:
		d.opts.Logger.Info("history downsampling recovered")
	}
	d.failing = err != nil
}

// RunOnce aggregates every completed bucket that is not aggregated yet, then
// deletes rows past their retention. Running it again without new samples
// changes nothing.
func (d *Downsampler) RunOnce(ctx context.Context) error {
	written, ok := d.store.takeWritten()
	if err := d.runOnce(ctx, written, ok); err != nil {
		if ok {
			d.store.restoreWritten(written) // aggregate them on the next run
		}
		return err
	}
	return nil
}

func (d *Downsampler) runOnce(ctx context.Context, written int64, hasWritten bool) error {
	now := d.opts.Now().Unix()
	ret := d.opts.Retention
	keep := []int64{ // per table, the oldest timestamp to keep
		now - seconds(ret.Raw),
		now - seconds(ret.Minute),
		now - seconds(ret.Quarter),
	}

	// Raw buckets ending before end are complete, and each level's buckets
	// are complete once the level below covers them.
	end := now - seconds(d.opts.Delay)
	done := make([]int64, len(levels))
	changed, hasChanged := written, hasWritten
	for i, lv := range levels {
		end = floorTo(end, lv.bucket)
		from, ok, err := doneUntil(ctx, d.store.db, lv.table)
		if err != nil {
			return err
		}
		if !ok {
			from = 0 // never ran: start at the oldest source row
		}
		// Aggregate again the buckets that got new source rows, but not those
		// whose source rows retention may already have deleted.
		if hasChanged {
			if redo := max(floorTo(changed, lv.bucket), ceilTo(keep[i], lv.bucket)); redo < from {
				from = redo
			}
		}
		if done[i], err = d.aggregate(ctx, lv, from, end); err != nil {
			return fmt.Errorf("aggregate %s: %w", lv.table, err)
		}
		// The next level has to look at everything this one may have changed.
		changed, hasChanged = from, true
		end = done[i]
	}

	// Never delete source rows the next level has not aggregated yet.
	cutoffs := []struct {
		table  string
		before int64
	}{
		{"samples_raw", min(keep[0], done[0])},
		{"samples_1m", min(keep[1], done[1])},
		{"samples_15m", keep[2]},
	}
	for _, c := range cutoffs {
		n, err := d.expire(ctx, c.table, c.before)
		if err != nil {
			return fmt.Errorf("expire %s: %w", c.table, err)
		}
		if n > 0 {
			d.opts.Logger.Debug("history rows expired", "table", c.table, "rows", n)
		}
	}
	return nil
}

// aggregate rolls the source rows of lv with from <= ts < end up into lv's
// table and returns how far the table is now complete. from and end are
// multiples of the bucket size.
func (d *Downsampler) aggregate(ctx context.Context, lv level, from, end int64) (int64, error) {
	done, ok, err := doneUntil(ctx, d.store.db, lv.table)
	if err != nil {
		return 0, err
	}
	if !ok {
		done = 0
	}
	for from < end {
		// Skip straight to the next source row, so a gap in the data (or a
		// sample from a clock that was years off) costs one query.
		next, ok, err := nextTS(ctx, d.store.db, lv.source, from)
		if err != nil {
			return 0, err
		}
		if !ok || next >= end {
			break
		}
		from = floorTo(next, lv.bucket)
		to := min(from+chunkBuckets*lv.bucket, end)
		if err := d.aggregateChunk(ctx, lv, from, to); err != nil {
			return 0, err
		}
		done = max(done, to)
		from = to
	}
	if end > done {
		if err := setDoneUntil(ctx, d.store.db, lv.table, end); err != nil {
			return 0, err
		}
		done = end
	}
	return done, nil
}

// aggregateChunk aggregates from <= ts < to and records the progress in the
// same transaction. Its first statement writes, so it takes the write lock
// before reading and never has to upgrade a stale read snapshot.
func (d *Downsampler) aggregateChunk(ctx context.Context, lv level, from, to int64) error {
	tx, err := d.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit
	if _, err := tx.ExecContext(ctx, lv.insert, from, to); err != nil {
		return err
	}
	if err := setDoneUntil(ctx, tx, lv.table, to); err != nil {
		return err
	}
	return tx.Commit()
}

// expire deletes the rows of table with ts < before and returns how many it
// deleted. Each statement deletes at most deleteBatch rows of one series,
// found through the primary key.
func (d *Downsampler) expire(ctx context.Context, table string, before int64) (int64, error) {
	ids, err := seriesIDList(ctx, d.store.db)
	if err != nil {
		return 0, err
	}
	stmt := fmt.Sprintf(`DELETE FROM %[1]s WHERE series_id = ?1 AND ts IN (
		SELECT ts FROM %[1]s WHERE series_id = ?1 AND ts < ?2 ORDER BY ts LIMIT ?3)`, table)
	var total int64
	for _, id := range ids {
		for {
			res, err := d.store.db.ExecContext(ctx, stmt, id, before, deleteBatch)
			if err != nil {
				return total, err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return total, err
			}
			total += n
			if n < deleteBatch {
				break
			}
		}
	}
	return total, nil
}

// execer is what *sql.DB and *sql.Tx have in common.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func doneUntil(ctx context.Context, db *sql.DB, table string) (int64, bool, error) {
	var ts int64
	err := db.QueryRowContext(ctx, "SELECT done_until FROM rollup_state WHERE name = ?", table).Scan(&ts)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return ts, err == nil, err
}

// setDoneUntil records that table is complete up to ts. It never moves
// backwards, so aggregating old buckets again keeps the later mark.
func setDoneUntil(ctx context.Context, db execer, table string, ts int64) error {
	_, err := db.ExecContext(ctx, `INSERT INTO rollup_state (name, done_until) VALUES (?1, ?2)
		ON CONFLICT (name) DO UPDATE SET done_until = MAX(done_until, excluded.done_until)`, table, ts)
	return err
}

// nextTS returns the oldest ts >= from in table, using the primary key of
// each series.
func nextTS(ctx context.Context, db *sql.DB, table string, from int64) (int64, bool, error) {
	var ts sql.NullInt64
	q := fmt.Sprintf(`SELECT MIN((SELECT MIN(ts) FROM %s WHERE series_id = s.id AND ts >= ?1)) FROM series s`, table)
	if err := db.QueryRowContext(ctx, q, from).Scan(&ts); err != nil {
		return 0, false, err
	}
	return ts.Int64, ts.Valid, nil
}

func seriesIDList(ctx context.Context, db *sql.DB) ([]int64, error) {
	rows, err := db.QueryContext(ctx, "SELECT id FROM series ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func seconds(d time.Duration) int64 { return int64(d / time.Second) }

// floorTo rounds ts down to a multiple of step.
func floorTo(ts, step int64) int64 {
	r := ts % step
	if r < 0 {
		r += step
	}
	return ts - r
}

// ceilTo rounds ts up to a multiple of step.
func ceilTo(ts, step int64) int64 { return -floorTo(-ts, step) }
