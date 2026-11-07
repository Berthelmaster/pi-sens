package collectors

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

func init() { metrics.Register(&cpu{}) }

// cpu reports CPU usage (total and per core) from /proc/stat and the load
// average from /proc/loadavg.
//
// /proc/stat holds cumulative jiffies since boot, so usage is the difference
// between two samples. The first sample has nothing to compare against and
// omits usage; so does a CPU whose counters went backwards (counter reset,
// e.g. after a core went offline and came back).
type cpu struct {
	prev map[string]cpuTimes // keyed by the /proc/stat line name: "cpu", "cpu0", ...
}

type cpuData struct {
	Cores   int       `json:"cores"`
	Usage   *cpuUsage `json:"usage,omitempty"`
	PerCore []cpuCore `json:"per_core,omitempty"`
	Load    loadAvg   `json:"load"`
}

// cpuUsage splits the time since the previous sample into percentages that
// add up to 100. UsagePct is everything except idle and iowait.
type cpuUsage struct {
	UsagePct  float64 `json:"usage_pct"`
	UserPct   float64 `json:"user_pct"` // user + nice, guest time included
	SystemPct float64 `json:"system_pct"`
	IOWaitPct float64 `json:"iowait_pct"`
	IRQPct    float64 `json:"irq_pct"` // irq + softirq
	StealPct  float64 `json:"steal_pct"`
	IdlePct   float64 `json:"idle_pct"`
}

type cpuCore struct {
	Core int `json:"core"`
	cpuUsage
}

type loadAvg struct {
	Load1        float64 `json:"load1"`
	Load5        float64 `json:"load5"`
	Load15       float64 `json:"load15"`
	RunningTasks int     `json:"running_tasks"`
	TotalTasks   int     `json:"total_tasks"`
}

// cpuTimes are the /proc/stat columns we use, in jiffies. Guest and
// guest_nice are left out because the kernel already counts them in user and
// nice.
type cpuTimes struct {
	user, nice, system, idle, iowait, irq, softirq, steal uint64
}

func (t cpuTimes) total() uint64 {
	return t.user + t.nice + t.system + t.idle + t.iowait + t.irq + t.softirq + t.steal
}

func (*cpu) Name() string { return "cpu" }

func (c *cpu) Collect(_ context.Context, env *metrics.Env) (metrics.Result, error) {
	times, err := readProcStat(env.FS)
	if err != nil {
		return metrics.Result{}, err
	}
	load, err := readLoadAvg(env.FS)
	if err != nil {
		return metrics.Result{}, err
	}

	data := cpuData{Load: load}
	series := map[string]float64{
		"load.1":  load.Load1,
		"load.5":  load.Load5,
		"load.15": load.Load15,
	}

	if u, ok := usageSince(c.prev["cpu"], times["cpu"]); ok {
		data.Usage = &u
		series["cpu.usage_pct"] = u.UsagePct
		series["cpu.iowait_pct"] = u.IOWaitPct
	}
	for _, n := range coreNumbers(times) {
		name := "cpu" + strconv.Itoa(n)
		data.Cores++
		if u, ok := usageSince(c.prev[name], times[name]); ok {
			data.PerCore = append(data.PerCore, cpuCore{Core: n, cpuUsage: u})
			series[fmt.Sprintf("cpu.core%d.usage_pct", n)] = u.UsagePct
		}
	}

	c.prev = times
	return metrics.Result{Data: data, Series: series}, nil
}

// usageSince returns the usage between two samples of the same CPU. ok is
// false when there is no previous sample, no time has passed or the counters
// were reset.
func usageSince(prev, cur cpuTimes) (u cpuUsage, ok bool) {
	if prev == (cpuTimes{}) || cur.total() <= prev.total() {
		return cpuUsage{}, false
	}
	// A single counter can still go backwards without a reset: the kernel's
	// per-CPU iowait is known to do that. Count it as no time rather than
	// letting it go negative.
	d := cpuTimes{
		user:    delta(prev.user, cur.user),
		nice:    delta(prev.nice, cur.nice),
		system:  delta(prev.system, cur.system),
		idle:    delta(prev.idle, cur.idle),
		iowait:  delta(prev.iowait, cur.iowait),
		irq:     delta(prev.irq, cur.irq),
		softirq: delta(prev.softirq, cur.softirq),
		steal:   delta(prev.steal, cur.steal),
	}
	total := float64(d.total())
	if total == 0 {
		return cpuUsage{}, false
	}
	pct := func(v uint64) float64 { return round2(float64(v) / total * 100) }
	return cpuUsage{
		UsagePct:  pct(d.total() - d.idle - d.iowait),
		UserPct:   pct(d.user + d.nice),
		SystemPct: pct(d.system),
		IOWaitPct: pct(d.iowait),
		IRQPct:    pct(d.irq + d.softirq),
		StealPct:  pct(d.steal),
		IdlePct:   pct(d.idle),
	}, true
}

// coreNumbers returns the numbers of the cores in times, sorted. Offline
// cores are missing from /proc/stat, so there can be gaps.
func coreNumbers(times map[string]cpuTimes) []int {
	var cores []int
	for name := range times {
		if n, err := strconv.Atoi(strings.TrimPrefix(name, "cpu")); err == nil {
			cores = append(cores, n)
		}
	}
	sort.Ints(cores)
	return cores
}

func delta(prev, cur uint64) uint64 {
	if cur < prev {
		return 0
	}
	return cur - prev
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// readProcStat parses the cpu lines of /proc/stat:
//
//	cpu  user nice system idle iowait irq softirq steal guest guest_nice
//	cpu0 ...
//
// Older kernels have fewer columns; missing ones are zero.
func readProcStat(fsys fs.FS) (map[string]cpuTimes, error) {
	b, err := fs.ReadFile(fsys, "proc/stat")
	if err != nil {
		return nil, err
	}
	times := map[string]cpuTimes{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024) // the intr line can be long
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 || !strings.HasPrefix(fields[0], "cpu") {
			continue
		}
		name := fields[0]
		if name != "cpu" {
			if _, err := strconv.Atoi(strings.TrimPrefix(name, "cpu")); err != nil {
				return nil, fmt.Errorf("proc/stat: unexpected line %q", name)
			}
		}
		if len(fields) < 5 {
			return nil, fmt.Errorf("proc/stat: %s: want at least 4 columns, got %d", name, len(fields)-1)
		}
		var v [8]uint64
		for i := range v {
			if i+1 >= len(fields) {
				break
			}
			v[i], err = strconv.ParseUint(fields[i+1], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("proc/stat: %s: %w", name, err)
			}
		}
		times[name] = cpuTimes{
			user: v[0], nice: v[1], system: v[2], idle: v[3],
			iowait: v[4], irq: v[5], softirq: v[6], steal: v[7],
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("proc/stat: %w", err)
	}
	if _, ok := times["cpu"]; !ok {
		return nil, fmt.Errorf("proc/stat: no cpu line")
	}
	return times, nil
}

// readLoadAvg parses /proc/loadavg: "0.52 0.58 0.59 2/575 541274".
func readLoadAvg(fsys fs.FS) (loadAvg, error) {
	b, err := fs.ReadFile(fsys, "proc/loadavg")
	if err != nil {
		return loadAvg{}, err
	}
	fields := strings.Fields(string(b))
	if len(fields) < 4 {
		return loadAvg{}, fmt.Errorf("proc/loadavg: want at least 4 fields, got %d", len(fields))
	}
	var l loadAvg
	for i, dst := range []*float64{&l.Load1, &l.Load5, &l.Load15} {
		if *dst, err = strconv.ParseFloat(fields[i], 64); err != nil {
			return loadAvg{}, fmt.Errorf("proc/loadavg: %w", err)
		}
	}
	running, total, ok := strings.Cut(fields[3], "/")
	if !ok {
		return loadAvg{}, fmt.Errorf("proc/loadavg: bad task field %q", fields[3])
	}
	if l.RunningTasks, err = strconv.Atoi(running); err != nil {
		return loadAvg{}, fmt.Errorf("proc/loadavg: %w", err)
	}
	if l.TotalTasks, err = strconv.Atoi(total); err != nil {
		return loadAvg{}, fmt.Errorf("proc/loadavg: %w", err)
	}
	return l, nil
}
