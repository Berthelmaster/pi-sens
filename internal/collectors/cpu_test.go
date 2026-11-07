package collectors

import (
	"context"
	"io/fs"
	"os"
	"reflect"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

func collectCPU(t *testing.T, c *cpu, fsys fs.FS) metrics.Result {
	t.Helper()
	env := &metrics.Env{FS: fsys, Now: time.Now()}
	res, err := c.Collect(context.Background(), env)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	return res
}

func cpuDataOf(t *testing.T, res metrics.Result) cpuData {
	t.Helper()
	data, ok := res.Data.(cpuData)
	if !ok {
		t.Fatalf("Data is %T, want cpuData", res.Data)
	}
	return data
}

// The fixtures are two consecutive /proc/stat snapshots of a 4-core Pi with
// 100 jiffies per core in between; see the comments below for the split.
func TestCPU(t *testing.T) {
	c := &cpu{}

	first := collectCPU(t, c, os.DirFS("testdata/cpu/first"))
	wantFirst := map[string]float64{"load.1": 2.27, "load.5": 4.11, "load.15": 3.90}
	if !reflect.DeepEqual(first.Series, wantFirst) {
		t.Errorf("first Series = %v, want only the load average %v", first.Series, wantFirst)
	}
	if d := cpuDataOf(t, first); d.Cores != 4 || d.Usage != nil || d.PerCore != nil {
		t.Errorf("first Data = %+v, want 4 cores and no usage", d)
	}

	second := collectCPU(t, c, os.DirFS("testdata/cpu/second"))
	wantSeries := map[string]float64{
		"cpu.usage_pct":       36.25, // 145 of 400 jiffies
		"cpu.iowait_pct":      5,
		"cpu.core0.usage_pct": 75, // guest time is part of user, not counted twice
		"cpu.core1.usage_pct": 15,
		"cpu.core2.usage_pct": 35,
		"cpu.core3.usage_pct": 20, // 10 of it steal
		"load.1":              2.10,
		"load.5":              4.02,
		"load.15":             3.88,
	}
	if !reflect.DeepEqual(second.Series, wantSeries) {
		t.Errorf("second Series = %v\nwant %v", second.Series, wantSeries)
	}

	data := cpuDataOf(t, second)
	wantTotal := cpuUsage{
		UsagePct: 36.25, UserPct: 23.75, SystemPct: 7.5, IOWaitPct: 5,
		IRQPct: 2.5, StealPct: 2.5, IdlePct: 58.75,
	}
	if data.Usage == nil || *data.Usage != wantTotal {
		t.Errorf("Usage = %+v, want %+v", data.Usage, wantTotal)
	}
	wantCores := []cpuCore{
		{0, cpuUsage{UsagePct: 75, UserPct: 60, SystemPct: 10, IOWaitPct: 5, IRQPct: 5, IdlePct: 20}},
		{1, cpuUsage{UsagePct: 15, UserPct: 10, SystemPct: 5, IdlePct: 85}},
		{2, cpuUsage{UsagePct: 35, UserPct: 20, SystemPct: 10, IOWaitPct: 15, IRQPct: 5, IdlePct: 50}},
		{3, cpuUsage{UsagePct: 20, UserPct: 5, SystemPct: 5, StealPct: 10, IdlePct: 80}},
	}
	if !reflect.DeepEqual(data.PerCore, wantCores) {
		t.Errorf("PerCore = %+v\nwant %+v", data.PerCore, wantCores)
	}
	wantLoad := loadAvg{Load1: 2.10, Load5: 4.02, Load15: 3.88, RunningTasks: 3, TotalTasks: 577}
	if data.Cores != 4 || data.Load != wantLoad {
		t.Errorf("Cores = %d, Load = %+v, want 4 and %+v", data.Cores, data.Load, wantLoad)
	}
}

func cpuFS(stat string) fstest.MapFS {
	return fstest.MapFS{
		"proc/stat":    {Data: []byte(stat)},
		"proc/loadavg": {Data: []byte("0.00 0.01 0.05 1/100 1234\n")},
	}
}

func TestCPUCounterReset(t *testing.T) {
	c := &cpu{}
	collectCPU(t, c, cpuFS(
		"cpu  2000 0 1000 7000 0 0 0 0\n"+
			"cpu0 1000 0 500 3500 0 0 0 0\n"+
			"cpu1 1000 0 500 3500 0 0 0 0\n"))

	// cpu1 restarted from zero (offlined and back), which also takes the
	// aggregate line backwards.
	res := collectCPU(t, c, cpuFS(
		"cpu  1150 0 560 3790 0 0 0 0\n"+
			"cpu0 1100 0 550 3700 0 0 0 0\n"+
			"cpu1 50 0 10 90 0 0 0 0\n"))
	for k, v := range res.Series {
		if v < 0 {
			t.Errorf("%s = %v, want no negative values", k, v)
		}
	}
	if _, ok := res.Series["cpu.usage_pct"]; ok {
		t.Errorf("cpu.usage_pct = %v after the total went backwards, want it omitted", res.Series["cpu.usage_pct"])
	}
	if _, ok := res.Series["cpu.core1.usage_pct"]; ok {
		t.Errorf("cpu.core1.usage_pct reported after a reset, want it omitted")
	}
	if got := res.Series["cpu.core0.usage_pct"]; got != 42.86 { // 150 of 350
		t.Errorf("cpu.core0.usage_pct = %v, want 42.86", got)
	}

	// The reset values are the new baseline.
	res = collectCPU(t, c, cpuFS(
		"cpu  1250 0 560 3885 5 0 0 0\n"+
			"cpu0 1150 0 550 3745 5 0 0 0\n"+
			"cpu1 100 0 10 140 0 0 0 0\n"))
	want := map[string]float64{"cpu.usage_pct": 50, "cpu.core0.usage_pct": 50, "cpu.core1.usage_pct": 50}
	for k, v := range want {
		if res.Series[k] != v {
			t.Errorf("%s = %v, want %v", k, res.Series[k], v)
		}
	}
}

func TestCPUIOWaitGoesBackwards(t *testing.T) {
	c := &cpu{}
	collectCPU(t, c, cpuFS("cpu  100 0 100 700 100 0 0 0\n"))
	res := collectCPU(t, c, cpuFS("cpu  150 0 150 800 90 0 0 0\n"))
	d := cpuDataOf(t, res)
	want := cpuUsage{UsagePct: 50, UserPct: 25, SystemPct: 25, IdlePct: 50}
	if d.Usage == nil || *d.Usage != want {
		t.Errorf("Usage = %+v, want %+v", d.Usage, want)
	}
}

func TestCPUOfflineCores(t *testing.T) {
	res := collectCPU(t, &cpu{}, cpuFS(
		"cpu  2 0 0 2 0 0 0 0\n"+
			"cpu0 1 0 0 1 0 0 0 0\n"+
			"cpu3 1 0 0 1 0 0 0 0\n"))
	if d := cpuDataOf(t, res); d.Cores != 2 {
		t.Errorf("Cores = %d, want 2", d.Cores)
	}
}

func TestCPUOldKernelColumns(t *testing.T) {
	c := &cpu{}
	collectCPU(t, c, cpuFS("cpu  100 0 100 800\n"))
	res := collectCPU(t, c, cpuFS("cpu  150 0 150 900\n"))
	if got := res.Series["cpu.usage_pct"]; got != 50 {
		t.Errorf("cpu.usage_pct = %v, want 50", got)
	}
}

func TestCPUErrors(t *testing.T) {
	const stat = "cpu  1 2 3 4 5 6 7 8 0 0\n"
	tests := map[string]fstest.MapFS{
		"missing stat":      {"proc/loadavg": {Data: []byte("0.00 0.01 0.05 1/100 1234\n")}},
		"missing loadavg":   {"proc/stat": {Data: []byte(stat)}},
		"no cpu line":       cpuFS("intr 1 2 3\n"),
		"too few columns":   cpuFS("cpu  1 2 3\n"),
		"garbage counter":   cpuFS("cpu  1 2 x 4\n"),
		"bad cpu line name": cpuFS(stat + "cpuX 1 2 3 4\n"),
		"short loadavg": {
			"proc/stat":    {Data: []byte(stat)},
			"proc/loadavg": {Data: []byte("0.00 0.01\n")},
		},
		"bad load": {
			"proc/stat":    {Data: []byte(stat)},
			"proc/loadavg": {Data: []byte("abc 0.01 0.05 1/100 1234\n")},
		},
		"bad tasks": {
			"proc/stat":    {Data: []byte(stat)},
			"proc/loadavg": {Data: []byte("0.00 0.01 0.05 100 1234\n")},
		},
	}
	for name, fsys := range tests {
		t.Run(name, func(t *testing.T) {
			env := &metrics.Env{FS: fsys, Now: time.Now()}
			if _, err := (&cpu{}).Collect(context.Background(), env); err == nil {
				t.Fatal("Collect() error = nil, want an error")
			}
		})
	}
}

func TestCPUIsRegistered(t *testing.T) {
	for _, c := range metrics.Registered() {
		if c.Name() == "cpu" {
			return
		}
	}
	t.Fatal("cpu collector is not registered")
}
