package collectors

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

func init() { metrics.Register(cpufreq{}) }

// cpufreq reports the clock frequency and scaling governor of every core, from
// /sys/devices/system/cpu/cpu<N>/cpufreq/. Machines without cpufreq (most VMs
// and CI runners) get an empty section rather than an error.
type cpufreq struct{}

const cpufreqSysDir = "sys/devices/system/cpu"

type cpufreqData struct {
	// AvgMHz is the mean current frequency of all cores; omitted when there
	// are none.
	AvgMHz float64       `json:"avg_mhz,omitempty"`
	Cores  []cpufreqCore `json:"cores"`
}

type cpufreqCore struct {
	Core     int     `json:"core"`
	CurMHz   float64 `json:"cur_mhz"`
	MinMHz   float64 `json:"min_mhz,omitempty"`
	MaxMHz   float64 `json:"max_mhz,omitempty"`
	Governor string  `json:"governor,omitempty"`
}

func (cpufreq) Name() string { return "cpufreq" }

func (cpufreq) Collect(_ context.Context, env *metrics.Env) (metrics.Result, error) {
	cores, err := cpufreqCoreNumbers(env.FS)
	if err != nil {
		return metrics.Result{}, err
	}

	data := cpufreqData{Cores: []cpufreqCore{}}
	series := map[string]float64{}
	var sum float64
	for _, n := range cores {
		dir := path.Join(cpufreqSysDir, "cpu"+strconv.Itoa(n), "cpufreq")
		cur, ok, err := cpufreqReadMHz(env.FS, path.Join(dir, "scaling_cur_freq"))
		if err != nil {
			return metrics.Result{}, err
		}
		if !ok {
			continue // no cpufreq for this core
		}
		c := cpufreqCore{Core: n, CurMHz: cur}
		if c.MinMHz, _, err = cpufreqReadMHz(env.FS, path.Join(dir, "scaling_min_freq")); err != nil {
			return metrics.Result{}, err
		}
		if c.MaxMHz, _, err = cpufreqReadMHz(env.FS, path.Join(dir, "scaling_max_freq")); err != nil {
			return metrics.Result{}, err
		}
		if c.Governor, _, err = cpufreqReadValue(env.FS, path.Join(dir, "scaling_governor")); err != nil {
			return metrics.Result{}, err
		}

		data.Cores = append(data.Cores, c)
		series[fmt.Sprintf("cpu.core%d.freq_mhz", n)] = cur
		sum += cur
	}

	if len(data.Cores) == 0 {
		return metrics.Result{Data: data}, nil
	}
	data.AvgMHz = sum / float64(len(data.Cores))
	series["cpu.freq_mhz"] = data.AvgMHz
	return metrics.Result{Data: data, Series: series}, nil
}

// cpufreqCoreNumbers returns the numbers of the cpu<N> directories under
// sys/devices/system/cpu in ascending order, skipping siblings such as
// cpufreq and cpuidle. A missing directory yields no cores.
func cpufreqCoreNumbers(fsys fs.FS) ([]int, error) {
	entries, err := fs.ReadDir(fsys, cpufreqSysDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var cores []int
	for _, e := range entries {
		num, ok := strings.CutPrefix(e.Name(), "cpu")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(num)
		if err != nil || n < 0 {
			continue
		}
		cores = append(cores, n)
	}
	sort.Ints(cores)
	return cores, nil
}

// cpufreqReadValue reads a single-value sysfs file. ok is false if it does not exist.
func cpufreqReadValue(fsys fs.FS, name string) (s string, ok bool, err error) {
	b, err := fs.ReadFile(fsys, name)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(string(b)), true, nil
}

// cpufreqReadMHz reads a sysfs frequency in kHz and returns it in MHz. ok is
// false if the file does not exist.
func cpufreqReadMHz(fsys fs.FS, name string) (mhz float64, ok bool, err error) {
	s, ok, err := cpufreqReadValue(fsys, name)
	if err != nil || !ok {
		return 0, ok, err
	}
	khz, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("%s: %w", name, err)
	}
	return float64(khz) / 1000, true, nil
}
