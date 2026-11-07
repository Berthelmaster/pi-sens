package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

// fakeSource serves a fixed latest snapshot. Snapshots passed to publish go
// to every subscriber.
type fakeSource struct {
	snap metrics.Snapshot
	ok   bool

	mu   sync.Mutex
	subs map[chan metrics.Snapshot]struct{}
	// subscribed receives a value on every Subscribe call when non-nil.
	subscribed chan struct{}
}

func (f *fakeSource) Latest() (metrics.Snapshot, bool) { return f.snap, f.ok }

func (f *fakeSource) Subscribe() (<-chan metrics.Snapshot, func()) {
	ch := make(chan metrics.Snapshot, metrics.SubscriberBuffer)
	f.mu.Lock()
	if f.subs == nil {
		f.subs = map[chan metrics.Snapshot]struct{}{}
	}
	f.subs[ch] = struct{}{}
	f.mu.Unlock()
	if f.subscribed != nil {
		f.subscribed <- struct{}{}
	}
	return ch, func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		if _, ok := f.subs[ch]; ok {
			delete(f.subs, ch)
			close(ch)
		}
	}
}

func (f *fakeSource) publish(snap metrics.Snapshot) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for ch := range f.subs {
		ch <- snap
	}
}

// stop closes every subscriber channel, like the sampler does when it stops.
func (f *fakeSource) stop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for ch := range f.subs {
		delete(f.subs, ch)
		close(ch)
	}
}

func (f *fakeSource) subscribers() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.subs)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestHealth(t *testing.T) {
	s := New(":0", "1.2.3", testUI, discardLogger(), &fakeSource{})

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	want := map[string]string{"status": "ok", "version": "1.2.3"}
	if len(body) != len(want) || body["status"] != want["status"] || body["version"] != want["version"] {
		t.Errorf("body = %v, want %v", body, want)
	}
}

func TestHealthRejectsOtherMethods(t *testing.T) {
	s := New(":0", "dev", testUI, discardLogger(), &fakeSource{})

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/health", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestServeShutsDownOnCancel(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := New(ln.Addr().String(), "dev", testUI, discardLogger(), &fakeSource{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()

	resp, err := http.Get("http://" + ln.Addr().String() + "/api/health")
	if err != nil {
		t.Fatalf("GET /api/health: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() error = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve() did not return after context cancel")
	}
}

func TestMetrics(t *testing.T) {
	ts := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	src := &fakeSource{ok: true, snap: metrics.Snapshot{
		Timestamp:  ts,
		Collectors: map[string]any{"uptime": json.RawMessage(`{"seconds":42}`)},
		Series:     map[string]float64{"system.uptime_s": 42},
	}}
	s := New(":0", "dev", testUI, discardLogger(), src)

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/metrics", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var body struct {
		Timestamp  time.Time                  `json:"timestamp"`
		Collectors map[string]json.RawMessage `json:"collectors"`
		Series     map[string]float64         `json:"series"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !body.Timestamp.Equal(ts) || string(body.Collectors["uptime"]) != `{"seconds":42}` || body.Series["system.uptime_s"] != 42 {
		t.Errorf("body = %+v", body)
	}
}

func TestMetricsBeforeFirstSample(t *testing.T) {
	s := New(":0", "dev", testUI, discardLogger(), &fakeSource{})

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/metrics", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}
