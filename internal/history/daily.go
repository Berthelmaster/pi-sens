package history

// The highest value of a series per local calendar day.
//
// A counter that climbs during the day and resets at midnight, such as
// energy.today_kwh, ends each day at its highest value, so the day's maximum
// is the day's total. The maximum of a bucket's maximums is the maximum of
// the samples behind them, so every table gives the same answer and DailyMax
// reads each part of the range from the coarsest table that has it: the 15
// minute rollups up to where they are done, the 1 minute rollups from there
// up to where those are done, and raw samples for the rest. Thirty days are
// then about 3000 rows, not millions.
//
// A row belongs to the day its ts falls in. Rollup rows start at multiples
// of their bucket size since the epoch and every time zone in use is a whole
// number of quarter hours off UTC, so a bucket never straddles midnight.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"
)

// DayMax is one local calendar day of a DailyMax answer.
type DayMax struct {
	// Start is the day's midnight in the location DailyMax was given.
	Start time.Time
	// Max is the highest value stored for the day, valid if OK.
	Max float64
	OK  bool
}

// DailyMax returns the highest value of the series key on each calendar day
// in loc that [from, to) touches, oldest first, the first day being the one
// from falls in. Days without data have OK false. Rows before from or at or
// after to are left out, so the first and last day may be partial.
func (s *Store) DailyMax(ctx context.Context, key string, from, to time.Time, loc *time.Location) ([]DayMax, error) {
	if !from.Before(to) {
		return nil, errors.New("history: daily range is empty")
	}
	if loc == nil {
		loc = time.Local
	}
	days := dayStarts(from, to, loc)
	out := make([]DayMax, len(days))
	for i, d := range days {
		out[i].Start = d
	}

	// One read transaction, so the done marks and the rows are from the
	// same snapshot even if the downsampler runs meanwhile.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("history: daily max: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	done15, err := txDoneUntil(ctx, tx, "samples_15m")
	if err != nil {
		return nil, fmt.Errorf("history: daily max: %w", err)
	}
	done1, err := txDoneUntil(ctx, tx, "samples_1m")
	if err != nil {
		return nil, fmt.Errorf("history: daily max: %w", err)
	}
	done1 = max(done1, done15)

	lo, hi := from.Unix(), to.Unix()
	parts := []struct {
		table, column string
		from, to      int64
	}{
		{"samples_15m", "max", lo, min(hi, done15)},
		{"samples_1m", "max", max(lo, done15), min(hi, done1)},
		{"samples_raw", "value", max(lo, done1), hi},
	}
	unix := make([]int64, len(days))
	for i, d := range days {
		unix[i] = d.Unix()
	}
	for _, p := range parts {
		if p.from >= p.to {
			continue
		}
		err := dailyRows(ctx, tx, p.table, p.column, key, p.from, p.to, func(ts int64, v float64) {
			// The last day starting at or before ts.
			i := sort.Search(len(unix), func(i int) bool { return unix[i] > ts }) - 1
			if i < 0 {
				return
			}
			if d := &out[i]; !d.OK || v > d.Max {
				d.Max, d.OK = v, true
			}
		})
		if err != nil {
			return nil, fmt.Errorf("history: daily max %q: %w", key, err)
		}
	}
	return out, nil
}

// dayStarts returns the midnight in loc of every day that [from, to)
// touches. time.Date normalises the day, so a month end or a DST change
// needs no special case.
func dayStarts(from, to time.Time, loc *time.Location) []time.Time {
	y, m, d := from.In(loc).Date()
	var out []time.Time
	for i := 0; ; i++ {
		start := time.Date(y, m, d+i, 0, 0, 0, 0, loc)
		if !start.Before(to) {
			return out
		}
		out = append(out, start)
	}
}

// txDoneUntil is doneUntil inside a transaction, 0 when the table has not
// been aggregated yet.
func txDoneUntil(ctx context.Context, tx *sql.Tx, table string) (int64, error) {
	var ts int64
	err := tx.QueryRowContext(ctx, "SELECT done_until FROM rollup_state WHERE name = ?", table).Scan(&ts)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return ts, err
}

// dailyRows calls fn with ts and column of every row of the series key in
// table with from <= ts < to. table and column are constants from DailyMax.
func dailyRows(ctx context.Context, tx *sql.Tx, table, column, key string, from, to int64, fn func(int64, float64)) error {
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`
		SELECT r.ts, r.%s
		FROM series s CROSS JOIN %s r
		WHERE s.key = ?1 AND r.series_id = s.id AND r.ts >= ?2 AND r.ts < ?3
		ORDER BY r.ts`, column, table), key, from, to)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var ts int64
		var v float64
		if err := rows.Scan(&ts, &v); err != nil {
			return err
		}
		fn(ts, v)
	}
	return rows.Err()
}
