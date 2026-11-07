// Package energy adds up the energy the Pi uses from its power readings and
// keeps the totals in the data directory, so they survive restarts and
// deploys.
//
// A Meter is fed one power reading per sample with Add and integrates the
// watts over the real time between readings with the trapezoid rule. A gap
// longer than the meter's MaxGap (pisens was stopped, or the readings failed
// for a while) adds nothing: the meter does not guess what it missed. Totals
// are kept for today, yesterday, this month and since the first reading. The
// day and month roll over at midnight in the meter's time zone.
//
// The meter only knows the times it is given, so tests drive it with any
// clock they like.
package energy

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileName is the name of the state file in the data directory.
const FileName = "energy.json"

// DefaultMaxGap is the longest time between two readings that is still
// integrated.
const DefaultMaxGap = 2 * time.Minute

// SaveInterval is the shortest time between two writes of the state file
// while sampling. Close always writes.
const SaveInterval = time.Minute

const (
	dayLayout   = "2006-01-02"
	monthLayout = "2006-01"
)

// Options configures a Meter. All fields are optional.
type Options struct {
	// MaxGap defaults to DefaultMaxGap.
	MaxGap time.Duration
	// Location is the time zone whose midnight starts a new day. Defaults to
	// time.Local.
	Location *time.Location
	// Logger defaults to slog.Default().
	Logger *slog.Logger
}

// Totals is what the meter has counted, as of the time passed to Totals.
type Totals struct {
	// Live is true once the meter has had a reading since it was opened.
	// Totals loaded from the state file alone do not make it live.
	Live bool
	// PowerW is the latest reading, nil if there is none within MaxGap.
	PowerW *float64
	// TodayKWh, YesterdayKWh and MonthKWh count the calendar day, the day
	// before and the calendar month. TotalKWh counts everything since Since,
	// the time of the first reading ever.
	TodayKWh     float64
	YesterdayKWh float64
	MonthKWh     float64
	TotalKWh     float64
	Since        time.Time
}

// state is what the state file holds.
type state struct {
	// Day and Month are the local day ("2026-11-07") and month ("2026-11")
	// that TodayKWh and MonthKWh belong to.
	Day          string    `json:"day"`
	Month        string    `json:"month"`
	TodayKWh     float64   `json:"today_kwh"`
	YesterdayKWh float64   `json:"yesterday_kwh"`
	MonthKWh     float64   `json:"month_kwh"`
	TotalKWh     float64   `json:"total_kwh"`
	Since        time.Time `json:"since"`
	// LastTime and LastW are the latest reading, so a quick restart can
	// integrate across the time pisens was down if it is within MaxGap.
	LastTime time.Time `json:"last_time"`
	LastW    float64   `json:"last_w"`
}

// Meter accumulates energy. It is safe for concurrent use.
type Meter struct {
	path   string
	maxGap time.Duration
	loc    *time.Location
	logger *slog.Logger

	mu       sync.Mutex
	st       state
	live     bool
	dirty    bool
	lastSave time.Time // sample time of the last write, zero before the first
	saveErr  string    // last write error, so a failing disk is logged once
}

// Open creates a meter that keeps its state in dir/FileName and loads the
// totals saved there. A missing file starts from zero. A file that cannot be
// read as state is renamed to FileName+".bad" so it is not overwritten, and
// the meter starts from zero; the error says so and the meter is still
// usable.
func Open(dir string, opts Options) (*Meter, error) {
	m := &Meter{
		path:   filepath.Join(dir, FileName),
		maxGap: opts.MaxGap,
		loc:    opts.Location,
		logger: opts.Logger,
	}
	if m.maxGap <= 0 {
		m.maxGap = DefaultMaxGap
	}
	if m.loc == nil {
		m.loc = time.Local
	}
	if m.logger == nil {
		m.logger = slog.Default()
	}

	data, err := os.ReadFile(m.path)
	if errors.Is(err, fs.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return m, fmt.Errorf("read energy totals: %w", err)
	}
	var st state
	if err := json.Unmarshal(data, &st); err != nil || !st.valid() {
		if err == nil {
			err = errors.New("invalid values")
		}
		bad := m.path + ".bad"
		if rerr := os.Rename(m.path, bad); rerr != nil {
			return m, fmt.Errorf("read energy totals from %s: %w; moving it aside failed: %w", m.path, err, rerr)
		}
		return m, fmt.Errorf("read energy totals from %s: %w; moved it to %s and started from zero", m.path, err, bad)
	}
	m.st = st
	return m, nil
}

// valid reports whether loaded state makes sense, so a hand-edited or
// truncated file does not produce negative or NaN totals.
func (s state) valid() bool {
	for _, v := range []float64{s.TodayKWh, s.YesterdayKWh, s.MonthKWh, s.TotalKWh, s.LastW} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return false
		}
	}
	if s.Day != "" {
		if _, err := time.Parse(dayLayout, s.Day); err != nil {
			return false
		}
	}
	if s.Month != "" {
		if _, err := time.Parse(monthLayout, s.Month); err != nil {
			return false
		}
	}
	return true
}

// Path returns the state file's path.
func (m *Meter) Path() string { return m.path }

// Add records a power reading of w watts taken at t. The energy since the
// previous reading is added if the two are at most MaxGap apart; a reading
// that is not later than the previous one only replaces it. Negative, NaN
// and infinite readings are ignored.
//
// Add writes the state file when the last write is SaveInterval or more
// before t.
func (m *Meter) Add(t time.Time, w float64) {
	if math.IsNaN(w) || math.IsInf(w, 0) || w < 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	st := &m.st
	if last := st.LastTime; !last.IsZero() {
		if dt := t.Sub(last); dt > 0 && dt <= m.maxGap {
			kwh := (st.LastW + w) / 2 * dt.Hours() / 1000
			// The part of an interval that started before midnight belongs
			// to the day before.
			if midnight := m.startOfDay(t); last.Before(midnight) {
				before := kwh * float64(midnight.Sub(last)) / float64(dt)
				m.roll(last)
				m.add(before)
				kwh -= before
			}
			m.roll(t)
			m.add(kwh)
		}
	}
	m.roll(t)
	if st.Since.IsZero() {
		st.Since = t
	}
	st.LastTime, st.LastW = t, w
	m.live = true
	m.dirty = true

	if m.lastSave.IsZero() || t.Sub(m.lastSave) >= SaveInterval || t.Before(m.lastSave) {
		m.lastSave = t
		_ = m.saveLocked() // logged there; tried again in a minute
	}
}

// Totals returns the counters as of t, rolling the day and month over first
// if t is past midnight.
func (m *Meter) Totals(t time.Time) Totals {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.roll(t)
	st := m.st
	out := Totals{
		Live:         m.live,
		TodayKWh:     st.TodayKWh,
		YesterdayKWh: st.YesterdayKWh,
		MonthKWh:     st.MonthKWh,
		TotalKWh:     st.TotalKWh,
		Since:        st.Since,
	}
	if m.live {
		if age := t.Sub(st.LastTime); age >= 0 && age <= m.maxGap {
			w := st.LastW
			out.PowerW = &w
		}
	}
	return out
}

// Close writes the state file if anything changed since the last write. The
// meter can still be used afterwards.
func (m *Meter) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saveLocked()
}

func (m *Meter) add(kwh float64) {
	m.st.TodayKWh += kwh
	m.st.MonthKWh += kwh
	m.st.TotalKWh += kwh
}

// roll moves the counters on to t's day and month. Time never goes back for
// the counters: a t on an earlier day than the current one changes nothing.
func (m *Meter) roll(t time.Time) {
	lt := t.In(m.loc)
	day, month := lt.Format(dayLayout), lt.Format(monthLayout)
	st := &m.st

	if st.Day == "" {
		st.Day, st.Month = day, month
		m.dirty = true
		return
	}
	if day <= st.Day {
		return
	}
	// Yesterday is only yesterday if the stored day is the one before t's.
	prev, _ := time.ParseInLocation(dayLayout, st.Day, m.loc)
	if prev.AddDate(0, 0, 1).Format(dayLayout) == day {
		st.YesterdayKWh = st.TodayKWh
	} else {
		st.YesterdayKWh = 0
	}
	st.TodayKWh = 0
	st.Day = day
	if month != st.Month {
		st.MonthKWh = 0
		st.Month = month
	}
	m.dirty = true
}

// startOfDay returns local midnight at the start of t's day.
func (m *Meter) startOfDay(t time.Time) time.Time {
	lt := t.In(m.loc)
	return time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, m.loc)
}

// saveLocked writes the state file through a temporary file and a rename, so
// a crash mid-write leaves the previous totals rather than a broken file.
// Write errors are logged once until a write succeeds again.
func (m *Meter) saveLocked() error {
	// Without a reading ever there is nothing worth keeping, and a Pi
	// without PMIC readings gets no file at all.
	if !m.dirty || m.st.LastTime.IsZero() {
		return nil
	}
	err := m.write()
	if err != nil {
		if msg := err.Error(); msg != m.saveErr {
			m.logger.Warn("save energy totals", "path", m.path, "err", msg)
			m.saveErr = msg
		}
		return err
	}
	if m.saveErr != "" {
		m.logger.Info("energy totals saved again", "path", m.path)
		m.saveErr = ""
	}
	m.dirty = false
	return nil
}

func (m *Meter) write() error {
	data, err := json.MarshalIndent(m.st, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(m.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, FileName+".tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op once renamed
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), m.path)
}
