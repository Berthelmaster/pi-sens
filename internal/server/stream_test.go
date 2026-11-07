package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

func snapAt(sec int) metrics.Snapshot {
	return metrics.Snapshot{
		Timestamp:  time.Date(2026, 11, 7, 12, 0, sec, 0, time.UTC),
		Collectors: map[string]any{"uptime": json.RawMessage(`{"seconds":42}`)},
		Series:     map[string]float64{"system.uptime_s": float64(sec)},
	}
}

// sseEvent is one parsed SSE message. Comments have only comment set.
type sseEvent struct {
	event   string
	data    string
	comment string
}

type sseReader struct {
	t  *testing.T
	br *bufio.Reader
}

func newSSEReader(t *testing.T, body io.Reader) *sseReader {
	return &sseReader{t: t, br: bufio.NewReader(body)}
}

// next reads up to and including the next blank line.
func (r *sseReader) next() (sseEvent, error) {
	var ev sseEvent
	for {
		line, err := r.br.ReadString('\n')
		if err != nil {
			return ev, err
		}
		line = strings.TrimSuffix(line, "\n")
		switch {
		case line == "":
			return ev, nil
		case strings.HasPrefix(line, ":"):
			ev.comment = strings.TrimSpace(line[1:])
		case strings.HasPrefix(line, "event: "):
			ev.event = line[len("event: "):]
		case strings.HasPrefix(line, "data: "):
			ev.data += line[len("data: "):]
		default:
			r.t.Fatalf("unexpected SSE line %q", line)
		}
	}
}

// nextSnapshot reads events until a snapshot, skipping heartbeats.
func (r *sseReader) nextSnapshot() metrics.Snapshot {
	r.t.Helper()
	for {
		ev, err := r.next()
		if err != nil {
			r.t.Fatalf("read SSE event: %v", err)
		}
		if ev.comment != "" {
			continue
		}
		if ev.event != "snapshot" {
			r.t.Fatalf("event = %q, want snapshot", ev.event)
		}
		var snap metrics.Snapshot
		if err := json.Unmarshal([]byte(ev.data), &snap); err != nil {
			r.t.Fatalf("decode snapshot %q: %v", ev.data, err)
		}
		return snap
	}
}

// openStream starts GET /api/stream and fails the test unless it answers 200
// with an event stream. The stream is closed when ctx is cancelled.
func openStream(t *testing.T, ctx context.Context, baseURL string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/stream: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	return resp
}

// waitFor polls cond until it is true or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestStreamSendsLatestThenEverySnapshot(t *testing.T) {
	src := &fakeSource{snap: snapAt(1), ok: true, subscribed: make(chan struct{}, 1)}
	ts := httptest.NewServer(New(":0", "dev", discardLogger(), src).Handler())
	t.Cleanup(ts.Close) // runs after openStream's cleanup closes the stream

	resp := openStream(t, context.Background(), ts.URL)
	<-src.subscribed
	r := newSSEReader(t, resp.Body)

	if got := r.nextSnapshot(); !got.Timestamp.Equal(snapAt(1).Timestamp) || got.Series["system.uptime_s"] != 1 {
		t.Fatalf("first snapshot = %+v, want the latest one", got)
	}

	// The sample that was already sent as the latest one is not repeated.
	src.publish(snapAt(1))
	src.publish(snapAt(2))
	src.publish(snapAt(3))
	for _, want := range []int{2, 3} {
		got := r.nextSnapshot()
		if !got.Timestamp.Equal(snapAt(want).Timestamp) {
			t.Fatalf("snapshot timestamp = %v, want %v", got.Timestamp, snapAt(want).Timestamp)
		}
		if string(mustJSON(t, got.Collectors["uptime"])) != `{"seconds":42}` {
			t.Errorf("uptime section = %v", got.Collectors["uptime"])
		}
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestStreamBeforeFirstSample(t *testing.T) {
	src := &fakeSource{subscribed: make(chan struct{}, 1)}
	ts := httptest.NewServer(New(":0", "dev", discardLogger(), src).Handler())
	t.Cleanup(ts.Close) // runs after openStream's cleanup closes the stream

	// Headers arrive straight away even with nothing to send yet.
	resp := openStream(t, context.Background(), ts.URL)
	<-src.subscribed

	src.publish(snapAt(5))
	if got := newSSEReader(t, resp.Body).nextSnapshot(); !got.Timestamp.Equal(snapAt(5).Timestamp) {
		t.Fatalf("snapshot timestamp = %v, want %v", got.Timestamp, snapAt(5).Timestamp)
	}
}

func TestStreamHeartbeat(t *testing.T) {
	src := &fakeSource{}
	s := New(":0", "dev", discardLogger(), src)
	s.heartbeatInterval = 10 * time.Millisecond
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close) // runs after openStream's cleanup closes the stream

	resp := openStream(t, context.Background(), ts.URL)
	r := newSSEReader(t, resp.Body)
	for range 2 {
		ev, err := r.next()
		if err != nil {
			t.Fatalf("read SSE event: %v", err)
		}
		if ev.comment != "heartbeat" || ev.event != "" {
			t.Fatalf("event = %+v, want a heartbeat comment", ev)
		}
	}
}

func TestStreamUnsubscribesWhenClientDisconnects(t *testing.T) {
	src := &fakeSource{snap: snapAt(1), ok: true, subscribed: make(chan struct{}, 1)}
	ts := httptest.NewServer(New(":0", "dev", discardLogger(), src).Handler())
	t.Cleanup(ts.Close) // runs after openStream's cleanup closes the stream

	ctx, cancel := context.WithCancel(context.Background())
	resp := openStream(t, ctx, ts.URL)
	<-src.subscribed
	newSSEReader(t, resp.Body).nextSnapshot()

	cancel()
	waitFor(t, "the stream to unsubscribe", func() bool { return src.subscribers() == 0 })
}

func TestStreamHandlerReturnsOnCancelledContext(t *testing.T) {
	src := &fakeSource{snap: snapAt(1), ok: true, subscribed: make(chan struct{}, 1)}
	s := New(":0", "dev", discardLogger(), src)
	before := runtime.NumGoroutine()

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/stream", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Handler().ServeHTTP(rec, req)
	}()
	<-src.subscribed

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not return after the request context was cancelled")
	}
	if n := src.subscribers(); n != 0 {
		t.Errorf("subscribers = %d after disconnect, want 0", n)
	}
	waitFor(t, "goroutines to exit", func() bool { return runtime.NumGoroutine() <= before })

	if !strings.HasPrefix(rec.Body.String(), "event: snapshot\ndata: {") {
		t.Errorf("body = %q, want it to start with a snapshot event", rec.Body)
	}
}

func TestStreamEndsWhenSourceStops(t *testing.T) {
	src := &fakeSource{snap: snapAt(1), ok: true, subscribed: make(chan struct{}, 1)}
	ts := httptest.NewServer(New(":0", "dev", discardLogger(), src).Handler())
	t.Cleanup(ts.Close) // runs after openStream's cleanup closes the stream

	resp := openStream(t, context.Background(), ts.URL)
	<-src.subscribed
	r := newSSEReader(t, resp.Body)
	r.nextSnapshot()

	src.stop()
	if _, err := r.next(); !errors.Is(err, io.EOF) {
		t.Fatalf("read after source stopped: err = %v, want EOF", err)
	}
}

// serve runs s on a local listener and returns its base URL and a function
// that cancels Serve's context and returns its result. The test cleanup stops
// the server too if the test did not.
func serve(t *testing.T, s *Server) (string, func() error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()

	var once sync.Once
	var serveErr error
	stop := func() error {
		once.Do(func() {
			cancel()
			serveErr = <-done
		})
		return serveErr
	}
	t.Cleanup(func() { _ = stop() })
	return "http://" + ln.Addr().String(), stop
}

func TestStreamOutlivesServerWriteTimeout(t *testing.T) {
	src := &fakeSource{snap: snapAt(1), ok: true, subscribed: make(chan struct{}, 1)}
	s := New(":0", "dev", discardLogger(), src)
	s.writeTimeout = 50 * time.Millisecond
	url, _ := serve(t, s)

	resp := openStream(t, context.Background(), url)
	<-src.subscribed
	r := newSSEReader(t, resp.Body)
	r.nextSnapshot()

	time.Sleep(4 * s.writeTimeout)
	src.publish(snapAt(2))
	if got := r.nextSnapshot(); !got.Timestamp.Equal(snapAt(2).Timestamp) {
		t.Fatalf("snapshot timestamp = %v, want %v", got.Timestamp, snapAt(2).Timestamp)
	}
}

func TestShutdownClosesStreams(t *testing.T) {
	src := &fakeSource{snap: snapAt(1), ok: true, subscribed: make(chan struct{}, 1)}
	url, stop := serve(t, New(":0", "dev", discardLogger(), src))

	resp := openStream(t, context.Background(), url)
	<-src.subscribed
	r := newSSEReader(t, resp.Body)
	r.nextSnapshot()

	start := time.Now()
	if err := stop(); err != nil {
		t.Fatalf("Serve() error = %v, want nil", err)
	}
	// Shutdown waits up to shutdownTimeout for handlers, so a stream that
	// ignores shutdown would show up here as a multi-second delay.
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("shutdown took %v with an open stream, want well under a second", elapsed)
	}
	if _, err := r.next(); !errors.Is(err, io.EOF) {
		t.Fatalf("read after shutdown: err = %v, want EOF", err)
	}
	if n := src.subscribers(); n != 0 {
		t.Errorf("subscribers = %d after shutdown, want 0", n)
	}
}
