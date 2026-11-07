package collectors

import (
	"reflect"
	"testing"
)

func TestSeriesName(t *testing.T) {
	tests := []struct {
		in       string
		collapse string // collapseSeparators
		keep     string // keepSeparators
	}{
		{"", "", ""},
		{"eth0", "eth0", "eth0"},
		{"wlan0", "wlan0", "wlan0"},
		{"Sensor 1", "sensor_1", "sensor_1"},
		{"eth0.100", "eth0_100", "eth0_100"},
		{"cpu-thermal", "cpu_thermal", "cpu_thermal"},
		{"rp1_adc", "rp1_adc", "rp1_adc"},
		{"/boot/firmware", "boot_firmware", "_boot_firmware"},
		{"/media/pi/My Disk", "media_pi_my_disk", "_media_pi_my_disk"},
		{"br--lan.", "br_lan", "br__lan_"},
		{"a__b", "a_b", "a__b"},
		{"/--", "", "___"},
		{"Café", "caf", "caf__"}, // é is two bytes
	}
	for _, tt := range tests {
		if got := seriesName(tt.in, collapseSeparators); got != tt.collapse {
			t.Errorf("seriesName(%q, collapseSeparators) = %q, want %q", tt.in, got, tt.collapse)
		}
		if got := seriesName(tt.in, keepSeparators); got != tt.keep {
			t.Errorf("seriesName(%q, keepSeparators) = %q, want %q", tt.in, got, tt.keep)
		}
	}
}

func TestUniqueName(t *testing.T) {
	taken := map[string]bool{"total": true}
	var got []string
	for _, name := range []string{"nvme", "nvme", "total", "nvme", "nvme_2", "cpu"} {
		got = append(got, uniqueName(taken, name))
	}
	want := []string{"nvme", "nvme_2", "total_2", "nvme_3", "nvme_2_2", "cpu"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("uniqueName sequence = %q, want %q", got, want)
	}
	for _, name := range want {
		if !taken[name] {
			t.Errorf("uniqueName did not mark %q taken", name)
		}
	}
}
