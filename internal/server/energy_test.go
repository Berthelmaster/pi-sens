package server

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/history"
)

// energyZone is an hour east of UTC, so a local day is not a UTC day.
var energyZone = time.FixedZone("UTC+1", 3600)

// seedEnergy opens a database with energy.today_kwh as the energy collector
// writes it: one sample a minute from 2026-11-03 00:00 local to end,
// climbing through each day and restarting from zero at local midnight.
// The days end at 0.12 and 0.24 kWh; the third climbs at 0.0003 kWh a
// minute.
func seedEnergy(t *testing.T, end time.Time) *history.Store {
	t.Helper()
	ctx := context.Background()
	store, err := history.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	rates := []float64{0.12 / 1439, 0.24 / 1439, 0.0003}
	day0 := time.Date(2026, 11, 3, 0, 0, 0, 0, energyZone)
	var samples []history.Sample
	for ts := day0; ts.Before(end); ts = ts.Add(time.Minute) {
		day := int(ts.Sub(day0) / (24 * time.Hour))
		minute := float64(ts.Sub(day0.AddDate(0, 0, day)) / time.Minute)
		samples = append(samples, history.Sample{
			Time:   ts,
			Values: map[string]float64{energyTodayKey: rates[day] * minute, "energy.month_kwh": 5},
		})
	}
	if err := store.Insert(ctx, samples); err != nil {
		t.Fatal(err)
	}
	return store
}

type energyBody struct {
	Days []struct {
		Date  string   `json:"date"`
		Start int64    `json:"start"`
		KWh   *float64 `json:"kwh"`
		Cost  *float64 `json:"cost"`
	} `json:"days"`
	Price    *float64 `json:"price"`
	Currency *string  `json:"currency"`
}

func getEnergy(t *testing.T, s *Server, target string) energyBody {
	t.Helper()
	rec := get(t, s, target)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d: %s", target, rec.Code, rec.Body)
	}
	var body energyBody
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return body
}

func newEnergyServer(t *testing.T, store HistorySource, now time.Time, opts ...Option) *Server {
	t.Helper()
	s := New(":0", "dev", testUI, discardLogger(), &fakeSource{}, nil, store, opts...)
	s.now = func() time.Time { return now }
	s.loc = energyZone
	return s
}

func TestEnergyDailyAcrossMidnightResets(t *testing.T) {
	// Two full days and the third up to 10:00 local.
	now := time.Date(2026, 11, 5, 10, 0, 30, 0, energyZone)
	store := seedEnergy(t, now)
	s := newEnergyServer(t, store, now, WithEnergyPrice(2.5, "DKK"))

	body := getEnergy(t, s, "/api/energy/daily?days=4")
	type day struct {
		date      string
		start     int64
		kwh, cost float64 // -1 for null
	}
	var got []day
	for _, d := range body.Days {
		g := day{d.Date, d.Start, -1, -1}
		if d.KWh != nil {
			g.kwh = round6(*d.KWh)
		}
		if d.Cost != nil {
			g.cost = *d.Cost
		}
		got = append(got, g)
	}
	midnight := func(d int) int64 { return time.Date(2026, 11, d, 0, 0, 0, 0, energyZone).Unix() }
	want := []day{
		{"2026-11-02", midnight(2), -1, -1}, // nothing stored
		{"2026-11-03", midnight(3), 0.12, 0.3},
		{"2026-11-04", midnight(4), 0.24, 0.6},
		{"2026-11-05", midnight(5), 0.18, 0.45}, // 600 minutes so far
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("days =\n  %v\nwant\n  %v", got, want)
	}
	if body.Price == nil || *body.Price != 2.5 || body.Currency == nil || *body.Currency != "DKK" {
		t.Errorf("price %v currency %v, want 2.5 DKK", body.Price, body.Currency)
	}

	// The default is 30 days, ending today.
	body = getEnergy(t, s, "/api/energy/daily")
	if n := len(body.Days); n != 30 || body.Days[0].Date != "2026-10-07" || body.Days[29].Date != "2026-11-05" {
		t.Errorf("default: %d days from %v", n, body.Days)
	}
}

func TestEnergyDailyWithoutPrice(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, energyZone)
	s := newEnergyServer(t, seedEnergy(t, now), now)

	rec := get(t, s, "/api/energy/daily?days=1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	raw := rec.Body.String()
	if strings.Contains(raw, "cost") || strings.Contains(raw, "price") || strings.Contains(raw, "currency") {
		t.Errorf("body = %s, want no cost, price or currency", raw)
	}
	var body energyBody
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Days) != 1 || body.Days[0].KWh == nil || round6(*body.Days[0].KWh) != round6(0.12/1439*719) {
		t.Errorf("body = %s, want today up to 11:59", raw)
	}
}

func TestEnergyDailyEmpty(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, energyZone)
	s := newEnergyServer(t, seedEnergy(t, time.Unix(0, 0)), now, WithEnergyPrice(2.5, ""))
	body := getEnergy(t, s, "/api/energy/daily?days=3")
	if len(body.Days) != 3 {
		t.Fatalf("got %d days, want 3", len(body.Days))
	}
	for _, d := range body.Days {
		if d.KWh != nil || d.Cost != nil {
			t.Errorf("day %s = %v kWh, cost %v; want null and no cost", d.Date, d.KWh, d.Cost)
		}
	}
}

func TestEnergyDailyRejectsBadDays(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, energyZone)
	s := newEnergyServer(t, seedEnergy(t, time.Unix(0, 0)), now)
	for _, days := range []string{"0", "-1", "367", "ten", "1.5"} {
		rec := get(t, s, "/api/energy/daily?days="+days)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("days=%s: status = %d, want %d", days, rec.Code, http.StatusBadRequest)
			continue
		}
		var body errorResponse
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil || !strings.Contains(body.Error, "days must be") {
			t.Errorf("days=%s: error = %q (%v)", days, body.Error, err)
		}
	}
}

func round6(v float64) float64 { return float64(int64(v*1e6+0.5)) / 1e6 }
