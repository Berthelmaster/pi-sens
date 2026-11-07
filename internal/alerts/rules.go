// Package alerts evaluates threshold rules against every metrics snapshot and
// keeps track of which alerts are pending, firing and recently resolved.
//
// A Rule compares one series, or every series matching a glob such as
// "fs.*.used_pct", against a threshold. When the comparison holds the alert
// for that series becomes pending, and once it has held for the rule's For
// duration it fires. It resolves as soon as the comparison stops holding, or
// when the series has been missing from StaleSamples snapshots in a row.
//
// The Evaluator publishes the set of firing and resolved alerts to the HTTP
// API and every state change as a Transition for notifiers.
package alerts

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Op is the comparison a rule makes between a series value and its threshold.
type Op string

// Supported comparisons.
const (
	OpAbove Op = ">"
	OpBelow Op = "<"
)

// Severity is how serious an alert is.
type Severity string

// Supported severities.
const (
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// Rule is one threshold check.
type Rule struct {
	// Name identifies the rule. It is unique within a rule set.
	Name string
	// Series is an exact series key such as "temp.cpu_c", or a glob such as
	// "fs.*.used_pct" that is checked against every series in a snapshot.
	// The glob syntax is that of path.Match.
	Series string
	Op     Op
	// Threshold is the value the series is compared with.
	Threshold float64
	// For is how long the comparison has to hold before the alert fires.
	// Zero fires on the first matching sample.
	For      time.Duration
	Severity Severity
}

// isGlob reports whether Series is a pattern rather than an exact key.
func (r Rule) isGlob() bool {
	return strings.ContainsAny(r.Series, `*?[\`)
}

// matches reports whether the series key falls under the rule.
func (r Rule) matches(key string) bool {
	if !r.isGlob() {
		return key == r.Series
	}
	ok, _ := path.Match(r.Series, key) // the pattern was checked by validate
	return ok
}

// breached reports whether v crosses the threshold.
func (r Rule) breached(v float64) bool {
	switch r.Op {
	case OpAbove:
		return v > r.Threshold
	case OpBelow:
		return v < r.Threshold
	}
	return false
}

// String formats the rule as "temp.cpu_c > 75 for 1m0s (warning)".
func (r Rule) String() string {
	s := fmt.Sprintf("%s %s %g", r.Series, r.Op, r.Threshold)
	if r.For > 0 {
		s += " for " + r.For.String()
	}
	return s + " (" + string(r.Severity) + ")"
}

// DefaultRules returns the rules used when no rules file is configured.
func DefaultRules() []Rule {
	return []Rule{
		{Name: "cpu-temp-high", Series: "temp.cpu_c", Op: OpAbove, Threshold: 75, For: time.Minute, Severity: SeverityWarning},
		{Name: "cpu-temp-critical", Series: "temp.cpu_c", Op: OpAbove, Threshold: 82, For: 30 * time.Second, Severity: SeverityCritical},
		{Name: "undervoltage", Series: "pi.undervoltage", Op: OpAbove, Threshold: 0, Severity: SeverityCritical},
		{Name: "undervoltage-recent", Series: "power.undervoltage_events_5m", Op: OpAbove, Threshold: 0, Severity: SeverityWarning},
		{Name: "throttled", Series: "pi.throttled", Op: OpAbove, Threshold: 0, For: 30 * time.Second, Severity: SeverityWarning},
		{Name: "disk-full", Series: "fs.*.used_pct", Op: OpAbove, Threshold: 90, Severity: SeverityWarning},
		{Name: "memory-high", Series: "mem.used_pct", Op: OpAbove, Threshold: 90, For: 2 * time.Minute, Severity: SeverityWarning},
		{Name: "services-failed", Series: "services.failed", Op: OpAbove, Threshold: 0, For: time.Minute, Severity: SeverityWarning},
	}
}

// LoadRules reads the rules file at path. An empty path means no file was
// configured and returns DefaultRules. Errors name the file, and the rule
// and line where possible, so they can be shown to the user as they are.
func LoadRules(path string) ([]Rule, error) {
	if path == "" {
		return DefaultRules(), nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("alerts file: %w", err)
	}
	rules, err := ParseRules(b)
	if err != nil {
		return nil, fmt.Errorf("alerts file %s: %w", path, err)
	}
	return rules, nil
}

// rulesFile is the layout of a rules file.
type rulesFile struct {
	Rules *[]yaml.Node `yaml:"rules"`
}

// entriesFile is rulesFile with the rules decoded.
type entriesFile struct {
	Rules []ruleEntry `yaml:"rules"`
}

// ruleEntry is one rule as written in a rules file. Pointers tell a missing
// field from a zero one.
type ruleEntry struct {
	Name      string   `yaml:"name"`
	Series    string   `yaml:"series"`
	Op        string   `yaml:"op"`
	Threshold *float64 `yaml:"threshold"`
	For       string   `yaml:"for"`
	Severity  string   `yaml:"severity"`
}

// ParseRules parses the YAML rules format:
//
//	rules:
//	  - name: cpu-temp-high
//	    series: temp.cpu_c
//	    op: ">"
//	    threshold: 75
//	    for: 1m
//	    severity: warning
//
// Unknown fields are rejected so a typo cannot silently disable a check. An
// explicit empty list ("rules: []") turns all alerts off.
func ParseRules(b []byte) ([]Rule, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, errors.New(`the file is empty; it needs a "rules" list (use "rules: []" to turn alerts off)`)
	}
	// Decode twice: once into nodes for the line of each rule, and once into
	// ruleEntry values, which is where unknown fields and type errors show up.
	var f rulesFile
	if err := decodeStrict(b, &f); err != nil {
		return nil, err
	}
	if f.Rules == nil {
		return nil, errors.New(`missing "rules" list (use "rules: []" to turn alerts off)`)
	}
	var entries entriesFile
	if err := decodeStrict(b, &entries); err != nil {
		return nil, err
	}

	rules := make([]Rule, 0, len(*f.Rules))
	var errs []error
	names := map[string]int{} // name -> line of first use
	for i, node := range *f.Rules {
		e := entries.Rules[i]
		r, err := e.rule()
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", entryLabel(i, node.Line, e.Name), err))
			continue
		}
		if first, dup := names[r.Name]; dup {
			errs = append(errs, fmt.Errorf("%s: duplicate name, already used on line %d", entryLabel(i, node.Line, e.Name), first))
			continue
		}
		names[r.Name] = node.Line
		rules = append(rules, r)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return rules, nil
}

func entryLabel(i, line int, name string) string {
	if name == "" {
		return fmt.Sprintf("rule %d (line %d)", i+1, line)
	}
	return fmt.Sprintf("rule %d %q (line %d)", i+1, name, line)
}

// decodeStrict decodes the YAML in b into v, rejecting unknown fields.
func decodeStrict(b []byte, v any) error {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	err := dec.Decode(v)
	if errors.Is(err, io.EOF) {
		return errors.New(`the file has no content; it needs a "rules" list (use "rules: []" to turn alerts off)`)
	}
	if err != nil {
		return cleanYAMLError(err)
	}
	return nil
}

// yamlTypes names the Go types that show up in yaml errors in words.
var yamlTypes = strings.NewReplacer(
	" in type alerts.rulesFile", "",
	" in type alerts.entriesFile", "",
	" in type alerts.ruleEntry", "",
	"into []yaml.Node", "into a list",
	"into []alerts.ruleEntry", "into a list",
	"into alerts.ruleEntry", "into a rule",
	"into alerts.rulesFile", `into a map with a "rules" list`,
	"into alerts.entriesFile", `into a map with a "rules" list`,
)

// cleanYAMLError makes a yaml error fit on one line: it drops the "yaml: "
// prefix and the Go type names, and joins multiple errors with "; ".
func cleanYAMLError(err error) error {
	msg := strings.TrimPrefix(err.Error(), "yaml: ")
	msg = strings.TrimPrefix(msg, "unmarshal errors:\n")
	msg = yamlTypes.Replace(msg)
	lines := strings.Split(msg, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return errors.New(strings.Join(lines, "; "))
}

func (e ruleEntry) rule() (Rule, error) {
	var errs []error
	r := Rule{
		Name:     strings.TrimSpace(e.Name),
		Series:   strings.TrimSpace(e.Series),
		Op:       Op(strings.TrimSpace(e.Op)),
		Severity: Severity(strings.TrimSpace(e.Severity)),
	}
	if r.Name == "" {
		errs = append(errs, errors.New("name is required"))
	}
	switch {
	case r.Series == "":
		errs = append(errs, errors.New("series is required"))
	case r.isGlob():
		if _, err := path.Match(r.Series, ""); err != nil {
			errs = append(errs, fmt.Errorf("series %q is not a valid glob", r.Series))
		}
	}
	switch r.Op {
	case OpAbove, OpBelow:
	case "":
		errs = append(errs, errors.New(`op is required: ">" or "<", in quotes`))
	default:
		errs = append(errs, fmt.Errorf(`op must be ">" or "<", got %q`, e.Op))
	}
	if e.Threshold == nil {
		errs = append(errs, errors.New("threshold is required"))
	} else {
		r.Threshold = *e.Threshold
	}
	if f := strings.TrimSpace(e.For); f != "" {
		d, err := time.ParseDuration(f)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf(`for must be a duration such as "30s" or "2m", got %q`, e.For))
		case d < 0:
			errs = append(errs, fmt.Errorf("for must not be negative, got %s", e.For))
		default:
			r.For = d
		}
	}
	switch r.Severity {
	case SeverityWarning, SeverityCritical:
	case "":
		errs = append(errs, errors.New(`severity is required: "warning" or "critical"`))
	default:
		errs = append(errs, fmt.Errorf(`severity must be "warning" or "critical", got %q`, e.Severity))
	}
	if len(errs) == 0 {
		return r, nil
	}
	msgs := make([]string, len(errs))
	for i, err := range errs {
		msgs[i] = err.Error()
	}
	return r, errors.New(strings.Join(msgs, "; "))
}
