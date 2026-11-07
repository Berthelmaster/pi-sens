// Package promtext renders a metrics snapshot in the Prometheus text
// exposition format (version 0.0.4) for GET /metrics.
//
// Every series key in the snapshot maps to a metric name and, for keys that
// name a core, interface, disk and so on, a label. The known keys are listed
// in rules; any other key falls back to a sanitised name, so a new collector
// shows up without changes here. Every metric is a gauge: the series are
// current values and rates, not counters.
package promtext

import (
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

// ContentType is the Content-Type of the text format.
const ContentType = "text/plain; version=0.0.4; charset=utf-8"

// Prefix starts every metric name.
const Prefix = "pisens_"

// Label is one name="value" pair on a sample.
type Label struct {
	Name  string
	Value string
}

// Metric is where one series key ends up in the output.
type Metric struct {
	// Name is the metric family name, e.g. "pisens_cpu_usage_percent".
	Name string
	// Label is set when the key carries a dimension, e.g. core="3".
	Label *Label
	// Help is the family's HELP text.
	Help string
}

// rule maps series keys to a metric. A pattern is either an exact key or has
// one {label} placeholder, which matches a non-empty part of the key and
// becomes the value of that label: "cpu.core{core}.usage_pct" matches
// "cpu.core3.usage_pct" with core="3".
type rule struct {
	pattern string
	name    string
	help    string

	// Derived from pattern by compileRules.
	prefix, suffix, label string
}

// rules lists the known series keys. Exact keys come first, so "net.total.*"
// wins over "net.{iface}.*". Units are kept as the collectors report them
// (percent, MHz) and named in the metric suffix.
var rules = compileRules([]rule{
	{pattern: "cpu.usage_pct", name: "cpu_total_usage_percent", help: "CPU usage across all cores in percent."},
	{pattern: "cpu.iowait_pct", name: "cpu_iowait_percent", help: "Share of CPU time spent waiting for I/O in percent."},
	{pattern: "cpu.freq_mhz", name: "cpu_average_frequency_megahertz", help: "Average current CPU core frequency in MHz."},
	{pattern: "cpu.core{core}.usage_pct", name: "cpu_usage_percent", help: "CPU usage per core in percent."},
	{pattern: "cpu.core{core}.freq_mhz", name: "cpu_frequency_megahertz", help: "Current CPU frequency per core in MHz."},

	{pattern: "load.1", name: "load1", help: "1 minute load average."},
	{pattern: "load.5", name: "load5", help: "5 minute load average."},
	{pattern: "load.15", name: "load15", help: "15 minute load average."},

	{pattern: "mem.used_bytes", name: "memory_used_bytes", help: "Memory in use in bytes."},
	{pattern: "mem.used_pct", name: "memory_used_percent", help: "Memory in use in percent of the total."},
	{pattern: "mem.available_bytes", name: "memory_available_bytes", help: "Memory available for new work in bytes."},
	{pattern: "mem.cached_bytes", name: "memory_cached_bytes", help: "Memory used by the page cache in bytes."},
	{pattern: "swap.used_bytes", name: "swap_used_bytes", help: "Swap in use in bytes."},
	{pattern: "swap.used_pct", name: "swap_used_percent", help: "Swap in use in percent of the total."},

	{pattern: "net.total.rx_bps", name: "net_total_rx_bytes_per_second", help: "Bytes received per second over all interfaces."},
	{pattern: "net.total.tx_bps", name: "net_total_tx_bytes_per_second", help: "Bytes sent per second over all interfaces."},
	{pattern: "net.{iface}.rx_bps", name: "net_rx_bytes_per_second", help: "Bytes received per second per network interface."},
	{pattern: "net.{iface}.tx_bps", name: "net_tx_bytes_per_second", help: "Bytes sent per second per network interface."},

	{pattern: "wifi.{iface}.signal_dbm", name: "wifi_signal_dbm", help: "Wi-Fi signal strength in dBm."},
	{pattern: "wifi.{iface}.quality_pct", name: "wifi_quality_percent", help: "Wi-Fi link quality in percent."},

	{pattern: "disk.{device}.read_bps", name: "disk_read_bytes_per_second", help: "Bytes read per second per block device."},
	{pattern: "disk.{device}.write_bps", name: "disk_write_bytes_per_second", help: "Bytes written per second per block device."},
	{pattern: "disk.{device}.util_pct", name: "disk_util_percent", help: "Share of time the block device was busy in percent."},

	{pattern: "fs.{mount}.used_pct", name: "filesystem_used_percent", help: "Filesystem space in use in percent."},
	{pattern: "fs.{mount}.used_bytes", name: "filesystem_used_bytes", help: "Filesystem space in use in bytes."},

	{pattern: "temp.{sensor}_c", name: "temperature_celsius", help: "Temperature per sensor in degrees Celsius."},

	{pattern: "fan.rpm", name: "fan_speed_rpm", help: "Fan speed in revolutions per minute."},
	{pattern: "fan.pwm_pct", name: "fan_pwm_percent", help: "Fan PWM duty cycle in percent."},

	{pattern: "pi.core_v", name: "core_voltage_volts", help: "SoC core voltage in volts."},
	{pattern: "pi.arm_clock_mhz", name: "arm_clock_megahertz", help: "ARM clock reported by the firmware in MHz."},
	{pattern: "pi.power_w", name: "power_watts", help: "Total power drawn as measured by the PMIC in watts."},
	{pattern: "pi.ext5v_v", name: "ext5v_voltage_volts", help: "External 5 V supply voltage in volts."},
	{pattern: "pi.throttled", name: "throttled", help: "1 while the firmware is throttling the CPU, otherwise 0."},
	{pattern: "pi.undervoltage", name: "undervoltage", help: "1 while the supply voltage is too low, otherwise 0."},
	{pattern: "power.undervoltage_events", name: "undervoltage_events", help: "Under-voltage events since pisens started."},
	{pattern: "power.undervoltage_events_5m", name: "undervoltage_events_5m", help: "Under-voltage events in the last 5 minutes."},

	{pattern: "energy.today_kwh", name: "energy_today_kilowatt_hours", help: "Energy used today since local midnight in kWh, from the PMIC power readings."},
	{pattern: "energy.month_kwh", name: "energy_month_kilowatt_hours", help: "Energy used this calendar month in kWh, from the PMIC power readings."},

	{pattern: "services.failed", name: "services_failed", help: "Number of failed systemd services."},

	{pattern: "system.uptime_s", name: "uptime_seconds", help: "System uptime in seconds."},
	{pattern: "system.procs", name: "processes", help: "Number of processes."},
})

func compileRules(rs []rule) []rule {
	for i := range rs {
		r := &rs[i]
		r.name = Prefix + r.name
		if open := strings.IndexByte(r.pattern, '{'); open >= 0 {
			end := strings.IndexByte(r.pattern, '}')
			r.prefix, r.label, r.suffix = r.pattern[:open], r.pattern[open+1:end], r.pattern[end+1:]
		}
	}
	return rs
}

// Lookup returns the metric a series key maps to.
func Lookup(key string) Metric {
	for _, r := range rules {
		if r.label == "" {
			if key == r.pattern {
				return Metric{Name: r.name, Help: r.help}
			}
			continue
		}
		if len(key) <= len(r.prefix)+len(r.suffix) ||
			!strings.HasPrefix(key, r.prefix) || !strings.HasSuffix(key, r.suffix) {
			continue
		}
		value := key[len(r.prefix) : len(key)-len(r.suffix)]
		return Metric{Name: r.name, Label: &Label{Name: r.label, Value: value}, Help: r.help}
	}
	return Metric{Name: SanitiseName(key), Help: "pisens series " + key + "."}
}

// SanitiseName turns an unknown series key into a valid metric name: it adds
// the prefix and replaces every character outside [a-zA-Z0-9_] with an
// underscore, e.g. "foo.bar-baz" becomes "pisens_foo_bar_baz".
func SanitiseName(key string) string {
	var b strings.Builder
	b.Grow(len(Prefix) + len(key))
	b.WriteString(Prefix)
	for _, r := range key {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// timestampMetric reports when the snapshot was taken, so a dashboard can
// tell a stale exporter from a quiet Pi.
const (
	timestampMetric = Prefix + "last_sample_timestamp_seconds"
	timestampHelp   = "Unix time of the sample these values come from."
)

type sample struct {
	labels string // rendered {...} or ""
	value  float64
}

type family struct {
	name    string
	help    string
	samples []sample
}

// Write renders snap in the text format. Families are sorted by name and
// samples by series key, so the output is stable. When two keys end up as
// the same metric with the same labels, only the first key (in sort order)
// is written, because a scrape with duplicate series is rejected whole.
func Write(w io.Writer, snap metrics.Snapshot) error {
	keys := make([]string, 0, len(snap.Series))
	for k := range snap.Series {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	families := map[string]*family{}
	seen := map[string]bool{}
	// The timestamp goes in first so a series key that happens to sanitise
	// to the same name cannot replace it.
	if !snap.Timestamp.IsZero() {
		ts := float64(snap.Timestamp.UnixNano()) / float64(time.Second)
		families[timestampMetric] = &family{name: timestampMetric, help: timestampHelp,
			samples: []sample{{value: ts}}}
		seen[timestampMetric] = true
	}
	for _, k := range keys {
		m := Lookup(k)
		labels := renderLabels(m.Label)
		if seen[m.Name+labels] {
			continue
		}
		seen[m.Name+labels] = true
		f := families[m.Name]
		if f == nil {
			f = &family{name: m.Name, help: m.Help}
			families[m.Name] = f
		}
		f.samples = append(f.samples, sample{labels: labels, value: snap.Series[k]})
	}

	names := make([]string, 0, len(families))
	for n := range families {
		names = append(names, n)
	}
	sort.Strings(names)

	// The output is a few kB, so it is built in memory and written once.
	var b strings.Builder
	for _, n := range names {
		f := families[n]
		b.WriteString("# HELP " + f.name + " " + escapeHelp(f.help) + "\n")
		b.WriteString("# TYPE " + f.name + " gauge\n")
		for _, s := range f.samples {
			b.WriteString(f.name + s.labels + " " + formatValue(s.value) + "\n")
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func renderLabels(l *Label) string {
	if l == nil {
		return ""
	}
	return "{" + l.Name + `="` + escapeLabelValue(l.Value) + `"}`
}

var (
	helpEscaper  = strings.NewReplacer(`\`, `\\`, "\n", `\n`)
	labelEscaper = strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`)
)

func escapeHelp(s string) string       { return helpEscaper.Replace(s) }
func escapeLabelValue(s string) string { return labelEscaper.Replace(s) }

func formatValue(v float64) string {
	switch {
	case math.IsNaN(v):
		return "NaN"
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}
