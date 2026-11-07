// Package notify pushes alert notifications to a generic webhook and to ntfy.
//
// A Dispatcher reads the alert transitions from the evaluator and turns the
// ones that matter, firing and resolved, into Events. Each Notifier gets its
// own queue and worker, so a slow or unreachable endpoint never holds up the
// evaluator or the other notifiers. Failed deliveries are retried with
// exponential backoff.
//
// A per-alert cooldown keeps a flapping alert from flooding the endpoints:
// an alert sends at most one firing notification per cooldown. A firing that
// is held back is sent once the cooldown ends if the alert is still firing,
// and a resolved notification is only sent for an alert whose firing was sent.
package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/alerts"
)

// Delivery limits.
const (
	// QueueSize is how many events a notifier can fall behind before the
	// oldest queued one is dropped.
	QueueSize = 64
	// RequestTimeout limits one delivery attempt.
	RequestTimeout = 10 * time.Second
)

// Event is one notification: an alert that started firing or resolved.
type Event struct {
	// Alert is the name of the rule.
	Alert string `json:"alert"`
	// Series is the series key the alert is for, which tells the alerts of
	// a glob rule such as "fs.*.used_pct" apart.
	Series    string          `json:"series"`
	Severity  alerts.Severity `json:"severity"`
	Op        alerts.Op       `json:"op"`
	Value     float64         `json:"value"`
	Threshold float64         `json:"threshold"`
	// State is alerts.StateFiring or alerts.StateResolved.
	State    alerts.State `json:"state"`
	Hostname string       `json:"hostname"`
	// Time is when the alert fired or resolved.
	Time time.Time `json:"time"`
}

// Notifier delivers events to one endpoint.
type Notifier interface {
	// Name is a short label for logs, such as "webhook".
	Name() string
	// Target describes the endpoint for logs without secrets, such as
	// "https://ntfy.sh".
	Target() string
	// Send delivers one event. It returns a *StatusError for an HTTP error
	// response so the dispatcher can tell retryable failures apart.
	Send(ctx context.Context, e Event) error
}

// StatusError is an HTTP error response from an endpoint.
type StatusError struct {
	Code int
	// Body is the start of the response body, for the log.
	Body string
}

func (e *StatusError) Error() string {
	msg := fmt.Sprintf("HTTP %d %s", e.Code, http.StatusText(e.Code))
	if e.Body != "" {
		msg += ": " + e.Body
	}
	return msg
}

// temporary reports whether retrying might help: server errors, rate
// limiting and timeouts. Other client errors will fail the same way again.
func (e *StatusError) temporary() bool {
	return e.Code >= 500 || e.Code == http.StatusTooManyRequests || e.Code == http.StatusRequestTimeout
}

// Options configures a Dispatcher.
type Options struct {
	// Cooldown is the shortest time between two firing notifications for
	// the same alert. Zero turns the cooldown off.
	Cooldown time.Duration
	// Hostname goes into every event.
	Hostname string
	// Logger defaults to slog.Default().
	Logger *slog.Logger
}

// Dispatcher turns alert transitions into notifications.
type Dispatcher struct {
	notifiers []Notifier
	cooldown  time.Duration
	hostname  string
	logger    *slog.Logger

	// Delivery tuning, changed by tests.
	attempts   int           // tries per event, including the first
	backoff    time.Duration // wait before the first retry, doubled each time
	maxBackoff time.Duration
	now        func() time.Time
}

// New creates a dispatcher for notifiers. With no notifiers Run returns
// straight away.
func New(notifiers []Notifier, opts Options) *Dispatcher {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	opts.Cooldown = max(opts.Cooldown, 0)
	return &Dispatcher{
		notifiers:  append([]Notifier(nil), notifiers...),
		cooldown:   opts.Cooldown,
		hostname:   opts.Hostname,
		logger:     opts.Logger,
		attempts:   5,
		backoff:    2 * time.Second,
		maxBackoff: time.Minute,
		now:        time.Now,
	}
}

// Enabled reports whether there is anything to notify.
func (d *Dispatcher) Enabled() bool {
	return len(d.notifiers) > 0
}

// Describe lists the notifiers and their endpoints without secrets, for the
// startup log, or "none".
func (d *Dispatcher) Describe() string {
	if len(d.notifiers) == 0 {
		return "none"
	}
	parts := make([]string, len(d.notifiers))
	for i, n := range d.notifiers {
		parts[i] = n.Name() + " " + n.Target()
	}
	return strings.Join(parts, ", ")
}

// alertKey identifies one alert: a rule for one series.
type alertKey struct {
	rule, series string
}

// tracked is what the dispatcher remembers about one alert.
type tracked struct {
	// last is the latest firing or resolved transition.
	last alerts.Transition
	// notified is the state last sent: firing, resolved or empty.
	notified alerts.State
	// firedAt is when the last firing notification was sent.
	firedAt time.Time
}

// held reports whether the alert fires but its notification is waiting
// for the cooldown.
func (t *tracked) held() bool {
	return t.last.To == alerts.StateFiring && t.notified != alerts.StateFiring
}

// Run sends notifications for the transitions from trans until ctx is done
// or trans is closed. When trans is closed the events already queued are
// still delivered before Run returns; when ctx is done they are dropped and
// requests in flight are cancelled. With no notifiers Run returns at once
// without reading trans.
func (d *Dispatcher) Run(ctx context.Context, trans <-chan alerts.Transition) {
	if len(d.notifiers) == 0 {
		return
	}

	queues := make([]chan Event, len(d.notifiers))
	var wg sync.WaitGroup
	for i, n := range d.notifiers {
		q := make(chan Event, QueueSize)
		queues[i] = q
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.work(ctx, n, q)
		}()
	}
	defer func() {
		for _, q := range queues {
			close(q)
		}
		wg.Wait()
	}()

	state := map[alertKey]*tracked{}
	send := func(t alerts.Transition) {
		e := d.event(t)
		for i, q := range queues {
			if dropped := enqueue(q, e); dropped {
				d.logger.Warn("notifier fell behind; dropped the oldest queued notification",
					"notifier", d.notifiers[i].Name())
			}
		}
	}

	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	for {
		// Wake up when the first held firing notification is due.
		var wake <-chan time.Time
		if due, ok := d.nextDue(state); ok {
			timer.Reset(max(due.Sub(d.now()), 0))
			wake = timer.C
		}

		select {
		case <-ctx.Done():
			return
		case t, ok := <-trans:
			if !ok {
				return
			}
			d.handle(state, t, send)
		case <-wake:
			d.release(state, send)
		}
		// Go 1.23 timers: Stop also discards a pending fire, so the next
		// Reset starts clean.
		timer.Stop()
	}
}

// handle applies one transition to the alert state and sends what is due.
func (d *Dispatcher) handle(state map[alertKey]*tracked, t alerts.Transition, send func(alerts.Transition)) {
	if t.To != alerts.StateFiring && t.To != alerts.StateResolved {
		return // pending and inactive are not worth a notification
	}
	k := alertKey{t.Alert.Rule, t.Alert.Series}
	s := state[k]
	if s == nil {
		s = &tracked{}
		state[k] = s
	}
	s.last = t

	now := d.now()
	switch t.To {
	case alerts.StateFiring:
		if s.notified == alerts.StateFiring {
			return
		}
		if !s.firedAt.IsZero() && now.Sub(s.firedAt) < d.cooldown {
			d.logger.Debug("alert notification held back by the cooldown",
				"rule", t.Alert.Rule, "series", t.Alert.Series, "until", s.firedAt.Add(d.cooldown))
			return
		}
		s.notified, s.firedAt = alerts.StateFiring, now
		send(t)
	case alerts.StateResolved:
		// A firing that was held back is simply forgotten, so the endpoint
		// never hears of a resolve without the firing before it.
		if s.notified == alerts.StateFiring {
			s.notified = alerts.StateResolved
			send(t)
		}
	}
	d.prune(state, now)
}

// release sends the held firing notifications whose cooldown has ended.
func (d *Dispatcher) release(state map[alertKey]*tracked, send func(alerts.Transition)) {
	now := d.now()
	for _, s := range state {
		if s.held() && now.Sub(s.firedAt) >= d.cooldown {
			s.notified, s.firedAt = alerts.StateFiring, now
			send(s.last)
		}
	}
	d.prune(state, now)
}

// nextDue returns when the first held firing notification may be sent.
func (d *Dispatcher) nextDue(state map[alertKey]*tracked) (time.Time, bool) {
	var due time.Time
	found := false
	for _, s := range state {
		if !s.held() {
			continue
		}
		at := s.firedAt.Add(d.cooldown)
		if !found || at.Before(due) {
			due, found = at, true
		}
	}
	return due, found
}

// prune forgets resolved alerts whose cooldown has ended, so the state
// does not grow with every series a glob rule ever matched.
func (d *Dispatcher) prune(state map[alertKey]*tracked, now time.Time) {
	for k, s := range state {
		if s.last.To == alerts.StateResolved && now.Sub(s.firedAt) >= d.cooldown {
			delete(state, k)
		}
	}
}

func (d *Dispatcher) event(t alerts.Transition) Event {
	return Event{
		Alert:     t.Alert.Rule,
		Series:    t.Alert.Series,
		Severity:  t.Alert.Severity,
		Op:        t.Alert.Op,
		Value:     t.Alert.Value,
		Threshold: t.Alert.Threshold,
		State:     t.To,
		Hostname:  d.hostname,
		Time:      t.At,
	}
}

// enqueue adds e to q without blocking, dropping the oldest queued event if
// q is full. It reports whether one was dropped.
func enqueue(q chan Event, e Event) (dropped bool) {
	for {
		select {
		case q <- e:
			return dropped
		default:
			// Full: drop the oldest. The worker may have taken it
			// meanwhile, which is fine too.
			select {
			case <-q:
				dropped = true
			default:
			}
		}
	}
}

// work delivers the events from q one at a time, in order.
func (d *Dispatcher) work(ctx context.Context, n Notifier, q <-chan Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-q:
			if !ok {
				return
			}
			d.deliver(ctx, n, e)
		}
	}
}

// deliver sends e, retrying temporary failures with exponential backoff.
func (d *Dispatcher) deliver(ctx context.Context, n Notifier, e Event) {
	attrs := []any{"notifier", n.Name(), "rule", e.Alert, "series", e.Series, "state", e.State}
	wait := d.backoff
	for attempt := 1; ; attempt++ {
		err := n.Send(ctx, e)
		if err == nil {
			d.logger.Info("alert notification sent", attrs...)
			return
		}
		if ctx.Err() != nil {
			return // shutting down
		}
		err = redact(err)
		var se *StatusError
		permanent := errors.As(err, &se) && !se.temporary()
		if permanent || attempt >= d.attempts {
			d.logger.Error("alert notification failed",
				append(attrs, "attempts", attempt, "err", err)...)
			return
		}
		d.logger.Warn("alert notification failed; retrying",
			append(attrs, "attempt", attempt, "retry_in", wait.String(), "err", err)...)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, d.maxBackoff)
	}
}

// redact drops the URL from a *url.Error, since webhook URLs and ntfy
// topics often carry secrets. The notifier name is logged instead.
func redact(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %w", strings.ToLower(ue.Op), ue.Err)
	}
	return err
}

// target returns the scheme and host of rawURL: enough to tell where
// notifications go without the path, query or user info that may hold a
// token or a secret topic name.
func target(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return "(invalid URL)"
	}
	return u.Scheme + "://" + u.Host
}
