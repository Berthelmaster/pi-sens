package promtext

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

func TestLookup(t *testing.T) {
	tests := []struct {
		key   string
		name  string
		label *Label
	}{
		{"cpu.usage_pct", "pisens_cpu_total_usage_percent", nil},
		{"cpu.iowait_pct", "pisens_cpu_iowait_percent", nil},
		{"cpu.freq_mhz", "pisens_cpu_average_frequency_megahertz", nil},
		{"cpu.core3.usage_pct", "pisens_cpu_usage_percent", &Label{"core", "3"}},
		{"cpu.core12.usage_pct", "pisens_cpu_usage_percent", &Label{"core", "12"}},
		{"cpu.core0.freq_mhz", "pisens_cpu_frequency_megahertz", &Label{"core", "0"}},

		{"load.1", "pisens_load1", nil},
		{"load.5", "pisens_load5", nil},
		{"load.15", "pisens_load15", nil},

		{"mem.used_bytes", "pisens_memory_used_bytes", nil},
		{"mem.used_pct", "pisens_memory_used_percent", nil},
		{"mem.available_bytes", "pisens_memory_available_bytes", nil},
		{"mem.cached_bytes", "pisens_memory_cached_bytes", nil},
		{"swap.used_bytes", "pisens_swap_used_bytes", nil},
		{"swap.used_pct", "pisens_swap_used_percent", nil},

		{"net.eth0.rx_bps", "pisens_net_rx_bytes_per_second", &Label{"iface", "eth0"}},
		{"net.wlan0.tx_bps", "pisens_net_tx_bytes_per_second", &Label{"iface", "wlan0"}},
		{"net.total.rx_bps", "pisens_net_total_rx_bytes_per_second", nil},
		{"net.total.tx_bps", "pisens_net_total_tx_bytes_per_second", nil},

		{"wifi.wlan0.signal_dbm", "pisens_wifi_signal_dbm", &Label{"iface", "wlan0"}},
		{"wifi.wlan0.quality_pct", "pisens_wifi_quality_percent", &Label{"iface", "wlan0"}},

		{"disk.nvme0n1.read_bps", "pisens_disk_read_bytes_per_second", &Label{"device", "nvme0n1"}},
		{"disk.mmcblk0.write_bps", "pisens_disk_write_bytes_per_second", &Label{"device", "mmcblk0"}},
		{"disk.sda.util_pct", "pisens_disk_util_percent", &Label{"device", "sda"}},

		{"fs.root.used_pct", "pisens_filesystem_used_percent", &Label{"mount", "root"}},
		{"fs.boot_firmware.used_bytes", "pisens_filesystem_used_bytes", &Label{"mount", "boot_firmware"}},

		{"temp.cpu_c", "pisens_temperature_celsius", &Label{"sensor", "cpu"}},
		{"temp.rp1_adc_c", "pisens_temperature_celsius", &Label{"sensor", "rp1_adc"}},

		{"fan.rpm", "pisens_fan_speed_rpm", nil},
		{"fan.pwm_pct", "pisens_fan_pwm_percent", nil},

		{"pi.core_v", "pisens_core_voltage_volts", nil},
		{"pi.arm_clock_mhz", "pisens_arm_clock_megahertz", nil},
		{"pi.power_w", "pisens_power_watts", nil},
		{"pi.ext5v_v", "pisens_ext5v_voltage_volts", nil},
		{"pi.throttled", "pisens_throttled", nil},
		{"pi.undervoltage", "pisens_undervoltage", nil},

		{"energy.today_kwh", "pisens_energy_today_kilowatt_hours", nil},
		{"energy.month_kwh", "pisens_energy_month_kilowatt_hours", nil},

		{"services.failed", "pisens_services_failed", nil},
		{"system.uptime_s", "pisens_uptime_seconds", nil},
		{"system.procs", "pisens_processes", nil},

		// Unknown keys fall back to a sanitised name without labels.
		{"gpu.mem_bytes", "pisens_gpu_mem_bytes", nil},
		{"weird.key-with spaces/äö", "pisens_weird_key_with_spaces___", nil},
		{"9lives", "pisens_9lives", nil},
		// A key that only looks like a labelled one needs a non-empty label.
		{"temp._c", "pisens_temp__c", nil},
		{"cpu.core.usage_pct", "pisens_cpu_core_usage_pct", nil},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got := Lookup(tt.key)
			if got.Name != tt.name {
				t.Errorf("Lookup(%q).Name = %q, want %q", tt.key, got.Name, tt.name)
			}
			switch {
			case tt.label == nil && got.Label != nil:
				t.Errorf("Lookup(%q).Label = %+v, want none", tt.key, *got.Label)
			case tt.label != nil && (got.Label == nil || *got.Label != *tt.label):
				t.Errorf("Lookup(%q).Label = %v, want %+v", tt.key, got.Label, *tt.label)
			}
			if got.Help == "" {
				t.Errorf("Lookup(%q).Help is empty", tt.key)
			}
		})
	}
}

func TestRulesAreValid(t *testing.T) {
	names := map[string]string{}
	for _, r := range rules {
		if !metricNameRE.MatchString(r.name) {
			t.Errorf("rule %q: invalid metric name %q", r.pattern, r.name)
		}
		if strings.Contains(r.pattern, "{") && !labelNameRE.MatchString(r.label) {
			t.Errorf("rule %q: invalid label name %q", r.pattern, r.label)
		}
		if strings.ContainsAny(r.prefix+r.suffix, "{}") {
			t.Errorf("rule %q: more than one placeholder", r.pattern)
		}
		if r.help == "" {
			t.Errorf("rule %q: no help", r.pattern)
		}
		// Two rules may share a name only with the same label, or the
		// family would mix labelled and unlabelled samples.
		if prev, ok := names[r.name]; ok && prev != r.label {
			t.Errorf("metric %q is used with label %q and %q", r.name, prev, r.label)
		}
		names[r.name] = r.label
	}
}

func TestWrite(t *testing.T) {
	snap := metrics.Snapshot{
		Timestamp: time.Unix(1762500000, 500_000_000),
		Series: map[string]float64{
			"cpu.usage_pct":       12.5,
			"cpu.core0.usage_pct": 10,
			"cpu.core3.usage_pct": 15,
			"net.eth0.rx_bps":     1234.56,
			"temp.cpu_c":          51.2,
			"mem.used_bytes":      3.5e9,
			"custom.thing":        -1,
		},
	}
	var b strings.Builder
	if err := Write(&b, snap); err != nil {
		t.Fatal(err)
	}
	want := `# HELP pisens_cpu_total_usage_percent CPU usage across all cores in percent.
# TYPE pisens_cpu_total_usage_percent gauge
pisens_cpu_total_usage_percent 12.5
# HELP pisens_cpu_usage_percent CPU usage per core in percent.
# TYPE pisens_cpu_usage_percent gauge
pisens_cpu_usage_percent{core="0"} 10
pisens_cpu_usage_percent{core="3"} 15
# HELP pisens_custom_thing pisens series custom.thing.
# TYPE pisens_custom_thing gauge
pisens_custom_thing -1
# HELP pisens_last_sample_timestamp_seconds Unix time of the sample these values come from.
# TYPE pisens_last_sample_timestamp_seconds gauge
pisens_last_sample_timestamp_seconds 1.7625000005e+09
# HELP pisens_memory_used_bytes Memory in use in bytes.
# TYPE pisens_memory_used_bytes gauge
pisens_memory_used_bytes 3.5e+09
# HELP pisens_net_rx_bytes_per_second Bytes received per second per network interface.
# TYPE pisens_net_rx_bytes_per_second gauge
pisens_net_rx_bytes_per_second{iface="eth0"} 1234.56
# HELP pisens_temperature_celsius Temperature per sensor in degrees Celsius.
# TYPE pisens_temperature_celsius gauge
pisens_temperature_celsius{sensor="cpu"} 51.2
`
	if got := b.String(); got != want {
		t.Errorf("Write() =\n%s\nwant\n%s", got, want)
	}
	if err := checkExposition(b.String()); err != nil {
		t.Errorf("output does not parse: %v", err)
	}
}

func TestWriteParses(t *testing.T) {
	// Every known key plus awkward ones: label values that need escaping,
	// unknown keys that collide with each other and with known names, and
	// values that are not plain numbers.
	series := map[string]float64{}
	for i, r := range rules {
		key := r.pattern
		if r.label != "" {
			key = r.prefix + "x" + r.suffix
		}
		series[key] = float64(i) + 0.25
	}
	for k, v := range map[string]float64{
		"cpu.core10.usage_pct":          1,
		`temp.we"ird\name_c`:            2,
		"temp.line\nbreak_c":            3,
		"a.b":                           4,
		"a_b":                           5, // same sanitised name as a.b
		"cpu_iowait_percent":            6, // sanitises to a known name
		"pisens.last_sample":            7,
		"huge.value":                    math.MaxFloat64,
		"tiny.value":                    math.SmallestNonzeroFloat64,
		"nan.value":                     math.NaN(),
		"inf.value":                     math.Inf(1),
		"neg_inf.value":                 math.Inf(-1),
		"last_sample_timestamp_seconds": 8, // collides with the timestamp metric
	} {
		series[k] = v
	}
	var b strings.Builder
	if err := Write(&b, metrics.Snapshot{Timestamp: time.Now(), Series: series}); err != nil {
		t.Fatal(err)
	}
	if err := checkExposition(b.String()); err != nil {
		t.Fatalf("output does not parse: %v\n%s", err, b.String())
	}
	if !strings.Contains(b.String(), `pisens_temperature_celsius{sensor="we\"ird\\name"} 2`+"\n") {
		t.Errorf("escaped label value missing:\n%s", b.String())
	}
	if !strings.Contains(b.String(), `pisens_temperature_celsius{sensor="line\nbreak"} 3`+"\n") {
		t.Errorf("escaped newline missing:\n%s", b.String())
	}
	if !strings.Contains(b.String(), "pisens_a_b 4\n") || strings.Contains(b.String(), "pisens_a_b 5\n") {
		t.Errorf("want the first of two colliding keys only:\n%s", b.String())
	}
}

func TestWriteEmpty(t *testing.T) {
	var b strings.Builder
	if err := Write(&b, metrics.Snapshot{}); err != nil {
		t.Fatal(err)
	}
	if b.String() != "" {
		t.Errorf("Write(empty) = %q, want nothing", b.String())
	}
}

var (
	metricNameRE = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)
	labelNameRE  = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
	sampleRE     = regexp.MustCompile(`^([a-zA-Z_:][a-zA-Z0-9_:]*)(\{(.*)\})? (\S+)$`)
	labelRE      = regexp.MustCompile(`^([a-zA-Z_][a-zA-Z0-9_]*)="((?:[^"\\\n]|\\[\\"n])*)"`)
)

// checkExposition is a strict parser for the subset of the text format
// (version 0.0.4) that Write produces, applying the same checks as
// promtool check metrics: valid names and label syntax, one HELP and one
// TYPE line per family before its samples, families not split up, no
// duplicate series, parseable values and a trailing newline.
func checkExposition(text string) error {
	if text == "" {
		return nil
	}
	if !strings.HasSuffix(text, "\n") {
		return fmt.Errorf("no trailing newline")
	}
	type state struct{ help, typ, samples bool }
	families := map[string]*state{}
	series := map[string]bool{}
	current := ""
	family := func(name string) *state {
		if name != current {
			if _, done := families[name]; done {
				return nil // the family was seen before and is split up
			}
			families[name] = &state{}
			current = name
		}
		return families[name]
	}

	for i, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		n := i + 1
		switch {
		case strings.HasPrefix(line, "# HELP "):
			name, help, ok := strings.Cut(strings.TrimPrefix(line, "# HELP "), " ")
			if !ok || !metricNameRE.MatchString(name) {
				return fmt.Errorf("line %d: bad HELP line %q", n, line)
			}
			for j := 0; j < len(help); j++ {
				if help[j] != '\\' {
					continue
				}
				if j+1 == len(help) || (help[j+1] != '\\' && help[j+1] != 'n') {
					return fmt.Errorf("line %d: bad escape in HELP %q", n, help)
				}
				j++
			}
			f := family(name)
			if f == nil || f.help || f.samples {
				return fmt.Errorf("line %d: HELP for %s repeated or after its samples", n, name)
			}
			f.help = true
		case strings.HasPrefix(line, "# TYPE "):
			name, typ, ok := strings.Cut(strings.TrimPrefix(line, "# TYPE "), " ")
			if !ok || !metricNameRE.MatchString(name) || typ != "gauge" {
				return fmt.Errorf("line %d: bad TYPE line %q", n, line)
			}
			f := family(name)
			if f == nil || f.typ || f.samples {
				return fmt.Errorf("line %d: TYPE for %s repeated or after its samples", n, name)
			}
			f.typ = true
		case strings.HasPrefix(line, "#") || line == "":
			return fmt.Errorf("line %d: unexpected line %q", n, line)
		default:
			m := sampleRE.FindStringSubmatch(line)
			if m == nil {
				return fmt.Errorf("line %d: bad sample %q", n, line)
			}
			name, labels, value := m[1], m[3], m[4]
			f := family(name)
			if f == nil {
				return fmt.Errorf("line %d: family %s split up", n, name)
			}
			if !f.help || !f.typ {
				return fmt.Errorf("line %d: sample of %s before its HELP and TYPE", n, name)
			}
			f.samples = true
			seenLabels := map[string]bool{}
			for rest := labels; rest != ""; {
				lm := labelRE.FindStringSubmatch(rest)
				if lm == nil {
					return fmt.Errorf("line %d: bad labels %q", n, labels)
				}
				if seenLabels[lm[1]] {
					return fmt.Errorf("line %d: label %s repeated", n, lm[1])
				}
				seenLabels[lm[1]] = true
				rest = strings.TrimPrefix(rest[len(lm[0]):], ",")
			}
			if _, err := strconv.ParseFloat(value, 64); err != nil {
				return fmt.Errorf("line %d: bad value %q", n, value)
			}
			if series[name+"{"+labels+"}"] {
				return fmt.Errorf("line %d: duplicate series %s{%s}", n, name, labels)
			}
			series[name+"{"+labels+"}"] = true
		}
	}
	for name, f := range families {
		if !f.samples {
			return fmt.Errorf("family %s has no samples", name)
		}
	}
	return nil
}

func TestCheckExpositionRejects(t *testing.T) {
	bad := map[string]string{
		"no newline":       "# HELP a x\n# TYPE a gauge\na 1",
		"no type":          "# HELP a x\na 1\n",
		"bad name":         "# HELP 1a x\n# TYPE 1a gauge\n1a 1\n",
		"duplicate series": "# HELP a x\n# TYPE a gauge\na 1\na 2\n",
		"split family":     "# HELP a x\n# TYPE a gauge\na 1\n# HELP b x\n# TYPE b gauge\nb 1\na{x=\"1\"} 2\n",
		"bad label":        "# HELP a x\n# TYPE a gauge\na{x=\"un\"escaped\"} 1\n",
		"bad value":        "# HELP a x\n# TYPE a gauge\na one\n",
		"empty family":     "# HELP a x\n# TYPE a gauge\n",
		"bad help escape":  "# HELP a x\\\"y\n# TYPE a gauge\na 1\n",
	}
	for name, text := range bad {
		if checkExposition(text) == nil {
			t.Errorf("%s: checkExposition accepted %q", name, text)
		}
	}
}
