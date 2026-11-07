package history

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// dailyZone is an hour east of UTC, so local midnight is not UTC midnight.
var dailyZone = time.FixedZone("UTC+1", 3600)

// dailyRates is the kWh per minute that seedDaily's counter climbs by on each
// of its days, the first starting 2026-11-03 local.
var dailyRates = []float64{0.0001, 0.0002, 0.0003}

// seedDaily stores a counter like energy.today_kwh for key: one sample a
// minute, half a minute past, from 2026-11-03 00:00 local to end, that
// climbs by the day's rate each minute and restarts from zero at every
// midnight. It also stores a series "other" with larger values, which must
// not leak into key's answer.
func seedDaily(t *testing.T, s *Store, key string, end time.Time) {
	t.Helper()
	var samples []Sample
	day0 := time.Date(2026, 11, 3, 0, 0, 0, 0, dailyZone)
	for ts := day0.Add(30 * time.Second); ts.Before(end); ts = ts.Add(time.Minute) {
		day := int(ts.Sub(day0) / (24 * time.Hour))
		minute := float64(ts.Sub(day0.AddDate(0, 0, day)) / time.Minute)
		samples = append(samples, Sample{Time: ts, Values: map[string]float64{
			key:     dailyRates[day] * minute,
			"other": 1000 + minute,
		}})
	}
	if err := s.Insert(context.Background(), samples); err != nil {
		t.Fatal(err)
	}
}

func TestDailyMaxAcrossMidnightResets(t *testing.T) {
	s := openTest(t, t.TempDir())
	const key = "energy.today_kwh"
	// Two full days and the third up to 10:08:30 local.
	end := time.Date(2026, 11, 5, 10, 9, 0, 0, dailyZone)
	seedDaily(t, s, key, end)

	from := time.Date(2026, 11, 2, 0, 0, 0, 0, dailyZone) // a day with no data
	to := end.Add(time.Hour)
	day := func(d int) time.Time { return time.Date(2026, 11, 2+d, 0, 0, 0, 0, dailyZone) }
	want := []DayMax{
		{Start: day(0)},
		{Start: day(1), Max: dailyRates[0] * 1439, OK: true}, // 23:59:30
		{Start: day(2), Max: dailyRates[1] * 1439, OK: true},
		{Start: day(3), Max: dailyRates[2] * 608, OK: true}, // 10:08:30
	}
	check := func(when string) {
		t.Helper()
		got, err := s.DailyMax(context.Background(), key, from, to, dailyZone)
		if err != nil {
			t.Fatalf("%s: DailyMax() error = %v", when, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: DailyMax() =\n  %+v\nwant\n  %+v", when, got, want)
		}
	}

	// Only raw samples so far.
	check("raw only")

	// Aggregate up to 10:08 and expire raw samples older than an hour and
	// minute rollups older than three hours. The first two days are then
	// only in the 15 minute rollups, 10:00 to 10:08 only in the minute
	// rollups and the last sample only raw.
	clock := &fakeClock{now: end.Add(-15 * time.Second)}
	ret := Retention{Raw: time.Hour, Minute: 3 * time.Hour, Quarter: 90 * 24 * time.Hour}
	runOnce(t, newTestDownsampler(t, s, clock, ret, 0))
	if n, _ := count(t, s, "samples_raw"); n == 0 || n > 120 {
		t.Fatalf("samples_raw has %d rows after retention, want the last hour or so", n)
	}
	check("downsampled")
}

func TestDailyMaxDays(t *testing.T) {
	s := openTest(t, t.TempDir())
	// A DST change: Europe/Copenhagen's 2026-10-25 has 25 hours.
	cph, err := time.LoadLocation("Europe/Copenhagen")
	if err != nil {
		t.Skipf("no time zone data: %v", err)
	}
	from := time.Date(2026, 10, 24, 12, 0, 0, 0, cph)
	to := time.Date(2026, 10, 26, 0, 0, 1, 0, cph)
	got, err := s.DailyMax(context.Background(), "missing", from, to, cph)
	if err != nil {
		t.Fatal(err)
	}
	var starts []string
	for _, d := range got {
		if d.OK {
			t.Errorf("day %v has data", d.Start)
		}
		starts = append(starts, d.Start.Format(time.RFC3339))
	}
	want := []string{"2026-10-24T00:00:00+02:00", "2026-10-25T00:00:00+02:00", "2026-10-26T00:00:00+01:00"}
	if !reflect.DeepEqual(starts, want) {
		t.Errorf("days = %v, want %v", starts, want)
	}

	if _, err := s.DailyMax(context.Background(), "x", to, to, cph); err == nil {
		t.Error("DailyMax() with an empty range succeeded")
	}
}
