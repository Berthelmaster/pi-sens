package alerts

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParseRules(t *testing.T) {
	in := `
# Comments are fine.
rules:
  - name: cpu-hot
    series: temp.cpu_c
    op: ">"
    threshold: 70
    for: 1m30s
    severity: warning
  - name: disk-full
    series: "fs.*.used_pct"
    op: '>'
    threshold: 95.5
    severity: critical
  - name: low-voltage
    series: pi.core_v
    op: <
    threshold: 0.8
    for: 10s
    severity: warning
`
	got, err := ParseRules([]byte(in))
	if err != nil {
		t.Fatalf("ParseRules() error = %v", err)
	}
	want := []Rule{
		{Name: "cpu-hot", Series: "temp.cpu_c", Op: OpAbove, Threshold: 70, For: 90 * time.Second, Severity: SeverityWarning},
		{Name: "disk-full", Series: "fs.*.used_pct", Op: OpAbove, Threshold: 95.5, Severity: SeverityCritical},
		{Name: "low-voltage", Series: "pi.core_v", Op: OpBelow, Threshold: 0.8, For: 10 * time.Second, Severity: SeverityWarning},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseRules() =\n%+v\nwant\n%+v", got, want)
	}
}

func TestParseRulesEmptyListDisablesAlerts(t *testing.T) {
	got, err := ParseRules([]byte("rules: []\n"))
	if err != nil {
		t.Fatalf("ParseRules() error = %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("ParseRules() = %#v, want an empty list", got)
	}
}

func TestParseRulesErrors(t *testing.T) {
	const ok = "    series: temp.cpu_c\n    op: \">\"\n    threshold: 1\n    severity: warning\n"
	tests := []struct {
		name string
		in   string
		want []string // substrings of the error
	}{
		{name: "empty file", in: "  \n", want: []string{`needs a "rules" list`}},
		{name: "only a comment", in: "# nothing\n", want: []string{`needs a "rules" list`}},
		{name: "no rules key", in: "other: 1\n", want: []string{"line 1: field other not found"}},
		{name: "rules null", in: "rules:\n", want: []string{`missing "rules" list`}},
		{name: "not yaml", in: "rules: [\n", want: []string{"line"}},
		{name: "rules not a list", in: "rules: {}\n", want: []string{"line 1: cannot unmarshal !!map into a list"}},
		{name: "top level is a list", in: "- name: a\n", want: []string{`line 1: cannot unmarshal !!seq into a map with a "rules" list`}},
		{name: "rule is not a map", in: "rules:\n  - 1\n", want: []string{"line 2: cannot unmarshal !!int `1` into a rule"}},
		{
			name: "unknown field",
			in:   "rules:\n  - name: a\n    serie: temp.cpu_c\n" + ok,
			want: []string{"line 3: field serie not found"},
		},
		{
			name: "missing fields",
			in:   "rules:\n  - for: 1m\n",
			want: []string{"rule 1 (line 2):", "name is required", "series is required", "op is required", "threshold is required", "severity is required"},
		},
		{
			name: "unquoted > leaves op empty",
			in:   "rules:\n  - name: a\n    series: temp.cpu_c\n    op: >\n    threshold: 1\n    severity: warning\n",
			want: []string{`rule 1 "a" (line 2): op is required: ">" or "<", in quotes`},
		},
		{
			name: "bad op",
			in:   "rules:\n  - name: a\n    series: x\n    op: \">=\"\n    threshold: 1\n    severity: warning\n",
			want: []string{`op must be ">" or "<", got ">="`},
		},
		{
			name: "bad threshold",
			in:   "rules:\n  - name: a\n    series: x\n    op: \">\"\n    threshold: 90%\n    severity: warning\n",
			want: []string{"line 5: cannot unmarshal !!str `90%` into float64"},
		},
		{
			name: "bad duration",
			in:   "rules:\n  - name: a\n    for: 5 minutes\n" + ok,
			want: []string{`rule 1 "a" (line 2): for must be a duration such as "30s" or "2m", got "5 minutes"`},
		},
		{
			name: "duration without unit",
			in:   "rules:\n  - name: a\n    for: 30\n" + ok,
			want: []string{`got "30"`},
		},
		{
			name: "negative duration",
			in:   "rules:\n  - name: a\n    for: -1s\n" + ok,
			want: []string{"for must not be negative"},
		},
		{
			name: "bad severity",
			in:   "rules:\n  - name: a\n    series: x\n    op: \"<\"\n    threshold: 1\n    severity: info\n",
			want: []string{`severity must be "warning" or "critical", got "info"`},
		},
		{
			name: "bad glob",
			in:   "rules:\n  - name: a\n    series: \"fs.[.used_pct\"\n    op: \"<\"\n    threshold: 1\n    severity: warning\n",
			want: []string{`series "fs.[.used_pct" is not a valid glob`},
		},
		{
			name: "duplicate name",
			in:   "rules:\n  - name: a\n" + ok + "  - name: a\n" + ok,
			want: []string{`rule 2 "a" (line 7): duplicate name, already used on line 2`},
		},
		{
			name: "every bad rule is reported",
			in:   "rules:\n  - name: a\n  - name: b\n" + ok + "  - name: c\n",
			want: []string{`rule 1 "a" (line 2)`, `rule 3 "c" (line 8)`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseRules([]byte(tt.in))
			if err == nil {
				t.Fatal("ParseRules() error = nil, want an error")
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not contain %q", err, w)
				}
			}
		})
	}
}

func TestLoadRules(t *testing.T) {
	got, err := LoadRules("")
	if err != nil {
		t.Fatalf("LoadRules(\"\") error = %v", err)
	}
	if !reflect.DeepEqual(got, DefaultRules()) {
		t.Errorf("LoadRules(\"\") = %+v, want the defaults", got)
	}

	dir := t.TempDir()
	good := filepath.Join(dir, "good.yaml")
	if err := os.WriteFile(good, []byte("rules:\n  - name: a\n    series: x\n    op: \">\"\n    threshold: 1\n    severity: warning\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = LoadRules(good)
	if err != nil {
		t.Fatalf("LoadRules(good) error = %v", err)
	}
	if len(got) != 1 || got[0].Name != "a" {
		t.Errorf("LoadRules(good) = %+v", got)
	}

	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(bad, []byte("rules:\n  - name: a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = LoadRules(bad)
	if err == nil || !strings.HasPrefix(err.Error(), "alerts file "+bad+": rule 1 \"a\" (line 2): series is required") {
		t.Errorf("LoadRules(bad) error = %v", err)
	}

	_, err = LoadRules(filepath.Join(dir, "missing.yaml"))
	if err == nil || !strings.Contains(err.Error(), "alerts file: open ") {
		t.Errorf("LoadRules(missing) error = %v", err)
	}
}

func TestDefaultRules(t *testing.T) {
	names := map[string]bool{}
	for _, r := range DefaultRules() {
		if names[r.Name] {
			t.Errorf("duplicate default rule name %q", r.Name)
		}
		names[r.Name] = true
		e := ruleEntry{Name: r.Name, Series: r.Series, Op: string(r.Op), Threshold: &r.Threshold, For: r.For.String(), Severity: string(r.Severity)}
		got, err := e.rule()
		if err != nil {
			t.Errorf("default rule %s is invalid: %v", r, err)
		}
		if got != r {
			t.Errorf("default rule %s round-trips to %s", r, got)
		}
	}
}

func TestDefaultRuleNames(t *testing.T) {
	var got []string
	for _, r := range DefaultRules() {
		got = append(got, r.Name)
	}
	want := []string{
		"cpu-temp-high",
		"cpu-temp-critical",
		"undervoltage",
		"undervoltage-recent",
		"rtc-battery-low",
		"throttled",
		"disk-full",
		"memory-high",
		"services-failed",
	}
	if !slices.Equal(got, want) {
		t.Errorf("default rules = %v, want %v (update the README alerts table too)", got, want)
	}
}

// defaultRule returns the built-in rule with the given name.
func defaultRule(t *testing.T, name string) Rule {
	t.Helper()
	for _, r := range DefaultRules() {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("no default rule %q", name)
	return Rule{}
}

func TestUndervoltageRecentRule(t *testing.T) {
	r := defaultRule(t, "undervoltage-recent")
	want := Rule{Name: "undervoltage-recent", Series: "power.undervoltage_events_5m", Op: OpAbove, Threshold: 0, Severity: SeverityWarning}
	if r != want {
		t.Errorf("undervoltage-recent = %+v, want %+v", r, want)
	}
}

func TestRTCBatteryLowRule(t *testing.T) {
	r := defaultRule(t, "rtc-battery-low")
	want := Rule{Name: "rtc-battery-low", Series: "power.rtc_battery_v", Op: OpBelow, Threshold: 2.6, For: 10 * time.Minute, Severity: SeverityWarning}
	if r != want {
		t.Errorf("rtc-battery-low = %+v, want %+v", r, want)
	}
}

func TestServicesFailedRule(t *testing.T) {
	r := defaultRule(t, "services-failed")
	want := Rule{Name: "services-failed", Series: "services.failed", Op: OpAbove, Threshold: 0, For: time.Minute, Severity: SeverityWarning}
	if r != want {
		t.Fatalf("services-failed = %s, want %s", r, want)
	}

	tests := []struct {
		name   string
		series map[string]float64
		want   []string // transitions after 2m of samples every 2s
	}{
		{
			// systemctl unavailable: the services collector publishes no series.
			name:   "series missing",
			series: map[string]float64{"temp.cpu_c": 50},
		},
		{
			name:   "no failed services",
			series: map[string]float64{"services.failed": 0},
		},
		{
			name:   "one failed service",
			series: map[string]float64{"services.failed": 1},
			want: []string{
				"services-failed services.failed inactive>pending @0s",
				"services-failed services.failed pending>firing @1m0s",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := NewEvaluator([]Rule{r}, discardLogger())
			ch, unsubscribe := e.SubscribeTransitions()
			defer unsubscribe()
			for at := time.Duration(0); at <= 2*time.Minute; at += 2 * time.Second {
				e.Evaluate(snapAt(at, tt.series))
			}
			var got []string
			for _, tr := range drain(ch) {
				got = append(got, formatTransition(tr)+" @"+tr.At.Sub(t0).String())
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("transitions = %q, want %q", got, tt.want)
			}
		})
	}

	// Short of a minute it stays pending and is not listed as firing.
	e := NewEvaluator([]Rule{r}, discardLogger())
	for at := time.Duration(0); at < time.Minute; at += 2 * time.Second {
		e.Evaluate(snapAt(at, map[string]float64{"services.failed": 1}))
	}
	if set := e.Alerts(); len(set.Firing) != 0 {
		t.Errorf("firing after 58s = %+v, want none", set.Firing)
	}
}

func TestRuleMatches(t *testing.T) {
	tests := []struct {
		pattern, key string
		want         bool
	}{
		{"temp.cpu_c", "temp.cpu_c", true},
		{"temp.cpu_c", "temp.cpu_c2", false},
		{"fs.*.used_pct", "fs.root.used_pct", true},
		{"fs.*.used_pct", "fs.media_pi_my_disk.used_pct", true},
		{"fs.*.used_pct", "fs.root.used_bytes", false},
		{"fs.*.used_pct", "net.root.used_pct", false},
		{"temp.*", "temp.nvme_c", true},
		{"temp.?pu_c", "temp.cpu_c", true},
		{"temp.[cg]pu_c", "temp.gpu_c", true},
	}
	for _, tt := range tests {
		r := Rule{Series: tt.pattern}
		if got := r.matches(tt.key); got != tt.want {
			t.Errorf("Rule{Series: %q}.matches(%q) = %v, want %v", tt.pattern, tt.key, got, tt.want)
		}
	}
}
