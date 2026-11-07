package collectors

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

func TestCPUFreq(t *testing.T) {
	env := &metrics.Env{FS: os.DirFS("testdata/cpufreq/pi5"), Now: time.Now()}

	res, err := cpufreq{}.Collect(context.Background(), env)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	wantSeries := map[string]float64{
		"cpu.freq_mhz":       2025,
		"cpu.core0.freq_mhz": 2400,
		"cpu.core1.freq_mhz": 1500,
		"cpu.core2.freq_mhz": 2400,
		"cpu.core3.freq_mhz": 1800,
	}
	if !reflect.DeepEqual(res.Series, wantSeries) {
		t.Errorf("Series = %v, want %v", res.Series, wantSeries)
	}

	data, ok := res.Data.(cpufreqData)
	if !ok {
		t.Fatalf("Data is %T, want cpufreqData", res.Data)
	}
	want := cpufreqData{
		AvgMHz: 2025,
		Cores: []cpufreqCore{
			{Core: 0, CurMHz: 2400, MinMHz: 1500, MaxMHz: 2400, Governor: "ondemand"},
			{Core: 1, CurMHz: 1500, MinMHz: 1500, MaxMHz: 2400, Governor: "ondemand"},
			{Core: 2, CurMHz: 2400, MinMHz: 1500, MaxMHz: 2400, Governor: "performance"},
			{Core: 3, CurMHz: 1800, MinMHz: 1500, MaxMHz: 2400, Governor: "performance"},
		},
	}
	if !reflect.DeepEqual(data, want) {
		t.Errorf("Data = %+v, want %+v", data, want)
	}
}

func TestCPUFreqMissing(t *testing.T) {
	tests := map[string]metrics.Env{
		"no cpufreq dirs": {FS: os.DirFS("testdata/cpufreq/missing")},
		"no cpu dir":      {FS: fstest.MapFS{}},
	}
	for name, env := range tests {
		t.Run(name, func(t *testing.T) {
			res, err := cpufreq{}.Collect(context.Background(), &env)
			if err != nil {
				t.Fatalf("Collect() error = %v, want an empty section", err)
			}
			if len(res.Series) != 0 {
				t.Errorf("Series = %v, want none", res.Series)
			}
			b, err := json.Marshal(res.Data)
			if err != nil {
				t.Fatal(err)
			}
			if got := string(b); got != `{"cores":[]}` {
				t.Errorf("Data = %s, want {\"cores\":[]}", got)
			}
		})
	}
}

func TestCPUFreqPartial(t *testing.T) {
	// cpu1 has no cpufreq (e.g. offline), cpu10 only reports its current
	// frequency. Cores are ordered numerically, not lexically.
	fsys := fstest.MapFS{
		"sys/devices/system/cpu/cpu0/cpufreq/scaling_cur_freq":  {Data: []byte("600000\n")},
		"sys/devices/system/cpu/cpu0/cpufreq/scaling_governor":  {Data: []byte("schedutil\n")},
		"sys/devices/system/cpu/cpu1/topology/core_id":          {Data: []byte("1\n")},
		"sys/devices/system/cpu/cpu10/cpufreq/scaling_cur_freq": {Data: []byte("1200000\n")},
		"sys/devices/system/cpu/cpu2/cpufreq/scaling_cur_freq":  {Data: []byte("900500\n")},
	}
	res, err := cpufreq{}.Collect(context.Background(), &metrics.Env{FS: fsys})
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	wantSeries := map[string]float64{
		"cpu.freq_mhz":        (600 + 900.5 + 1200) / 3.0,
		"cpu.core0.freq_mhz":  600,
		"cpu.core2.freq_mhz":  900.5,
		"cpu.core10.freq_mhz": 1200,
	}
	if !reflect.DeepEqual(res.Series, wantSeries) {
		t.Errorf("Series = %v, want %v", res.Series, wantSeries)
	}
	data := res.Data.(cpufreqData)
	wantCores := []cpufreqCore{
		{Core: 0, CurMHz: 600, Governor: "schedutil"},
		{Core: 2, CurMHz: 900.5},
		{Core: 10, CurMHz: 1200},
	}
	if !reflect.DeepEqual(data.Cores, wantCores) {
		t.Errorf("Cores = %+v, want %+v", data.Cores, wantCores)
	}
}

func TestCPUFreqErrors(t *testing.T) {
	const dir = "sys/devices/system/cpu/cpu0/cpufreq/"
	tests := map[string]fstest.MapFS{
		"garbage cur": {dir + "scaling_cur_freq": {Data: []byte("<unknown>\n")}},
		"garbage max": {
			dir + "scaling_cur_freq": {Data: []byte("600000\n")},
			dir + "scaling_max_freq": {Data: []byte("fast\n")},
		},
	}
	for name, fsys := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := (cpufreq{}).Collect(context.Background(), &metrics.Env{FS: fsys}); err == nil {
				t.Fatal("Collect() error = nil, want an error")
			}
		})
	}
}

func TestCPUFreqIsRegistered(t *testing.T) {
	for _, c := range metrics.Registered() {
		if c.Name() == "cpufreq" {
			return
		}
	}
	t.Fatal("cpufreq collector is not registered")
}
