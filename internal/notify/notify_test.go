package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/alerts"
)

var t0 = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// request is what the test server saw.
type request struct {
	Method string
	Path   string
	Header http.Header
	Body   string
}

// recorder is an httptest server that records requests and answers with
// the status codes in replies, in order, then 200.
type recorder struct {
	*httptest.Server

	mu       sync.Mutex
	requests []request
	replies  []int
	got      chan struct{} // gets a value per request
}

func newRecorder(t *testing.T, replies ...int) *recorder {
	t.Helper()
	r := &recorder{replies: replies, got: make(chan struct{}, 100)}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.requests = append(r.requests, request{req.Method, req.URL.Path, req.Header.Clone(), string(body)})
		code := http.StatusOK
		if len(r.replies) > 0 {
			code, r.replies = r.replies[0], r.replies[1:]
		}
		r.mu.Unlock()
		w.WriteHeader(code)
		r.got <- struct{}{}
	}))
	t.Cleanup(r.Close)
	return r
}

func (r *recorder) all() []request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]request(nil), r.requests...)
}

// waitFor waits until the server has seen n requests in total.
func (r *recorder) waitFor(t *testing.T, n int) []request {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for len(r.all()) < n {
		select {
		case <-r.got:
		case <-deadline:
			t.Fatalf("got %d requests, want %d", len(r.all()), n)
		}
	}
	return r.all()
}

// newTestDispatcher returns a dispatcher with fast retries.
func newTestDispatcher(cooldown time.Duration, notifiers ...Notifier) *Dispatcher {
	d := New(notifiers, Options{Cooldown: cooldown, Hostname: "pi5", Logger: discardLogger()})
	d.backoff, d.maxBackoff = time.Millisecond, 4*time.Millisecond
	return d
}

// run starts d on a fresh channel and returns the channel and a function
// that closes it and waits until every queued event is delivered.
func run(t *testing.T, d *Dispatcher) (chan<- alerts.Transition, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan alerts.Transition)
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.Run(ctx, ch)
	}()
	var once sync.Once
	finish := func() {
		once.Do(func() {
			close(ch)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("Run did not return after the channel closed")
			}
			cancel()
		})
	}
	t.Cleanup(finish)
	return ch, finish
}

var hot = alerts.Alert{
	Rule: "cpu-temp-high", Series: "temp.cpu_c", Severity: alerts.SeverityWarning,
	Op: alerts.OpAbove, Threshold: 75, For: time.Minute,
}

func transition(a alerts.Alert, from, to alerts.State, at time.Duration, value float64) alerts.Transition {
	a.State, a.Value = to, value
	return alerts.Transition{From: from, To: to, At: t0.Add(at), Alert: a}
}

func firing(a alerts.Alert, at time.Duration, value float64) alerts.Transition {
	return transition(a, alerts.StatePending, alerts.StateFiring, at, value)
}

func resolved(a alerts.Alert, at time.Duration, value float64) alerts.Transition {
	return transition(a, alerts.StateFiring, alerts.StateResolved, at, value)
}

func TestWebhookPayload(t *testing.T) {
	srv := newRecorder(t)
	ch, finish := run(t, newTestDispatcher(time.Hour, NewWebhook(srv.URL+"/hook")))
	ch <- firing(hot, time.Minute, 78.5)
	ch <- resolved(hot, 5*time.Minute, 70)
	finish()

	reqs := srv.all()
	if len(reqs) != 2 {
		t.Fatalf("got %d requests, want 2", len(reqs))
	}
	want := []string{
		`{"alert":"cpu-temp-high","series":"temp.cpu_c","severity":"warning","op":">","value":78.5,"threshold":75,"state":"firing","hostname":"pi5","time":"2026-11-07T12:01:00Z"}`,
		`{"alert":"cpu-temp-high","series":"temp.cpu_c","severity":"warning","op":">","value":70,"threshold":75,"state":"resolved","hostname":"pi5","time":"2026-11-07T12:05:00Z"}`,
	}
	for i, r := range reqs {
		if r.Method != http.MethodPost || r.Path != "/hook" {
			t.Errorf("request %d = %s %s, want POST /hook", i, r.Method, r.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("request %d Content-Type = %q, want application/json", i, ct)
		}
		if ua := r.Header.Get("User-Agent"); ua != "pisens" {
			t.Errorf("request %d User-Agent = %q, want pisens", i, ua)
		}
		if r.Body != want[i] {
			t.Errorf("request %d body =\n%s\nwant\n%s", i, r.Body, want[i])
		}
	}
}

func TestNtfyMessages(t *testing.T) {
	crit := alerts.Alert{
		Rule: "undervoltage", Series: "pi.undervoltage", Severity: alerts.SeverityCritical,
		Op: alerts.OpAbove, Threshold: 0,
	}
	low := alerts.Alert{
		Rule: "core-voltage-low", Series: "pi.core_v", Severity: alerts.SeverityWarning,
		Op: alerts.OpBelow, Threshold: 0.8,
	}
	tests := []struct {
		name     string
		t        alerts.Transition
		title    string
		priority string
		tags     string
		body     string
	}{
		{
			name:  "warning firing",
			t:     firing(hot, 0, 78.5),
			title: "pi5: cpu-temp-high firing", priority: "4", tags: "warning,warning",
			body: "temp.cpu_c is 78.5, above the threshold of 75.",
		},
		{
			name:  "critical firing",
			t:     transition(crit, alerts.StateInactive, alerts.StateFiring, 0, 1),
			title: "pi5: undervoltage firing", priority: "5", tags: "rotating_light,critical",
			body: "pi.undervoltage is 1, above the threshold of 0.",
		},
		{
			name:  "below firing",
			t:     firing(low, 0, 0.72),
			title: "pi5: core-voltage-low firing", priority: "4", tags: "warning,warning",
			body: "pi.core_v is 0.72, below the threshold of 0.8.",
		},
		{
			name:  "critical resolved",
			t:     resolved(crit, 0, 0),
			title: "pi5: undervoltage resolved", priority: "3", tags: "white_check_mark,critical",
			body: "pi.undervoltage is back to 0; the alert fires above 0.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newRecorder(t)
			d := newTestDispatcher(time.Hour, NewNtfy(srv.URL+"/pi-alerts"))
			ch, finish := run(t, d)
			if tt.t.To == alerts.StateResolved {
				// A resolve is only sent after its firing.
				ch <- transition(tt.t.Alert, alerts.StatePending, alerts.StateFiring, 0, 1)
			}
			ch <- tt.t
			finish()

			reqs := srv.all()
			if len(reqs) == 0 {
				t.Fatal("no request")
			}
			r := reqs[len(reqs)-1]
			if r.Method != http.MethodPost || r.Path != "/pi-alerts" {
				t.Errorf("request = %s %s, want POST /pi-alerts", r.Method, r.Path)
			}
			if got := r.Header.Get("Title"); got != tt.title {
				t.Errorf("Title = %q, want %q", got, tt.title)
			}
			if got := r.Header.Get("Priority"); got != tt.priority {
				t.Errorf("Priority = %q, want %q", got, tt.priority)
			}
			if got := r.Header.Get("Tags"); got != tt.tags {
				t.Errorf("Tags = %q, want %q", got, tt.tags)
			}
			if r.Body != tt.body {
				t.Errorf("body = %q, want %q", r.Body, tt.body)
			}
		})
	}
}

func TestNtfyEncodesNonASCIITitle(t *testing.T) {
	srv := newRecorder(t)
	ch, finish := run(t, newTestDispatcher(time.Hour, NewNtfy(srv.URL+"/t")))
	a := hot
	a.Rule = "température"
	ch <- firing(a, 0, 80)
	finish()

	reqs := srv.all()
	if len(reqs) != 1 {
		t.Fatalf("got %d requests, want 1", len(reqs))
	}
	got := reqs[0].Header.Get("Title")
	if want := "=?utf-8?q?pi5:_temp=C3=A9rature_firing?="; got != want {
		t.Errorf("Title = %q, want %q", got, want)
	}
}

// states returns the state field of every webhook request.
func states(t *testing.T, reqs []request) []string {
	t.Helper()
	out := make([]string, len(reqs))
	for i, r := range reqs {
		var e Event
		if err := json.Unmarshal([]byte(r.Body), &e); err != nil {
			t.Fatalf("decode %s: %v", r.Body, err)
		}
		out[i] = e.Series + " " + string(e.State)
	}
	return out
}

func TestCooldownHoldsBackFlapping(t *testing.T) {
	srv := newRecorder(t)
	ch, finish := run(t, newTestDispatcher(time.Hour, NewWebhook(srv.URL)))
	other := hot
	other.Series = "temp.gpu_c"

	ch <- firing(hot, 0, 80)
	ch <- resolved(hot, time.Minute, 70)
	// Within the cooldown: neither the firing nor its resolve is sent.
	ch <- firing(hot, 2*time.Minute, 80)
	ch <- resolved(hot, 3*time.Minute, 70)
	ch <- firing(hot, 4*time.Minute, 80)
	// The cooldown is per alert, so another series of the same rule is sent.
	ch <- firing(other, 4*time.Minute, 80)
	// Pending and inactive transitions are never sent.
	ch <- transition(other, alerts.StateInactive, alerts.StatePending, 5*time.Minute, 76)
	finish()

	got := states(t, srv.all())
	want := []string{"temp.cpu_c firing", "temp.cpu_c resolved", "temp.gpu_c firing"}
	if strings.Join(got, "; ") != strings.Join(want, "; ") {
		t.Errorf("notifications = %q, want %q", got, want)
	}
}

func TestCooldownSendsHeldFiringWhenItEnds(t *testing.T) {
	srv := newRecorder(t)
	cooldown := 200 * time.Millisecond
	ch, _ := run(t, newTestDispatcher(cooldown, NewWebhook(srv.URL)))

	start := time.Now()
	ch <- firing(hot, 0, 80)
	ch <- resolved(hot, time.Minute, 70)
	ch <- firing(hot, 2*time.Minute, 81)
	srv.waitFor(t, 2)

	// Still firing when the cooldown ends, so the endpoint hears about it.
	reqs := srv.waitFor(t, 3)
	if elapsed := time.Since(start); elapsed < cooldown {
		t.Errorf("held firing sent after %s, want at least the %s cooldown", elapsed, cooldown)
	}
	got := states(t, reqs)
	want := []string{"temp.cpu_c firing", "temp.cpu_c resolved", "temp.cpu_c firing"}
	if strings.Join(got, "; ") != strings.Join(want, "; ") {
		t.Errorf("notifications = %q, want %q", got, want)
	}
	var e Event
	if err := json.Unmarshal([]byte(reqs[2].Body), &e); err != nil {
		t.Fatal(err)
	}
	if e.Value != 81 || !e.Time.Equal(t0.Add(2*time.Minute)) {
		t.Errorf("held firing = value %v at %s, want the second firing's 81 at %s", e.Value, e.Time, t0.Add(2*time.Minute))
	}

	// Nothing else is pending.
	time.Sleep(2 * cooldown)
	if n := len(srv.all()); n != 3 {
		t.Errorf("got %d requests, want 3", n)
	}
}

func TestCooldownEndedSendsAgain(t *testing.T) {
	srv := newRecorder(t)
	cooldown := 50 * time.Millisecond
	ch, finish := run(t, newTestDispatcher(cooldown, NewWebhook(srv.URL)))

	ch <- firing(hot, 0, 80)
	ch <- resolved(hot, time.Minute, 70)
	srv.waitFor(t, 2)
	time.Sleep(cooldown + 20*time.Millisecond)
	ch <- firing(hot, 2*time.Minute, 80)
	finish()

	if n := len(srv.all()); n != 3 {
		t.Errorf("got %d requests, want 3", n)
	}
}

func TestNoCooldown(t *testing.T) {
	srv := newRecorder(t)
	ch, finish := run(t, newTestDispatcher(0, NewWebhook(srv.URL)))
	for i := range 3 {
		ch <- firing(hot, time.Duration(2*i)*time.Minute, 80)
		ch <- resolved(hot, time.Duration(2*i+1)*time.Minute, 70)
	}
	finish()

	if n := len(srv.all()); n != 6 {
		t.Errorf("got %d requests, want 6", n)
	}
}

func TestRetry(t *testing.T) {
	tests := []struct {
		name    string
		replies []int
		want    int // requests
	}{
		{name: "succeeds after server errors", replies: []int{503, 500}, want: 3},
		{name: "retries rate limiting", replies: []int{429}, want: 2},
		{name: "gives up after the last attempt", replies: []int{502, 502, 502, 502, 502, 502}, want: 5},
		{name: "client error is not retried", replies: []int{400}, want: 1},
		{name: "auth error is not retried", replies: []int{403}, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newRecorder(t, tt.replies...)
			ch, finish := run(t, newTestDispatcher(time.Hour, NewWebhook(srv.URL)))
			ch <- firing(hot, 0, 80)
			finish()

			reqs := srv.all()
			if len(reqs) != tt.want {
				t.Fatalf("got %d requests, want %d", len(reqs), tt.want)
			}
			for i, r := range reqs[1:] {
				if r.Body != reqs[0].Body {
					t.Errorf("retry %d body = %s, want %s", i+1, r.Body, reqs[0].Body)
				}
			}
		})
	}
}

func TestRetryBacksOff(t *testing.T) {
	srv := newRecorder(t, 503, 503, 503)
	d := newTestDispatcher(time.Hour, NewWebhook(srv.URL))
	d.backoff, d.maxBackoff = 20*time.Millisecond, 40*time.Millisecond
	ch, finish := run(t, d)

	start := time.Now()
	ch <- firing(hot, 0, 80)
	finish()

	if n := len(srv.all()); n != 4 {
		t.Fatalf("got %d requests, want 4", n)
	}
	// 20ms, then 40ms, then 40ms again because of the cap.
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Errorf("retries took %s, want at least 100ms of backoff", elapsed)
	}
}

// TestSlowEndpointDoesNotBlock checks that the dispatcher keeps reading
// transitions while an endpoint hangs, and that the other notifier still
// gets its notifications.
func TestSlowEndpointDoesNotBlock(t *testing.T) {
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(slow.Close)
	t.Cleanup(func() { close(release) })
	fast := newRecorder(t)

	ctx, cancel := context.WithCancel(context.Background())
	d := newTestDispatcher(time.Hour, NewWebhook(slow.URL), NewNtfy(fast.URL))
	ch := make(chan alerts.Transition) // unbuffered: every send waits for Run
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.Run(ctx, ch)
	}()

	n := 2 * QueueSize
	sent := make(chan struct{})
	go func() {
		defer close(sent)
		for i := range n {
			a := hot
			a.Series = fmt.Sprintf("temp.s%d_c", i)
			ch <- firing(a, 0, 80)
		}
	}()
	select {
	case <-sent:
	case <-time.After(5 * time.Second):
		t.Fatal("the dispatcher stopped reading transitions while an endpoint hung")
	}
	fast.waitFor(t, 1)

	// Shutting down cancels the hung request.
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the context was cancelled")
	}
}

func TestNoNotifiers(t *testing.T) {
	d := New(nil, Options{})
	if d.Enabled() {
		t.Error("Enabled() = true with no notifiers")
	}
	if got := d.Describe(); got != "none" {
		t.Errorf("Describe() = %q, want none", got)
	}
	// Run returns at once and never reads the channel.
	ch := make(chan alerts.Transition)
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.Run(context.Background(), ch)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run with no notifiers did not return")
	}
}

func TestSecretsStayOutOfLogs(t *testing.T) {
	const secret = "s3cr3t-topic"
	d := New([]Notifier{
		NewWebhook("https://user:" + secret + "@hooks.example.com:8443/" + secret + "?token=" + secret),
		NewNtfy("https://ntfy.sh/" + secret),
	}, Options{})
	got := d.Describe()
	if want := "webhook https://hooks.example.com:8443, ntfy https://ntfy.sh"; got != want {
		t.Errorf("Describe() = %q, want %q", got, want)
	}

	// A connection error from Go's HTTP client names the URL; the log
	// must not.
	srv := httptest.NewServer(http.NotFoundHandler())
	dead := srv.URL + "/" + secret
	srv.Close()
	var logs bytes.Buffer
	d = New([]Notifier{NewWebhook(dead)}, Options{Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	d.attempts, d.backoff = 2, time.Millisecond
	ch, finish := run(t, d)
	ch <- firing(hot, 0, 80)
	finish()

	if !strings.Contains(logs.String(), "alert notification failed") {
		t.Fatalf("no failure logged:\n%s", logs.String())
	}
	if strings.Contains(logs.String(), secret) {
		t.Errorf("logs contain the secret:\n%s", logs.String())
	}
}
