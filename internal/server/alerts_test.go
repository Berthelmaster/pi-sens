package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/alerts"
	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

func newTestEvaluator() *alerts.Evaluator {
	return alerts.NewEvaluator([]alerts.Rule{
		{Name: "uv", Series: "pi.undervoltage", Op: alerts.OpAbove, Threshold: 0, Severity: alerts.SeverityCritical},
		{Name: "hot", Series: "temp.cpu_c", Op: alerts.OpAbove, Threshold: 75, For: time.Minute, Severity: alerts.SeverityWarning},
	}, discardLogger())
}

func seriesAt(sec int, series map[string]float64) metrics.Snapshot {
	return metrics.Snapshot{Timestamp: snapAt(sec).Timestamp, Series: series}
}

// nextAlerts reads events until an alerts event, skipping heartbeats and
// snapshots.
func (r *sseReader) nextAlerts() alerts.Set {
	r.t.Helper()
	for {
		ev, err := r.next()
		if err != nil {
			r.t.Fatalf("read SSE event: %v", err)
		}
		if ev.event != "alerts" {
			continue
		}
		var set alerts.Set
		if err := json.Unmarshal([]byte(ev.data), &set); err != nil {
			r.t.Fatalf("decode alerts %q: %v", ev.data, err)
		}
		return set
	}
}

func TestAlertsEndpoint(t *testing.T) {
	ev := newTestEvaluator()
	ev.Evaluate(seriesAt(0, map[string]float64{"pi.undervoltage": 1}))
	ev.Evaluate(seriesAt(2, map[string]float64{"pi.undervoltage": 0}))
	ev.Evaluate(seriesAt(4, map[string]float64{"pi.undervoltage": 1, "temp.cpu_c": 80}))
	s := New(":0", "dev", testUI, discardLogger(), &fakeSource{}, ev)

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/alerts", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var body struct {
		Version  uint64           `json:"version"`
		Firing   []map[string]any `json:"firing"`
		Resolved []map[string]any `json:"resolved"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	// The pending temperature alert is not listed.
	if body.Version != 3 || len(body.Firing) != 1 || len(body.Resolved) != 1 {
		t.Fatalf("body = %+v, want one firing and one resolved alert at version 3", body)
	}
	f := body.Firing[0]
	if f["rule"] != "uv" || f["state"] != "firing" || f["firing_at"] != "2026-11-07T12:00:04Z" || f["resolved_at"] != nil {
		t.Errorf("firing alert = %v", f)
	}
	r := body.Resolved[0]
	if r["rule"] != "uv" || r["state"] != "resolved" || r["resolved_at"] != "2026-11-07T12:00:02Z" {
		t.Errorf("resolved alert = %v", r)
	}
}

func TestAlertsEndpointWithoutSource(t *testing.T) {
	s := New(":0", "dev", testUI, discardLogger(), &fakeSource{}, nil)

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/alerts", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestStreamSendsAlertsWhenTheyChange(t *testing.T) {
	src := &fakeSource{snap: snapAt(1), ok: true, subscribed: make(chan struct{}, 1)}
	ev := newTestEvaluator()
	ts := httptest.NewServer(New(":0", "dev", testUI, discardLogger(), src, ev).Handler())
	t.Cleanup(ts.Close) // runs after openStream's cleanup closes the stream

	resp := openStream(t, context.Background(), ts.URL)
	<-src.subscribed
	r := newSSEReader(t, resp.Body)

	// The latest snapshot comes first, then the current alerts.
	r.nextSnapshot()
	if set := r.nextAlerts(); set.Version != 0 || len(set.Firing) != 0 || set.Firing == nil {
		t.Fatalf("first alerts = %+v, want the empty set", set)
	}

	// A pending alert alone does not change the set; the firing one does.
	ev.Evaluate(seriesAt(2, map[string]float64{"temp.cpu_c": 80}))
	ev.Evaluate(seriesAt(3, map[string]float64{"temp.cpu_c": 80, "pi.undervoltage": 1}))
	set := r.nextAlerts()
	if set.Version != 1 || len(set.Firing) != 1 || set.Firing[0].Rule != "uv" || set.Resolved == nil {
		t.Fatalf("alerts = %+v, want uv firing at version 1", set)
	}

	ev.Evaluate(seriesAt(4, map[string]float64{"temp.cpu_c": 80, "pi.undervoltage": 0}))
	set = r.nextAlerts()
	if set.Version != 2 || len(set.Firing) != 0 || len(set.Resolved) != 1 {
		t.Fatalf("alerts = %+v, want uv resolved at version 2", set)
	}

	// Snapshots keep flowing after the alert source stops.
	snaps := make(chan metrics.Snapshot)
	close(snaps)
	ev.Run(context.Background(), snaps)
	src.publish(snapAt(5))
	if got := r.nextSnapshot(); !got.Timestamp.Equal(snapAt(5).Timestamp) {
		t.Fatalf("snapshot timestamp = %v, want %v", got.Timestamp, snapAt(5).Timestamp)
	}
}
