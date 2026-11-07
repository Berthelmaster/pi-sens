package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

var t0 = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func snapAt(at time.Duration, series map[string]float64) metrics.Snapshot {
	return metrics.Snapshot{Timestamp: t0.Add(at), Series: series}
}

// step is one snapshot fed to the evaluator and the transitions it should
// cause, written "rule series from>to".
type step struct {
	at     time.Duration
	series map[string]float64
	want   []string
}

func formatTransition(t Transition) string {
	return fmt.Sprintf("%s %s %s>%s", t.Alert.Rule, t.Alert.Series, t.From, t.To)
}

// drain returns the transitions that are waiting on ch.
func drain(ch <-chan Transition) []Transition {
	var out []Transition
	for {
		select {
		case t := <-ch:
			out = append(out, t)
		default:
			return out
		}
	}
}

func TestEvaluatorTransitions(t *testing.T) {
	hot := Rule{Name: "hot", Series: "temp.cpu_c", Op: OpAbove, Threshold: 75, For: 30 * time.Second, Severity: SeverityWarning}
	instant := Rule{Name: "uv", Series: "pi.undervoltage", Op: OpAbove, Threshold: 0, Severity: SeverityCritical}
	low := Rule{Name: "low", Series: "pi.core_v", Op: OpBelow, Threshold: 0.8, For: 4 * time.Second, Severity: SeverityWarning}
	disk := Rule{Name: "disk", Series: "fs.*.used_pct", Op: OpAbove, Threshold: 90, Severity: SeverityWarning}
	critical := Rule{Name: "crit", Series: "temp.cpu_c", Op: OpAbove, Threshold: 82, For: 10 * time.Second, Severity: SeverityCritical}

	temp := func(v float64) map[string]float64 { return map[string]float64{"temp.cpu_c": v} }
	none := map[string]float64{}

	tests := []struct {
		name  string
		rules []Rule
		steps []step
	}{
		{
			name:  "no for fires on the first sample and resolves when it clears",
			rules: []Rule{instant},
			steps: []step{
				{at: 0, series: map[string]float64{"pi.undervoltage": 0}},
				{at: 2 * time.Second, series: map[string]float64{"pi.undervoltage": 1}, want: []string{"uv pi.undervoltage inactive>firing"}},
				{at: 4 * time.Second, series: map[string]float64{"pi.undervoltage": 1}},
				{at: 6 * time.Second, series: map[string]float64{"pi.undervoltage": 0}, want: []string{"uv pi.undervoltage firing>resolved"}},
				{at: 8 * time.Second, series: map[string]float64{"pi.undervoltage": 0}},
			},
		},
		{
			name:  "pending until for has passed, then firing, then resolved",
			rules: []Rule{hot},
			steps: []step{
				{at: 0, series: temp(76), want: []string{"hot temp.cpu_c inactive>pending"}},
				{at: 10 * time.Second, series: temp(80)},
				{at: 29 * time.Second, series: temp(77)},
				{at: 30 * time.Second, series: temp(77), want: []string{"hot temp.cpu_c pending>firing"}},
				{at: 40 * time.Second, series: temp(90)},
				{at: 50 * time.Second, series: temp(70), want: []string{"hot temp.cpu_c firing>resolved"}},
			},
		},
		{
			name:  "clearing while pending resets the for timer",
			rules: []Rule{hot},
			steps: []step{
				{at: 0, series: temp(76), want: []string{"hot temp.cpu_c inactive>pending"}},
				{at: 20 * time.Second, series: temp(74), want: []string{"hot temp.cpu_c pending>inactive"}},
				{at: 25 * time.Second, series: temp(76), want: []string{"hot temp.cpu_c inactive>pending"}},
				{at: 50 * time.Second, series: temp(76)},
				{at: 55 * time.Second, series: temp(76), want: []string{"hot temp.cpu_c pending>firing"}},
			},
		},
		{
			name:  "a value equal to the threshold does not breach",
			rules: []Rule{hot, low},
			steps: []step{
				{at: 0, series: map[string]float64{"temp.cpu_c": 75, "pi.core_v": 0.8}},
				{at: time.Minute, series: map[string]float64{"temp.cpu_c": 75, "pi.core_v": 0.8}},
			},
		},
		{
			name:  "below threshold",
			rules: []Rule{low},
			steps: []step{
				{at: 0, series: map[string]float64{"pi.core_v": 0.85}},
				{at: 2 * time.Second, series: map[string]float64{"pi.core_v": 0.75}, want: []string{"low pi.core_v inactive>pending"}},
				{at: 6 * time.Second, series: map[string]float64{"pi.core_v": 0.7}, want: []string{"low pi.core_v pending>firing"}},
				{at: 8 * time.Second, series: map[string]float64{"pi.core_v": 0.9}, want: []string{"low pi.core_v firing>resolved"}},
			},
		},
		{
			name:  "glob tracks every matching series on its own",
			rules: []Rule{disk},
			steps: []step{
				{at: 0, series: map[string]float64{
					"fs.root.used_pct":      95,
					"fs.root.used_bytes":    1e12, // not matched by the glob
					"fs.mnt_nas.used_pct":   50,
					"mem.used_pct":          99,
					"fs.boot_firmware.used": 99,
				}, want: []string{"disk fs.root.used_pct inactive>firing"}},
				{at: 2 * time.Second, series: map[string]float64{"fs.root.used_pct": 95, "fs.mnt_nas.used_pct": 91},
					want: []string{"disk fs.mnt_nas.used_pct inactive>firing"}},
				{at: 4 * time.Second, series: map[string]float64{"fs.root.used_pct": 80, "fs.mnt_nas.used_pct": 91},
					want: []string{"disk fs.root.used_pct firing>resolved"}},
			},
		},
		{
			name:  "two rules on the same series",
			rules: []Rule{hot, critical},
			steps: []step{
				{at: 0, series: temp(85), want: []string{"hot temp.cpu_c inactive>pending", "crit temp.cpu_c inactive>pending"}},
				{at: 10 * time.Second, series: temp(85), want: []string{"crit temp.cpu_c pending>firing"}},
				{at: 20 * time.Second, series: temp(80), want: []string{"crit temp.cpu_c firing>resolved"}},
				{at: 30 * time.Second, series: temp(80), want: []string{"hot temp.cpu_c pending>firing"}},
			},
		},
		{
			name:  "a firing alert survives a few missing samples, then resolves",
			rules: []Rule{instant},
			steps: []step{
				{at: 0, series: map[string]float64{"pi.undervoltage": 1}, want: []string{"uv pi.undervoltage inactive>firing"}},
				{at: 2 * time.Second, series: none},
				{at: 4 * time.Second, series: none},
				{at: 6 * time.Second, series: map[string]float64{"pi.undervoltage": 1}},
				{at: 8 * time.Second, series: none},
				{at: 10 * time.Second, series: none},
				{at: 12 * time.Second, series: none, want: []string{"uv pi.undervoltage firing>resolved"}},
			},
		},
		{
			name:  "a pending alert keeps its start time over a missing sample",
			rules: []Rule{hot},
			steps: []step{
				{at: 0, series: temp(80), want: []string{"hot temp.cpu_c inactive>pending"}},
				{at: 15 * time.Second, series: none},
				{at: 30 * time.Second, series: temp(80), want: []string{"hot temp.cpu_c pending>firing"}},
			},
		},
		{
			name:  "a pending alert whose series goes away returns to inactive",
			rules: []Rule{hot},
			steps: []step{
				{at: 0, series: temp(80), want: []string{"hot temp.cpu_c inactive>pending"}},
				{at: 2 * time.Second, series: none},
				{at: 4 * time.Second, series: none},
				{at: 6 * time.Second, series: none, want: []string{"hot temp.cpu_c pending>inactive"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := NewEvaluator(tt.rules, discardLogger())
			ch, unsubscribe := e.SubscribeTransitions()
			defer unsubscribe()

			for _, s := range tt.steps {
				e.Evaluate(snapAt(s.at, s.series))
				var got []string
				for _, tr := range drain(ch) {
					got = append(got, formatTransition(tr))
					if !tr.At.Equal(t0.Add(s.at)) {
						t.Errorf("at %v: transition %s has At %v", s.at, formatTransition(tr), tr.At)
					}
				}
				if !slices.Equal(got, s.want) {
					t.Fatalf("at %v: transitions = %q, want %q", s.at, got, s.want)
				}
			}
		})
	}
}

func TestEvaluatorTimestamps(t *testing.T) {
	rule := Rule{Name: "hot", Series: "temp.cpu_c", Op: OpAbove, Threshold: 75, For: 30 * time.Second, Severity: SeverityWarning}
	e := NewEvaluator([]Rule{rule}, discardLogger())
	ch, unsubscribe := e.SubscribeTransitions()
	defer unsubscribe()

	e.Evaluate(snapAt(0, map[string]float64{"temp.cpu_c": 76}))
	e.Evaluate(snapAt(20*time.Second, map[string]float64{"temp.cpu_c": 78}))
	e.Evaluate(snapAt(30*time.Second, map[string]float64{"temp.cpu_c": 79}))

	set := e.Alerts()
	if len(set.Firing) != 1 {
		t.Fatalf("firing = %+v, want one alert", set.Firing)
	}
	a := set.Firing[0]
	if a.State != StateFiring || a.Value != 79 || !a.PendingAt.Equal(t0) ||
		a.FiringAt == nil || !a.FiringAt.Equal(t0.Add(30*time.Second)) || a.ResolvedAt != nil {
		t.Errorf("firing alert = %+v", a)
	}

	e.Evaluate(snapAt(45*time.Second, map[string]float64{"temp.cpu_c": 60}))
	set = e.Alerts()
	if len(set.Firing) != 0 || len(set.Resolved) != 1 {
		t.Fatalf("set = %+v, want one resolved alert", set)
	}
	r := set.Resolved[0]
	if r.State != StateResolved || r.Value != 60 || !r.PendingAt.Equal(t0) ||
		!r.FiringAt.Equal(t0.Add(30*time.Second)) || r.ResolvedAt == nil || !r.ResolvedAt.Equal(t0.Add(45*time.Second)) {
		t.Errorf("resolved alert = %+v", r)
	}

	trs := drain(ch)
	if len(trs) != 3 || trs[2].Alert.ResolvedAt == nil {
		t.Errorf("transitions = %+v", trs)
	}

	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"rule":"hot","series":"temp.cpu_c","severity":"warning","op":"\u003e","threshold":75,"state":"resolved","value":60,` +
		`"pending_at":"2026-11-07T12:00:00Z","firing_at":"2026-11-07T12:00:30Z","resolved_at":"2026-11-07T12:00:45Z","for_s":30}`
	if string(b) != want {
		t.Errorf("JSON =\n%s\nwant\n%s", b, want)
	}

	// An encoder with HTML escaping off gets the op unescaped.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r); err != nil {
		t.Fatal(err)
	}
	if want := strings.Replace(want, `\u003e`, ">", 1) + "\n"; buf.String() != want {
		t.Errorf("unescaped JSON =\n%s\nwant\n%s", buf.String(), want)
	}
}

func TestEvaluatorSet(t *testing.T) {
	rules := []Rule{
		{Name: "disk", Series: "fs.*.used_pct", Op: OpAbove, Threshold: 90, Severity: SeverityWarning},
		{Name: "uv", Series: "pi.undervoltage", Op: OpAbove, Threshold: 0, Severity: SeverityCritical},
		{Name: "hot", Series: "temp.cpu_c", Op: OpAbove, Threshold: 75, For: time.Minute, Severity: SeverityWarning},
	}
	e := NewEvaluator(rules, discardLogger())
	sets, unsubscribe := e.Subscribe()
	defer unsubscribe()

	if got := e.Alerts(); got.Version != 0 || got.Firing == nil || got.Resolved == nil || len(got.Firing)+len(got.Resolved) != 0 {
		t.Fatalf("initial set = %+v, want empty non-nil lists", got)
	}

	e.Evaluate(snapAt(0, map[string]float64{"fs.root.used_pct": 95, "temp.cpu_c": 80}))
	e.Evaluate(snapAt(time.Second, map[string]float64{"fs.root.used_pct": 95, "fs.a.used_pct": 99, "pi.undervoltage": 1, "temp.cpu_c": 80}))
	set := <-sets
	if set.Version != 2 || set.Resolved == nil {
		t.Errorf("version = %d, resolved = %#v; want 2 and an empty list", set.Version, set.Resolved)
	}
	var got []string
	for _, a := range set.Firing {
		got = append(got, a.Rule+" "+a.Series)
	}
	// Critical first, then by firing time, then by rule and series.
	want := []string{"uv pi.undervoltage", "disk fs.root.used_pct", "disk fs.a.used_pct"}
	if !slices.Equal(got, want) {
		t.Errorf("firing = %q, want %q", got, want)
	}

	// The pending alert does not change the set, so nothing is published.
	e.Evaluate(snapAt(2*time.Second, map[string]float64{"fs.root.used_pct": 95, "fs.a.used_pct": 99, "pi.undervoltage": 1, "temp.cpu_c": 80}))
	select {
	case s := <-sets:
		t.Fatalf("got set %+v without a change", s)
	default:
	}
	if v := e.Alerts().Version; v != 2 {
		t.Errorf("version = %d after no change, want 2", v)
	}

	// Flap the undervoltage alert past the history limit.
	for i := range ResolvedHistory + 5 {
		at := time.Duration(10+2*i) * time.Second
		e.Evaluate(snapAt(at, map[string]float64{"fs.root.used_pct": 95, "fs.a.used_pct": 99, "pi.undervoltage": 1}))
		e.Evaluate(snapAt(at+time.Second, map[string]float64{"fs.root.used_pct": 95, "fs.a.used_pct": 99, "pi.undervoltage": 0}))
	}
	set = e.Alerts()
	if len(set.Resolved) != ResolvedHistory {
		t.Fatalf("resolved = %d alerts, want %d", len(set.Resolved), ResolvedHistory)
	}
	for i := 1; i < len(set.Resolved); i++ {
		if !set.Resolved[i-1].ResolvedAt.After(*set.Resolved[i].ResolvedAt) {
			t.Fatalf("resolved alerts not newest first at %d", i)
		}
	}
	if last := <-sets; last.Version != set.Version {
		t.Errorf("subscriber has version %d, want the latest %d", last.Version, set.Version)
	}
	if len(set.Firing) != 2 {
		t.Errorf("firing = %+v, want the two disks", set.Firing)
	}
}

func TestEvaluatorRun(t *testing.T) {
	e := NewEvaluator([]Rule{{Name: "uv", Series: "pi.undervoltage", Op: OpAbove, Threshold: 0, Severity: SeverityCritical}}, discardLogger())
	sets, _ := e.Subscribe()
	trs, _ := e.SubscribeTransitions()

	snaps := make(chan metrics.Snapshot, 1)
	done := make(chan struct{})
	go func() {
		e.Run(context.Background(), snaps)
		close(done)
	}()

	snaps <- snapAt(0, map[string]float64{"pi.undervoltage": 1})
	if tr := <-trs; tr.To != StateFiring {
		t.Errorf("transition = %+v, want firing", tr)
	}
	if set := <-sets; len(set.Firing) != 1 {
		t.Errorf("set = %+v, want one firing alert", set)
	}

	close(snaps)
	<-done
	if _, ok := <-sets; ok {
		t.Error("set channel still open after Run returned")
	}
	if _, ok := <-trs; ok {
		t.Error("transition channel still open after Run returned")
	}
	if ch, _ := e.Subscribe(); !isClosed(ch) {
		t.Error("subscribing after Run returned gave an open channel")
	}
}

func isClosed[T any](ch <-chan T) bool {
	select {
	case _, ok := <-ch:
		return !ok
	default:
		return false
	}
}

func TestDefaultRulesFire(t *testing.T) {
	e := NewEvaluator(DefaultRules(), discardLogger())
	ch, unsubscribe := e.SubscribeTransitions()
	defer unsubscribe()

	bad := map[string]float64{
		"temp.cpu_c":                   85,
		"pi.undervoltage":              1,
		"pi.throttled":                 1,
		"power.undervoltage_events_5m": 1,
		"power.rtc_battery_v":          2.4,
		"fs.root.used_pct":             95,
		"mem.used_pct":                 95,
		"services.failed":              2,
	}
	for at := time.Duration(0); at <= 10*time.Minute; at += 2 * time.Second {
		e.Evaluate(snapAt(at, bad))
	}
	var fired []string
	for _, tr := range drain(ch) {
		if tr.To == StateFiring {
			fired = append(fired, fmt.Sprintf("%s@%s", tr.Alert.Rule, tr.At.Sub(t0)))
		}
	}
	slices.Sort(fired)
	want := []string{
		"cpu-temp-critical@30s",
		"cpu-temp-high@1m0s",
		"disk-full@0s",
		"memory-high@2m0s",
		"rtc-battery-low@10m0s",
		"services-failed@1m0s",
		"throttled@30s",
		"undervoltage-recent@0s",
		"undervoltage@0s",
	}
	if !slices.Equal(fired, want) {
		t.Errorf("fired = %s\nwant    %s", strings.Join(fired, " "), strings.Join(want, " "))
	}
}
