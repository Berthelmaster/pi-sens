package collectors

import (
	"context"
	"math"
	"path"
	"strconv"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

func init() { metrics.Register(rtc{}) }

// rtc reports the backup battery of the real-time clock, which keeps the
// time across power cuts. When it runs flat the clock is wrong after a power
// cut until NTP syncs, and so are the history timestamps of that stretch.
//
// The Pi 5 RTC (rpi-rtc) exposes battery_voltage and, while trickle charging
// is set up with dtparam=rtc_bbat_vchg, a non-zero charging_voltage, both in
// microvolts. The rtc<N> index is assigned at probe time and an RTC HAT may
// add another, so the device is the first /sys/class/rtc/rtc<N> that has
// battery_voltage.
//
// No RTC with a battery_voltage gives {"present": false}; an RTC that reads
// (close to) 0 V has no battery fitted and gives {"present": true,
// "battery": false}. Neither has a series.
type rtc struct{}

const (
	rtcClassDir = "sys/class/rtc"
	// rtcNoBatteryV is the reading below which no battery is fitted. Without
	// one the driver reads 0; a cell that low could not keep the clock
	// running anyway.
	rtcNoBatteryV = 0.1
)

type rtcData struct {
	Present bool `json:"present"`
	// RTC is the device the readings come from, e.g. "rtc0", and Name its
	// driver name ("rpi-rtc soc@107c000000:rpi_rtc" on a Pi 5).
	RTC     string `json:"rtc,omitempty"`
	Name    string `json:"name,omitempty"`
	Battery bool   `json:"battery"`
	// BatteryV is the battery voltage, omitted without a battery.
	BatteryV *float64 `json:"battery_v,omitempty"`
	// Charging is whether trickle charging is on, and ChargingV the voltage
	// it charges to. Both are omitted if the RTC cannot charge; ChargingV is
	// also omitted while charging is off.
	Charging  *bool    `json:"charging,omitempty"`
	ChargingV *float64 `json:"charging_voltage_v,omitempty"`
}

func (rtc) Name() string { return "rtc" }

func (rtc) Collect(_ context.Context, env *metrics.Env) (metrics.Result, error) {
	nums, err := numberedEntries(env.FS, rtcClassDir, "rtc")
	if err != nil {
		return metrics.Result{}, err
	}
	for _, n := range nums {
		dir := path.Join(rtcClassDir, "rtc"+strconv.Itoa(n))
		uv, ok, err := readInt(env.FS, path.Join(dir, "battery_voltage"))
		if err != nil {
			return metrics.Result{}, err
		}
		if !ok {
			continue
		}

		data := rtcData{Present: true, RTC: path.Base(dir)}
		data.Name, _, _ = readValue(env.FS, path.Join(dir, "name"))

		v := rtcVolts(uv)
		if v < rtcNoBatteryV {
			return metrics.Result{Data: data}, nil
		}
		data.Battery, data.BatteryV = true, &v

		cuv, ok, err := readInt(env.FS, path.Join(dir, "charging_voltage"))
		if err != nil {
			return metrics.Result{}, err
		}
		if ok {
			charging := cuv > 0
			data.Charging = &charging
			if charging {
				cv := rtcVolts(cuv)
				data.ChargingV = &cv
			}
		}

		return metrics.Result{
			Data:   data,
			Series: map[string]float64{"power.rtc_battery_v": v},
		}, nil
	}
	return metrics.Result{Data: rtcData{}}, nil
}

// rtcVolts turns a reading in microvolts into volts, rounded to millivolts.
func rtcVolts(uv int64) float64 { return math.Round(float64(uv)/1e3) / 1e3 }
