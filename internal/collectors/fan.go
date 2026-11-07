package collectors

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

func init() { metrics.Register(fan{}) }

// fan reports the Pi 5 active cooler: its speed and PWM duty cycle from the
// pwm-fan hwmon chip, and the fan level the thermal governor has picked from
// the matching cooling device.
//
// The hwmon<N> index is assigned at probe time and can change between boots,
// so the chip is looked up every sample: first under the cooling_fan platform
// device, then in /sys/class/hwmon for any chip that has fan1_input. The
// cooling device is the first /sys/class/thermal/cooling_device<N> whose type
// is pwm-fan.
//
// A machine with neither (no cooler fitted, not a Pi) gets
// {"present": false} and no series.
type fan struct{}

const (
	fanPlatformDir = "sys/devices/platform/cooling_fan/hwmon"
	fanHwmonDir    = "sys/class/hwmon"
	fanThermalDir  = "sys/class/thermal"
	fanCoolingType = "pwm-fan"
	fanPWMMax      = 255
)

type fanData struct {
	Present bool `json:"present"`
	// Hwmon is the chip the readings come from, e.g. "hwmon3", and Chip its
	// name ("pwmfan" on a Pi 5).
	Hwmon string `json:"hwmon,omitempty"`
	Chip  string `json:"chip,omitempty"`
	RPM   *int64 `json:"rpm,omitempty"`
	// PWM is the raw duty cycle, 0–255, and PWMPct the same in percent.
	PWM    *int64   `json:"pwm,omitempty"`
	PWMPct *float64 `json:"pwm_pct,omitempty"`
	// CoolingDevice is the thermal cooling device driving the fan, e.g.
	// "cooling_device0". Level is its cur_state, 0 (off) to MaxLevel.
	CoolingDevice string `json:"cooling_device,omitempty"`
	Level         *int64 `json:"level,omitempty"`
	MaxLevel      *int64 `json:"max_level,omitempty"`
}

func (fan) Name() string { return "fan" }

func (fan) Collect(_ context.Context, env *metrics.Env) (metrics.Result, error) {
	var data fanData
	series := map[string]float64{}

	dir, err := fanFindHwmon(env.FS)
	if err != nil {
		return metrics.Result{}, err
	}
	if dir != "" {
		data.Present = true
		data.Hwmon = path.Base(dir)
		data.Chip, _, _ = fanReadValue(env.FS, path.Join(dir, "name"))

		rpm, ok, err := fanReadInt(env.FS, path.Join(dir, "fan1_input"))
		if err != nil {
			return metrics.Result{}, err
		}
		if ok {
			data.RPM = &rpm
			series["fan.rpm"] = float64(rpm)
		}

		pwm, ok, err := fanReadInt(env.FS, path.Join(dir, "pwm1"))
		if err != nil {
			return metrics.Result{}, err
		}
		if ok {
			if pwm < 0 || pwm > fanPWMMax {
				return metrics.Result{}, fmt.Errorf("%s: %d is outside 0-%d", path.Join(dir, "pwm1"), pwm, fanPWMMax)
			}
			pct := math.Round(float64(pwm)*1000/fanPWMMax) / 10
			data.PWM, data.PWMPct = &pwm, &pct
			series["fan.pwm_pct"] = pct
		}
	}

	cdev, err := fanFindCoolingDevice(env.FS)
	if err != nil {
		return metrics.Result{}, err
	}
	if cdev != "" {
		data.Present = true
		data.CoolingDevice = path.Base(cdev)
		for _, f := range []struct {
			name string
			dst  **int64
		}{{"cur_state", &data.Level}, {"max_state", &data.MaxLevel}} {
			v, ok, err := fanReadInt(env.FS, path.Join(cdev, f.name))
			if err != nil {
				return metrics.Result{}, err
			}
			if ok {
				*f.dst = &v
			}
		}
	}

	if len(series) == 0 {
		series = nil
	}
	return metrics.Result{Data: data, Series: series}, nil
}

// fanFindHwmon returns the directory of the fan's hwmon chip, or "" if there
// is none. The cooling_fan platform device is the Pi 5 active cooler and wins;
// otherwise the first chip in /sys/class/hwmon with fan1_input is used.
func fanFindHwmon(fsys fs.FS) (string, error) {
	for _, parent := range []string{fanPlatformDir, fanHwmonDir} {
		nums, err := fanNumberedEntries(fsys, parent, "hwmon")
		if err != nil {
			return "", err
		}
		for _, n := range nums {
			dir := path.Join(parent, "hwmon"+strconv.Itoa(n))
			if _, err := fs.Stat(fsys, path.Join(dir, "fan1_input")); err == nil {
				return dir, nil
			}
		}
	}
	return "", nil
}

// fanFindCoolingDevice returns the directory of the first cooling device whose
// type is pwm-fan, or "" if there is none.
func fanFindCoolingDevice(fsys fs.FS) (string, error) {
	nums, err := fanNumberedEntries(fsys, fanThermalDir, "cooling_device")
	if err != nil {
		return "", err
	}
	for _, n := range nums {
		dir := path.Join(fanThermalDir, "cooling_device"+strconv.Itoa(n))
		typ, ok, err := fanReadValue(fsys, path.Join(dir, "type"))
		if err != nil {
			return "", err
		}
		if ok && typ == fanCoolingType {
			return dir, nil
		}
	}
	return "", nil
}

// fanNumberedEntries returns N for every <prefix><N> entry of dir, ascending.
// A missing dir yields none.
func fanNumberedEntries(fsys fs.FS, dir, prefix string) ([]int, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var nums []int
	for _, e := range entries {
		s, ok := strings.CutPrefix(e.Name(), prefix)
		if !ok {
			continue
		}
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			nums = append(nums, n)
		}
	}
	sort.Ints(nums)
	return nums, nil
}

// fanReadValue reads a single-value sysfs file. ok is false if it does not
// exist.
func fanReadValue(fsys fs.FS, name string) (s string, ok bool, err error) {
	b, err := fs.ReadFile(fsys, name)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(string(b)), true, nil
}

// fanReadInt reads a sysfs integer. ok is false if the file does not exist; a
// value that is not an integer is an error.
func fanReadInt(fsys fs.FS, name string) (v int64, ok bool, err error) {
	s, ok, err := fanReadValue(fsys, name)
	if err != nil || !ok {
		return 0, ok, err
	}
	v, err = strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("%s: %w", name, err)
	}
	return v, true, nil
}
