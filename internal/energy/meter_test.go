package energy

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// cph is a fixed zone with an offset, so the tests show that days roll over
// at local midnight and not at UTC midnight.
var cph = time.FixedZone("CET", 3600)

// t0 is 12:00 local time.
var t0 = time.Date(2026, 11, 7, 12, 0, 0, 0, cph)

func open(t *testing.T, dir string) *Meter {
	t.Helper()
	m, err := Open(dir, Options{Location: cph})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	return m
}

// feed adds a reading of w watts every step from start for d, both ends
// included, and returns the time of the last one.
func feed(m *Meter, start time.Time, d, step time.Duration, w float64) time.Time {
	t := start
	for ; !t.After(start.Add(d)); t = t.Add(step) {
		m.Add(t, w)
	}
	return t.Add(-step)
}

func near(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %.9f, want %.9f", name, got, want)
	}
}

func TestConstantPowerForAnHour(t *testing.T) {
	m := open(t, t.TempDir())
	end := feed(m, t0, time.Hour, 2*time.Second, 5)

	got := m.Totals(end)
	near(t, "TodayKWh", got.TodayKWh, 0.005)
	near(t, "MonthKWh", got.MonthKWh, 0.005)
	near(t, "TotalKWh", got.TotalKWh, 0.005)
	near(t, "YesterdayKWh", got.YesterdayKWh, 0)
	if !got.Live {
		t.Error("Live = false after readings")
	}
	if got.PowerW == nil || *got.PowerW != 5 {
		t.Errorf("PowerW = %v, want 5", got.PowerW)
	}
	if !got.Since.Equal(t0) {
		t.Errorf("Since = %v, want %v", got.Since, t0)
	}
}

func TestTrapezoid(t *testing.T) {
	m := open(t, t.TempDir())
	// From 4 W to 6 W over 36 seconds averages 5 W: 5 * 36 / 3600 Wh.
	m.Add(t0, 4)
	m.Add(t0.Add(36*time.Second), 6)
	near(t, "TotalKWh", m.Totals(t0.Add(36*time.Second)).TotalKWh, 0.00005)
}

func TestGapAddsNothing(t *testing.T) {
	m := open(t, t.TempDir())
	end := feed(m, t0, 30*time.Minute, 2*time.Second, 5)
	// pisens is down for 10 minutes, then runs another 30.
	end = feed(m, end.Add(10*time.Minute), 30*time.Minute, 2*time.Second, 5)

	got := m.Totals(end)
	near(t, "TodayKWh", got.TodayKWh, 0.005)
	near(t, "TotalKWh", got.TotalKWh, 0.005)
}

func TestShortGapIsIntegrated(t *testing.T) {
	m := open(t, t.TempDir())
	m.Add(t0, 6)
	m.Add(t0.Add(DefaultMaxGap), 6) // exactly MaxGap still counts
	near(t, "TotalKWh", m.Totals(t0.Add(DefaultMaxGap)).TotalKWh, 6*2.0/60/1000)

	m.Add(t0.Add(2*DefaultMaxGap+time.Second), 6) // just over does not
	near(t, "TotalKWh", m.Totals(t0.Add(2*DefaultMaxGap+time.Second)).TotalKWh, 6*2.0/60/1000)
}

func TestBadReadingsAreIgnored(t *testing.T) {
	m := open(t, t.TempDir())
	m.Add(t0, 5)
	m.Add(t0.Add(time.Second), math.NaN())
	m.Add(t0.Add(time.Second), math.Inf(1))
	m.Add(t0.Add(time.Second), -1)
	m.Add(t0, 5) // not later than the previous reading
	m.Add(t0.Add(-time.Second), 5)
	m.Add(t0.Add(-time.Second+36*time.Second), 5)
	got := m.Totals(t0.Add(35 * time.Second))
	near(t, "TotalKWh", got.TotalKWh, 5*36.0/3600/1000)
}

func TestMidnightRollsTodayIntoYesterday(t *testing.T) {
	m := open(t, t.TempDir())
	evening := time.Date(2026, 11, 7, 23, 0, 0, 0, cph)
	// 23:00 to 01:00 at a constant 3.6 W is 3.6 Wh before and 3.6 Wh after
	// midnight. Readings every 7 seconds put midnight inside an interval.
	end := feed(m, evening, 2*time.Hour, 7*time.Second, 3.6)
	got := m.Totals(end)
	afterMidnight := end.Sub(time.Date(2026, 11, 8, 0, 0, 0, 0, cph)).Hours() * 3.6 / 1000
	near(t, "YesterdayKWh", got.YesterdayKWh, 0.0036)
	near(t, "TodayKWh", got.TodayKWh, afterMidnight)
	near(t, "MonthKWh", got.MonthKWh, 0.0036+afterMidnight)
	near(t, "TotalKWh", got.TotalKWh, 0.0036+afterMidnight)
}

func TestTotalsRollOverWithoutReadings(t *testing.T) {
	m := open(t, t.TempDir())
	feed(m, t0, time.Hour, 10*time.Second, 5)

	// The next morning, before any new reading: today starts at zero.
	got := m.Totals(t0.Add(20 * time.Hour))
	near(t, "TodayKWh", got.TodayKWh, 0)
	near(t, "YesterdayKWh", got.YesterdayKWh, 0.005)
	if got.PowerW != nil {
		t.Errorf("PowerW = %v after hours without a reading, want nil", *got.PowerW)
	}

	// Two days on, yesterday had nothing either.
	got = m.Totals(t0.Add(48 * time.Hour))
	near(t, "YesterdayKWh", got.YesterdayKWh, 0)
	near(t, "MonthKWh", got.MonthKWh, 0.005)
	near(t, "TotalKWh", got.TotalKWh, 0.005)
}

func TestMonthRollover(t *testing.T) {
	m := open(t, t.TempDir())
	lastDay := time.Date(2026, 11, 30, 22, 0, 0, 0, cph)
	end := feed(m, lastDay, 4*time.Hour, 10*time.Second, 5) // until 02:00 on December 1st

	got := m.Totals(end)
	near(t, "YesterdayKWh", got.YesterdayKWh, 0.010)
	near(t, "TodayKWh", got.TodayKWh, 0.010)
	near(t, "MonthKWh", got.MonthKWh, 0.010)
	near(t, "TotalKWh", got.TotalKWh, 0.020)

	// Local midnight decides, not UTC midnight: 22:30 to 23:30 UTC is 23:30
	// to 00:30 local time, so half of it is already December.
	m2 := open(t, t.TempDir())
	end = feed(m2, time.Date(2026, 11, 30, 22, 30, 0, 0, time.UTC), time.Hour, 10*time.Second, 5)
	got = m2.Totals(end)
	near(t, "local MonthKWh", got.MonthKWh, 0.0025)
	near(t, "local TodayKWh", got.TodayKWh, 0.0025)
	near(t, "local YesterdayKWh", got.YesterdayKWh, 0.0025)
}

func TestRestartContinues(t *testing.T) {
	dir := t.TempDir()
	m := open(t, dir)
	end := feed(m, t0, 30*time.Minute, 2*time.Second, 5)
	if err := m.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// A deploy restarts pisens within a minute: the gap is bridged and the
	// totals carry on as if it never stopped.
	m2 := open(t, dir)
	before := m2.Totals(end)
	if before.Live {
		t.Error("Live = true before the first reading after a restart")
	}
	near(t, "TotalKWh after loading", before.TotalKWh, 0.0025)
	if !before.Since.Equal(t0) {
		t.Errorf("Since = %v after loading, want %v", before.Since, t0)
	}
	end = feed(m2, end.Add(30*time.Second), 30*time.Minute-30*time.Second, 2*time.Second, 5)

	got := m2.Totals(end)
	near(t, "TodayKWh", got.TodayKWh, 0.005)
	near(t, "TotalKWh", got.TotalKWh, 0.005)
	if !got.Since.Equal(t0) {
		t.Errorf("Since = %v, want %v", got.Since, t0)
	}
}

func TestRestartAfterLongDowntime(t *testing.T) {
	dir := t.TempDir()
	m := open(t, dir)
	end := feed(m, t0, time.Hour, 2*time.Second, 5)
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}

	// Down for three days: the old totals become nothing but the total and
	// (in the same month) the month.
	m2 := open(t, dir)
	later := end.Add(72 * time.Hour)
	feed(m2, later, time.Hour, 2*time.Second, 5)
	got := m2.Totals(later.Add(time.Hour))
	near(t, "TodayKWh", got.TodayKWh, 0.005)
	near(t, "YesterdayKWh", got.YesterdayKWh, 0)
	near(t, "MonthKWh", got.MonthKWh, 0.010)
	near(t, "TotalKWh", got.TotalKWh, 0.010)
}

func TestSavesAtMostOnceAMinute(t *testing.T) {
	dir := t.TempDir()
	m := open(t, dir)
	path := filepath.Join(dir, FileName)

	m.Add(t0, 5) // the first reading is saved right away
	saved := readState(t, path)
	if !saved.LastTime.Equal(t0) {
		t.Fatalf("saved LastTime = %v, want %v", saved.LastTime, t0)
	}

	feed(m, t0.Add(2*time.Second), 50*time.Second, 2*time.Second, 5)
	if saved := readState(t, path); !saved.LastTime.Equal(t0) {
		t.Errorf("saved LastTime = %v within a minute, want still %v", saved.LastTime, t0)
	}

	m.Add(t0.Add(time.Minute), 5)
	if saved := readState(t, path); !saved.LastTime.Equal(t0.Add(time.Minute)) {
		t.Errorf("saved LastTime = %v after a minute, want %v", saved.LastTime, t0.Add(time.Minute))
	}

	m.Add(t0.Add(time.Minute+2*time.Second), 5)
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if saved := readState(t, path); !saved.LastTime.Equal(t0.Add(time.Minute + 2*time.Second)) {
		t.Errorf("saved LastTime = %v after Close, want the last reading", saved.LastTime)
	}
}

func TestCloseWithoutReadingsWritesNothing(t *testing.T) {
	dir := t.TempDir()
	m := open(t, dir)
	m.Totals(t0) // no PMIC: only ever asked for totals
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, FileName)); err == nil {
		t.Errorf("%s written without any reading", FileName)
	}
}

func TestOpenBadFile(t *testing.T) {
	for name, content := range map[string]string{
		"not json":       "{",
		"negative total": `{"total_kwh": -1}`,
		"bad day":        `{"day": "yesterday"}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, FileName)
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			m, err := Open(dir, Options{Location: cph})
			if err == nil {
				t.Fatal("Open() error = nil, want one")
			}
			if m == nil {
				t.Fatal("Open() returned no meter")
			}
			if b, err := os.ReadFile(path + ".bad"); err != nil || string(b) != content {
				t.Errorf("bad file not moved aside: %q, %v", b, err)
			}
			feed(m, t0, time.Hour, 2*time.Second, 5)
			near(t, "TotalKWh", m.Totals(t0.Add(time.Hour)).TotalKWh, 0.005)
		})
	}
}

func readState(t *testing.T, path string) state {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var st state
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatal(err)
	}
	return st
}
