package collectors

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

func init() { metrics.Register(&throttle{}) }

// throttle reports the firmware's throttling and under-voltage flags from
// "vcgencmd get_throttled". The low bits say what is happening right now, the
// bits from 16 up whether it has happened at any point since boot.
//
// It also counts under-voltage events: every time the "under-voltage now"
// flag goes from clear to set between two samples, since pisens started. A
// flag that is already set on the first sample counts as one. A dip that
// starts and ends between two samples is missed; the "since boot" flag still
// shows it.
//
// On a Pi 5 it adds the supply's current rating, which the firmware
// negotiates with a USB-PD supply and puts in the device tree. Other boards
// do not have it, and the fields are left out.
//
// Without a usable vcgencmd (not a Pi, tool not installed, or the service user
// not in the video group) the section is {"available": false} with no series.
type throttle struct {
	// underVoltage is the flag at the last successful sample.
	underVoltage bool
	events       int
	last         time.Time
	// recent holds the times of the events within throttleRecentWindow.
	recent []time.Time
}

// Bits of the get_throttled value.
const (
	throttleUnderVoltage  = 1 << 0
	throttleArmFreqCapped = 1 << 1
	throttleThrottled     = 1 << 2
	throttleSoftTempLimit = 1 << 3
	throttleCurrentMask   = 0xf
	// Each current bit is repeated this far up as "has occurred since boot".
	throttleOccurredShift = 16

	// throttleRecentWindow is how far back power.undervoltage_events_5m
	// counts.
	throttleRecentWindow = 5 * time.Minute
)

// The Pi 5 firmware's power properties in the device tree, each a big-endian
// u32: the current the supply offers in mA (5000 for the official 27 W
// supply, 3000 for a plain 5 V 3 A one) and whether usb_max_current_enable=1
// in config.txt lifts the 600 mA USB limit that a supply below 5 A gets.
const (
	psuMaxCurrentFile = "proc/device-tree/chosen/power/max_current"
	psuUSBMaxFile     = "proc/device-tree/chosen/power/usb_max_current_enable"
	// psuOKMilliamps is the rating a Pi 5 needs to power USB devices at
	// full current.
	psuOKMilliamps = 5000
)

type throttleData struct {
	Available bool `json:"available"`
	// Raw is the value as reported, normalised to lowercase hex: "0x50005".
	Raw   string         `json:"raw,omitempty"`
	Flags *throttleFlags `json:"flags,omitempty"`

	// PSUMaxCurrentMA is the supply's rating in mA and PSUOK whether it is
	// enough (5000 mA). USBMaxCurrentEnable is whether config.txt lifts the
	// USB current limit anyway; it is left out if the firmware does not say.
	// All three are Pi 5 only.
	PSUMaxCurrentMA     *uint32 `json:"psu_max_current_ma,omitempty"`
	PSUOK               *bool   `json:"psu_ok,omitempty"`
	USBMaxCurrentEnable *bool   `json:"usb_max_current_enable,omitempty"`

	// UndervoltageEvents counts the under-voltage events since pisens
	// started, and LastUndervoltage is when the latest one began.
	UndervoltageEvents *int       `json:"undervoltage_events,omitempty"`
	LastUndervoltage   *time.Time `json:"last_undervoltage,omitempty"`
}

type throttleFlags struct {
	// Current state (bits 0-3).
	UnderVoltage  bool `json:"under_voltage"`
	ArmFreqCapped bool `json:"arm_freq_capped"`
	Throttled     bool `json:"throttled"`
	SoftTempLimit bool `json:"soft_temp_limit"`
	// Has occurred since boot (bits 16-19).
	UnderVoltageOccurred  bool `json:"under_voltage_occurred"`
	ArmFreqCappedOccurred bool `json:"arm_freq_capped_occurred"`
	ThrottledOccurred     bool `json:"throttled_occurred"`
	SoftTempLimitOccurred bool `json:"soft_temp_limit_occurred"`
}

func (*throttle) Name() string { return "throttle" }

func (t *throttle) Collect(ctx context.Context, env *metrics.Env) (metrics.Result, error) {
	out, err := vcgencmd(ctx, env, "get_throttled")
	if errors.Is(err, errVcgencmdUnavailable) {
		return metrics.Result{Data: throttleData{Available: false}}, nil
	}
	if err != nil {
		return metrics.Result{}, err
	}
	v, err := throttleParse(out)
	if err != nil {
		return metrics.Result{}, err
	}

	flags := throttleDecode(v)
	t.track(env.Now, flags.UnderVoltage)
	events := t.events
	data := throttleData{
		Available:          true,
		Raw:                fmt.Sprintf("0x%x", v),
		Flags:              &flags,
		UndervoltageEvents: &events,
	}
	if t.events > 0 {
		last := t.last.UTC()
		data.LastUndervoltage = &last
	}
	if env.FS != nil {
		data.PSUMaxCurrentMA, data.PSUOK, data.USBMaxCurrentEnable = psuRating(env.FS)
	}
	return metrics.Result{
		Data: data,
		Series: map[string]float64{
			"pi.throttled":                 throttleBool(v&throttleCurrentMask != 0),
			"pi.undervoltage":              throttleBool(flags.UnderVoltage),
			"power.undervoltage_events":    float64(t.events),
			"power.undervoltage_events_5m": float64(len(t.recent)),
		},
	}, nil
}

// track counts an under-voltage event when the flag has gone from clear to
// set since the previous sample, and drops events that have left the recent
// window.
func (t *throttle) track(now time.Time, underVoltage bool) {
	if underVoltage && !t.underVoltage {
		t.events++
		t.last = now
		t.recent = append(t.recent, now)
	}
	t.underVoltage = underVoltage
	keep := t.recent[:0]
	for _, at := range t.recent {
		if now.Sub(at) < throttleRecentWindow {
			keep = append(keep, at)
		}
	}
	t.recent = keep
}

// psuRating reads the Pi 5 supply rating from the device tree. Each value is
// nil if its property is missing or not a u32, so a board without them
// (anything before the Pi 5) simply leaves the fields out.
func psuRating(fsys fs.FS) (maxMA *uint32, ok *bool, usbMax *bool) {
	if ma, found := readDTU32(fsys, psuMaxCurrentFile); found {
		good := ma >= psuOKMilliamps
		maxMA, ok = &ma, &good
	}
	if v, found := readDTU32(fsys, psuUSBMaxFile); found {
		on := v != 0
		usbMax = &on
	}
	return maxMA, ok, usbMax
}

// readDTU32 reads a device-tree property holding one big-endian u32. found
// is false if the file is missing, unreadable or not 4 bytes long.
func readDTU32(fsys fs.FS, name string) (v uint32, found bool) {
	b, err := fs.ReadFile(fsys, name)
	if err != nil || len(b) != 4 {
		return 0, false
	}
	return binary.BigEndian.Uint32(b), true
}

// throttleParse reads the output of get_throttled, "throttled=0x50005".
func throttleParse(out string) (uint32, error) {
	s, ok := strings.CutPrefix(strings.TrimSpace(out), "throttled=")
	if !ok {
		return 0, fmt.Errorf("vcgencmd get_throttled: unexpected output %q", out)
	}
	v, err := strconv.ParseUint(s, 0, 32)
	if err != nil {
		return 0, fmt.Errorf("vcgencmd get_throttled: %w", err)
	}
	return uint32(v), nil
}

func throttleDecode(v uint32) throttleFlags {
	bit := func(b uint32) bool { return v&b != 0 }
	oc := func(b uint32) bool { return bit(b << throttleOccurredShift) }
	return throttleFlags{
		UnderVoltage:          bit(throttleUnderVoltage),
		ArmFreqCapped:         bit(throttleArmFreqCapped),
		Throttled:             bit(throttleThrottled),
		SoftTempLimit:         bit(throttleSoftTempLimit),
		UnderVoltageOccurred:  oc(throttleUnderVoltage),
		ArmFreqCappedOccurred: oc(throttleArmFreqCapped),
		ThrottledOccurred:     oc(throttleThrottled),
		SoftTempLimitOccurred: oc(throttleSoftTempLimit),
	}
}

func throttleBool(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
