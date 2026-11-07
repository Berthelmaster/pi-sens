package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
	"github.com/Berthelmaster/pi-sens/internal/promtext"
)

func TestPrometheus(t *testing.T) {
	src := &fakeSource{ok: true, snap: metrics.Snapshot{
		Timestamp: time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC),
		Series:    map[string]float64{"temp.cpu_c": 48.5, "cpu.core3.usage_pct": 7},
	}}
	s := New(":0", "dev", testUI, discardLogger(), src, nil, nil)
	s.EnablePrometheus()

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != promtext.ContentType {
		t.Errorf("Content-Type = %q, want %q", ct, promtext.ContentType)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"# TYPE pisens_temperature_celsius gauge\n",
		`pisens_temperature_celsius{sensor="cpu"} 48.5` + "\n",
		`pisens_cpu_usage_percent{core="3"} 7` + "\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
}

func TestPrometheusBeforeFirstSample(t *testing.T) {
	s := New(":0", "dev", testUI, discardLogger(), &fakeSource{}, nil, nil)
	s.EnablePrometheus()

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestPrometheusDisabled(t *testing.T) {
	src := &fakeSource{ok: true, snap: metrics.Snapshot{Series: map[string]float64{"temp.cpu_c": 48.5}}}
	s := New(":0", "dev", testUI, discardLogger(), src, nil, nil)

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (not the SPA page)", rec.Code, http.StatusNotFound)
	}
}
