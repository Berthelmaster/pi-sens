package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
	"github.com/Berthelmaster/pi-sens/internal/server"
)

// TestMetricsEndpoint wires the registered collectors, the sampler and the
// server together the way run does, with the uptime fixture as the root FS.
func TestMetricsEndpoint(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sampler := metrics.NewSampler(metrics.Options{
		Interval: time.Second,
		FS:       os.DirFS("../../internal/collectors/testdata/uptime"),
		Runner:   &metrics.FakeRunner{},
		Logger:   logger,
	})
	sampler.Sample(context.Background())
	srv := server.New(":0", "dev", logger, sampler)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body)
	}

	var body struct {
		Timestamp  time.Time                  `json:"timestamp"`
		Collectors map[string]json.RawMessage `json:"collectors"`
		Series     map[string]float64         `json:"series"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Timestamp.IsZero() {
		t.Error("timestamp is zero")
	}
	var up struct {
		Seconds float64 `json:"seconds"`
		Error   string  `json:"error"`
	}
	if err := json.Unmarshal(body.Collectors["uptime"], &up); err != nil {
		t.Fatalf("decode uptime section %s: %v", body.Collectors["uptime"], err)
	}
	if up.Error != "" || up.Seconds != 354127.42 {
		t.Errorf("uptime section = %s, want seconds 354127.42", body.Collectors["uptime"])
	}
	if got := body.Series["system.uptime_s"]; got != 354127.42 {
		t.Errorf("series system.uptime_s = %v, want 354127.42", got)
	}
}
