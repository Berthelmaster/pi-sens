package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/history"
)

// histBase is midnight UTC, so it starts a bucket of every size the tests
// see.
var histBase = time.Unix(1_700_006_400, 0)

// histMinutes is how many minutes seedHistory stores, 8 days.
const histMinutes = 8 * 24 * 60

// histEnd is the end of the seeded data.
var histEnd = histBase.Add(histMinutes * time.Minute)

// seedHistory opens a database in a temp directory with one sample a minute
// of "cpu.usage_pct" for 8 days, the i-th minute's sample having value i,
// and one "temp.cpu_c" sample. Both rollup tables are filled in too.
func seedHistory(t *testing.T) *history.Store {
	t.Helper()
	ctx := context.Background()
	store, err := history.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	samples := make([]history.Sample, histMinutes)
	for i := range samples {
		samples[i] = history.Sample{
			Time:   histBase.Add(time.Duration(i) * time.Minute),
			Values: map[string]float64{"cpu.usage_pct": float64(i)},
		}
	}
	samples[0].Values["temp.cpu_c"] = 50
	if err := store.Insert(ctx, samples); err != nil {
		t.Fatal(err)
	}
	d := history.NewDownsampler(store, history.DownsamplerOptions{
		Retention: history.Retention{Raw: 365 * 24 * time.Hour, Minute: 365 * 24 * time.Hour, Quarter: 365 * 24 * time.Hour},
		Logger:    discardLogger(),
		Now:       func() time.Time { return histEnd.Add(time.Hour) },
	})
	if err := d.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	return store
}

type historyBody struct {
	Resolution string `json:"resolution"`
	Step       int64  `json:"step"`
	Series     map[string]struct {
		T   []int64   `json:"t"`
		Avg []float64 `json:"avg"`
		Min []float64 `json:"min"`
		Max []float64 `json:"max"`
	} `json:"series"`
}

func newHistoryServer(t *testing.T, store HistorySource) *Server {
	t.Helper()
	s := New(":0", "dev", testUI, discardLogger(), &fakeSource{}, nil, store)
	s.now = func() time.Time { return histEnd }
	return s
}

func getHistory(t *testing.T, s *Server, params string) historyBody {
	t.Helper()
	rec := get(t, s, "/api/history?"+params)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/history?%s status = %d: %s", params, rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var body historyBody
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return body
}

func TestHistoryResolutions(t *testing.T) {
	s := newHistoryServer(t, seedHistory(t))
	end := histEnd.Unix()

	tests := []struct {
		name       string
		span       time.Duration
		points     string // empty for the default
		resolution string
		step       int64
		count      int
		group      int64 // seeded samples per point
	}{
		// One hour of raw samples, one point per sample.
		{"raw", time.Hour, "3600", "raw", 1, 60, 1},
		// Raw samples grouped into 10 minute buckets.
		{"raw grouped", 6 * time.Hour, "36", "raw", 600, 36, 10},
		// Two days of minute rollups in 500 points: 6 minute buckets.
		{"1m", 48 * time.Hour, "", "1m", 360, 480, 6},
		// Eight days of quarter rollups in 500 points: 30 minute buckets.
		{"15m", 8 * 24 * time.Hour, "", "15m", 1800, 384, 30},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			from := end - int64(tt.span/time.Second)
			params := fmt.Sprintf("series=cpu.usage_pct&from=%d&to=%d", from, end)
			if tt.points != "" {
				params += "&points=" + tt.points
			}
			body := getHistory(t, s, params)
			if body.Resolution != tt.resolution || body.Step != tt.step {
				t.Fatalf("resolution %q, step %d; want %q, step %d", body.Resolution, body.Step, tt.resolution, tt.step)
			}
			cpu := body.Series["cpu.usage_pct"]
			spacing := max(tt.step, 60) // samples are a minute apart
			if len(cpu.T) != tt.count || len(cpu.Avg) != tt.count || len(cpu.Min) != tt.count || len(cpu.Max) != tt.count {
				t.Fatalf("got %d/%d/%d/%d points, want %d", len(cpu.T), len(cpu.Avg), len(cpu.Min), len(cpu.Max), tt.count)
			}
			for k, ts := range cpu.T {
				// The first sample in the bucket has the minute index as value.
				first := float64((ts - histBase.Unix()) / 60)
				wantAvg := first + float64(tt.group-1)/2
				if ts != from+int64(k)*spacing || cpu.Min[k] != first || cpu.Max[k] != first+float64(tt.group-1) || cpu.Avg[k] != wantAvg {
					t.Fatalf("point %d = t %d avg %v min %v max %v; want t %d avg %v min %v max %v", k,
						ts, cpu.Avg[k], cpu.Min[k], cpu.Max[k], from+int64(k)*spacing, wantAvg, first, first+float64(tt.group-1))
				}
			}
		})
	}
}

func TestHistoryDefaultsAndSeveralSeries(t *testing.T) {
	s := newHistoryServer(t, seedHistory(t))

	// to defaults to now, and series can be repeated, padded and listed
	// twice. A key without data comes back empty.
	from := histBase.Unix()
	body := getHistory(t, s, fmt.Sprintf("series=temp.cpu_c,%%20nope&series=temp.cpu_c&from=%d&points=5000", from))
	if body.Resolution != "15m" || len(body.Series) != 2 {
		t.Fatalf("body = %+v, want 15m rollups of 2 series", body)
	}
	temp := body.Series["temp.cpu_c"]
	if !reflect.DeepEqual(temp.T, []int64{from}) || !reflect.DeepEqual(temp.Avg, []float64{50}) {
		t.Errorf("temp.cpu_c = %+v, want one point of 50 at %d", temp, from)
	}
	nope, ok := body.Series["nope"]
	if !ok || nope.T == nil || len(nope.T) != 0 || nope.Avg == nil {
		t.Errorf("nope = %+v, want empty arrays", nope)
	}
}

func TestHistoryRejectsBadInput(t *testing.T) {
	s := newHistoryServer(t, seedHistory(t))
	end := histEnd.Unix()
	eleven := make([]string, 11)
	for i := range eleven {
		eleven[i] = fmt.Sprintf("s%d", i)
	}

	tests := []struct {
		params string
		want   string // part of the error message
	}{
		{"from=0", "series is required"},
		{"series=,%20,&from=0", "series is required"},
		{"series=" + strings.Join(eleven, ",") + "&from=0", "at most 10 series"},
		{"series=a&to=100", "from is required"},
		{"series=a&from=yesterday", "from:"},
		{"series=a&from=-1", "from:"},
		{"series=a&from=1.5", "from:"},
		{"series=a&from=0&to=", "to:"},
		{"series=a&from=0&to=99999999999999", "to:"},
		{fmt.Sprintf("series=a&from=%d&to=%d", end, end), "must be before to"},
		{fmt.Sprintf("series=a&from=%d&to=%d", end, end-60), "must be before to"},
		{fmt.Sprintf("series=a&from=%d", end+60), "must be before to"},
		{"series=a&from=0&points=1", "points must be"},
		{"series=a&from=0&points=5001", "points must be"},
		{"series=a&from=0&points=many", "points must be"},
	}
	for _, tt := range tests {
		rec := get(t, s, "/api/history?"+tt.params)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want %d", tt.params, rec.Code, http.StatusBadRequest)
			continue
		}
		var body errorResponse
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("%s: decode body: %v", tt.params, err)
		}
		if !strings.Contains(body.Error, tt.want) {
			t.Errorf("%s: error = %q, want it to contain %q", tt.params, body.Error, tt.want)
		}
	}
}

func TestSeriesEndpoint(t *testing.T) {
	s := newHistoryServer(t, seedHistory(t))

	for prefix, want := range map[string][]string{
		"":     {"cpu.usage_pct", "temp.cpu_c"},
		"cpu.": {"cpu.usage_pct"},
		"disk": {},
	} {
		rec := get(t, s, "/api/series?prefix="+url.QueryEscape(prefix))
		if rec.Code != http.StatusOK {
			t.Fatalf("prefix %q: status = %d: %s", prefix, rec.Code, rec.Body)
		}
		var body struct {
			Series []string `json:"series"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if !reflect.DeepEqual(body.Series, want) {
			t.Errorf("prefix %q: series = %q, want %q", prefix, body.Series, want)
		}
	}
}

func TestHistoryDatabaseError(t *testing.T) {
	store := seedHistory(t)
	s := newHistoryServer(t, store)
	store.Close()

	for _, target := range []string{"/api/series", "/api/history?series=a&from=0", "/api/energy/daily"} {
		if rec := get(t, s, target); rec.Code != http.StatusInternalServerError {
			t.Errorf("GET %s status = %d, want %d", target, rec.Code, http.StatusInternalServerError)
		}
	}
}

func TestHistoryWithoutSource(t *testing.T) {
	s := New(":0", "dev", testUI, discardLogger(), &fakeSource{}, nil, nil)
	for _, target := range []string{"/api/series", "/api/history?series=a&from=0", "/api/energy/daily"} {
		if rec := get(t, s, target); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want %d", target, rec.Code, http.StatusNotFound)
		}
	}
}
