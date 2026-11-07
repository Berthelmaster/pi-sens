package alerts

import (
	"cmp"
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

const (
	// ResolvedHistory is how many resolved alerts the evaluator remembers.
	ResolvedHistory = 50
	// StaleSamples is after how many snapshots in a row without its series
	// an alert is dropped: a pending one goes back to inactive and a firing
	// one resolves. A single failed collection does not end an alert.
	StaleSamples = 3
	// TransitionBuffer is how many transitions a subscriber can fall behind
	// before the oldest unread one is dropped.
	TransitionBuffer = 64
)

// State is where an alert is in its life cycle.
type State string

// Alert states. Inactive only shows up in transitions: an alert that is not
// pending or firing is not tracked.
const (
	StateInactive State = "inactive"
	StatePending  State = "pending"
	StateFiring   State = "firing"
	StateResolved State = "resolved"
)

// Alert is the state of one rule for one matching series.
type Alert struct {
	Rule      string        `json:"rule"`
	Series    string        `json:"series"`
	Severity  Severity      `json:"severity"`
	Op        Op            `json:"op"`
	Threshold float64       `json:"threshold"`
	For       time.Duration `json:"-"` // encoded as for_s
	State     State         `json:"state"`
	// Value is the series value at the last state change.
	Value float64 `json:"value"`
	// PendingAt is when the threshold was first crossed.
	PendingAt time.Time `json:"pending_at"`
	// FiringAt is when the alert fired, nil while pending.
	FiringAt *time.Time `json:"firing_at,omitempty"`
	// ResolvedAt is when a firing alert resolved, nil until then.
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
}

// MarshalJSON adds the For duration as for_s in seconds, following the
// unit-suffix convention of the series keys.
func (a Alert) MarshalJSON() ([]byte, error) {
	type plain Alert // no methods, so no recursion
	return json.Marshal(struct {
		plain
		ForS float64 `json:"for_s"`
	}{plain(a), a.For.Seconds()})
}

// Transition is one state change of an alert, for notifiers.
type Transition struct {
	From State `json:"from"`
	To   State `json:"to"`
	// At is the timestamp of the snapshot that caused the change.
	At time.Time `json:"at"`
	// Alert is the alert after the change.
	Alert Alert `json:"alert"`
}

// Set is what the API shows: the firing alerts, most severe first, and the
// last ResolvedHistory resolved ones, newest first. Sets are shared, so
// treat them as read-only.
type Set struct {
	// Version goes up by one every time the set changes.
	Version  uint64  `json:"version"`
	Firing   []Alert `json:"firing"`
	Resolved []Alert `json:"resolved"`
}

type instanceKey struct {
	rule   int // index into Evaluator.rules
	series string
}

type instance struct {
	alert   Alert
	last    float64 // latest value seen
	missing int     // snapshots in a row without the series
}

// Evaluator checks rules against snapshots and tracks alert states. All
// times come from the snapshots, so it can be driven by hand in tests.
type Evaluator struct {
	rules  []Rule
	logger *slog.Logger

	mu       sync.Mutex
	active   map[instanceKey]*instance
	resolved []Alert // newest first
	set      Set

	sets        *hub[Set]
	transitions *hub[Transition]
}

// NewEvaluator creates an evaluator for rules, which should be valid (see
// ParseRules). A nil logger means slog.Default().
func NewEvaluator(rules []Rule, logger *slog.Logger) *Evaluator {
	if logger == nil {
		logger = slog.Default()
	}
	return &Evaluator{
		rules:       slices.Clone(rules),
		logger:      logger,
		active:      map[instanceKey]*instance{},
		set:         Set{Firing: []Alert{}, Resolved: []Alert{}},
		sets:        newHub[Set](1),
		transitions: newHub[Transition](TransitionBuffer),
	}
}

// Rules returns the rules the evaluator checks.
func (e *Evaluator) Rules() []Rule {
	return slices.Clone(e.rules)
}

// Run evaluates every snapshot from snaps until ctx is done or snaps is
// closed, then closes all subscriber channels. Subscribe to the sampler
// before starting it so the first sample is not missed.
func (e *Evaluator) Run(ctx context.Context, snaps <-chan metrics.Snapshot) {
	defer e.close()
	for {
		select {
		case <-ctx.Done():
			return
		case snap, ok := <-snaps:
			if !ok {
				return
			}
			e.Evaluate(snap)
		}
	}
}

// Alerts returns the current set of firing and resolved alerts.
func (e *Evaluator) Alerts() Set {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.set
}

// Subscribe returns a channel that receives the new Set every time it
// changes, and a function that unsubscribes and closes the channel. Only the
// latest set is kept for a slow reader. The channel is closed when Run
// returns.
func (e *Evaluator) Subscribe() (<-chan Set, func()) {
	return e.sets.subscribe()
}

// SubscribeTransitions returns a channel that receives every alert state
// change, and a function that unsubscribes and closes the channel. A reader
// that falls more than TransitionBuffer transitions behind loses the oldest
// ones. The channel is closed when Run returns.
func (e *Evaluator) SubscribeTransitions() (<-chan Transition, func()) {
	return e.transitions.subscribe()
}

func (e *Evaluator) close() {
	e.sets.close()
	e.transitions.close()
}

// Evaluate checks every rule against snap, updates the alert states and
// publishes the transitions and, if it changed, the new set.
func (e *Evaluator) Evaluate(snap metrics.Snapshot) {
	e.mu.Lock()
	defer e.mu.Unlock()

	ts := snap.Timestamp
	var trans []Transition
	seen := map[instanceKey]bool{}
	for i, r := range e.rules {
		for _, m := range matching(r, snap.Series) {
			k := instanceKey{rule: i, series: m.key}
			seen[k] = true
			if t, ok := e.step(k, r, m.value, ts); ok {
				trans = append(trans, t)
			}
		}
	}

	var stale []instanceKey
	for k, inst := range e.active {
		if seen[k] {
			continue
		}
		inst.missing++
		if inst.missing >= StaleSamples {
			stale = append(stale, k)
		}
	}
	slices.SortFunc(stale, func(a, b instanceKey) int {
		return cmp.Or(cmp.Compare(a.rule, b.rule), cmp.Compare(a.series, b.series))
	})
	for _, k := range stale {
		inst := e.active[k]
		trans = append(trans, e.end(k, inst.last, ts))
	}

	if len(trans) == 0 {
		return
	}
	changed := false
	for _, t := range trans {
		e.logTransition(t)
		if t.From == StateFiring || t.To == StateFiring {
			changed = true
		}
		if n := e.transitions.publish(t); n > 0 {
			e.logger.Warn("alert transition subscriber fell behind; dropped the oldest transitions", "dropped", n)
		}
	}
	if changed {
		e.set = e.buildSet()
		e.sets.publish(e.set)
	}
}

type match struct {
	key   string
	value float64
}

// matching returns the series the rule applies to, sorted by key so
// transitions come out in a stable order.
func matching(r Rule, series map[string]float64) []match {
	if !r.isGlob() {
		if v, ok := series[r.Series]; ok {
			return []match{{r.Series, v}}
		}
		return nil
	}
	var out []match
	for k, v := range series {
		if r.matches(k) {
			out = append(out, match{k, v})
		}
	}
	slices.SortFunc(out, func(a, b match) int { return cmp.Compare(a.key, b.key) })
	return out
}

// step applies one sample of one series to its alert and returns the
// transition it caused, if any.
func (e *Evaluator) step(k instanceKey, r Rule, v float64, ts time.Time) (Transition, bool) {
	inst := e.active[k]
	if !r.breached(v) {
		if inst == nil {
			return Transition{}, false
		}
		return e.end(k, v, ts), true
	}

	if inst == nil {
		inst = &instance{alert: Alert{
			Rule:      r.Name,
			Series:    k.series,
			Severity:  r.Severity,
			Op:        r.Op,
			Threshold: r.Threshold,
			For:       r.For,
			State:     StateInactive,
			PendingAt: ts,
		}}
		e.active[k] = inst
	}
	inst.last, inst.missing = v, 0

	a := &inst.alert
	from, to := a.State, StatePending
	if ts.Sub(a.PendingAt) >= r.For {
		to = StateFiring
	}
	if from == StateFiring || from == to {
		return Transition{}, false
	}
	a.State, a.Value = to, v
	if to == StateFiring {
		a.FiringAt = &ts
	}
	return Transition{From: from, To: to, At: ts, Alert: *a}, true
}

// end stops tracking an alert: a firing one resolves, a pending one goes
// back to inactive.
func (e *Evaluator) end(k instanceKey, v float64, ts time.Time) Transition {
	a := e.active[k].alert
	delete(e.active, k)

	from := a.State
	a.Value = v
	if from == StateFiring {
		a.State, a.ResolvedAt = StateResolved, &ts
		e.resolved = slices.Insert(e.resolved, 0, a)
		if len(e.resolved) > ResolvedHistory {
			e.resolved = e.resolved[:ResolvedHistory]
		}
	} else {
		a.State = StateInactive
	}
	return Transition{From: from, To: a.State, At: ts, Alert: a}
}

// buildSet makes a fresh Set, so sets already handed out stay unchanged.
func (e *Evaluator) buildSet() Set {
	firing := []Alert{}
	for _, inst := range e.active {
		if inst.alert.State == StateFiring {
			firing = append(firing, inst.alert)
		}
	}
	slices.SortFunc(firing, func(a, b Alert) int {
		return cmp.Or(
			cmp.Compare(severityRank(b.Severity), severityRank(a.Severity)),
			a.FiringAt.Compare(*b.FiringAt),
			cmp.Compare(a.Rule, b.Rule),
			cmp.Compare(a.Series, b.Series),
		)
	})
	return Set{
		Version:  e.set.Version + 1,
		Firing:   firing,
		Resolved: append([]Alert{}, e.resolved...),
	}
}

func severityRank(s Severity) int {
	if s == SeverityCritical {
		return 1
	}
	return 0
}

func (e *Evaluator) logTransition(t Transition) {
	attrs := []any{
		"rule", t.Alert.Rule,
		"series", t.Alert.Series,
		"severity", t.Alert.Severity,
		"value", t.Alert.Value,
	}
	switch t.To {
	case StateFiring:
		e.logger.Warn("alert firing", attrs...)
	case StateResolved:
		e.logger.Info("alert resolved", attrs...)
	default:
		e.logger.Debug("alert "+string(t.To), attrs...)
	}
}
