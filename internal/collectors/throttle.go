package collectors

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

func init() { metrics.Register(throttle{}) }

// throttle reports the firmware's throttling and under-voltage flags from
// "vcgencmd get_throttled". The low bits say what is happening right now, the
// bits from 16 up whether it has happened at any point since boot.
//
// Without a usable vcgencmd (not a Pi, tool not installed, or the service user
// not in the video group) the section is {"available": false} with no series.
type throttle struct{}

// Bits of the get_throttled value.
const (
	throttleUnderVoltage  = 1 << 0
	throttleArmFreqCapped = 1 << 1
	throttleThrottled     = 1 << 2
	throttleSoftTempLimit = 1 << 3
	throttleCurrentMask   = 0xf
	// Each current bit is repeated this far up as "has occurred since boot".
	throttleOccurredShift = 16
)

type throttleData struct {
	Available bool `json:"available"`
	// Raw is the value as reported, normalised to lowercase hex: "0x50005".
	Raw   string         `json:"raw,omitempty"`
	Flags *throttleFlags `json:"flags,omitempty"`
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

func (throttle) Name() string { return "throttle" }

func (throttle) Collect(ctx context.Context, env *metrics.Env) (metrics.Result, error) {
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
	return metrics.Result{
		Data: throttleData{
			Available: true,
			Raw:       fmt.Sprintf("0x%x", v),
			Flags:     &flags,
		},
		Series: map[string]float64{
			"pi.throttled":    throttleBool(v&throttleCurrentMask != 0),
			"pi.undervoltage": throttleBool(flags.UnderVoltage),
		},
	}, nil
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
