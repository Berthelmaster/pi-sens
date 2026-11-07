package collectors

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

func collectMemory(t *testing.T, fsys fstest.MapFS) (metrics.Result, error) {
	t.Helper()
	return memory{}.Collect(context.Background(), &metrics.Env{FS: fsys, Now: time.Now()})
}

func memoryFixture(t *testing.T, name string) (metrics.Result, memoryData) {
	t.Helper()
	env := &metrics.Env{FS: os.DirFS("testdata/memory/" + name), Now: time.Now()}
	res, err := memory{}.Collect(context.Background(), env)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	data, ok := res.Data.(memoryData)
	if !ok {
		t.Fatalf("Data is %T, want memoryData", res.Data)
	}
	return res, data
}

const kB = 1024

// testdata/memory/zram is a Pi 5 (8 GB) on Raspberry Pi OS with its default
// 2 GB zstd zram swap. zram1 exists but was never initialised.
func TestMemoryZram(t *testing.T) {
	res, data := memoryFixture(t, "zram")

	wantSeries := map[string]float64{
		"mem.used_bytes":      (8255824 - 4060800) * kB,
		"mem.used_pct":        50.81,
		"mem.available_bytes": 4060800 * kB,
		"mem.cached_bytes":    4085616 * kB,
		"swap.used_bytes":     (2097136 - 1628624) * kB,
		"swap.used_pct":       22.34,
	}
	if !reflect.DeepEqual(res.Series, wantSeries) {
		t.Errorf("Series = %v\nwant %v", res.Series, wantSeries)
	}

	want := memoryData{
		TotalBytes:     8255824 * kB,
		AvailableBytes: 4060800 * kB,
		UsedBytes:      (8255824 - 4060800) * kB,
		UsedPct:        50.81,
		FreeBytes:      426096 * kB,
		BuffersBytes:   181968 * kB,
		CachedBytes:    4085616 * kB,
		SharedBytes:    860480 * kB,
		Swap: swapData{
			TotalBytes: 2097136 * kB,
			FreeBytes:  1628624 * kB,
			UsedBytes:  (2097136 - 1628624) * kB,
			UsedPct:    22.34,
		},
		Zram: []zramDevice{{
			Device:           "zram0",
			Swap:             true,
			Algorithm:        "zstd",
			DiskSizeBytes:    2147483648,
			OrigDataBytes:    460881920,
			ComprDataBytes:   100483230,
			MemUsedBytes:     112328704,
			CompressionRatio: 4.59,
		}},
	}
	if !reflect.DeepEqual(data, want) {
		t.Errorf("Data = %+v\nwant %+v", data, want)
	}
}

// testdata/memory/swapfile is a 4 GB Pi on an older image that swaps to a
// 100 MB file (dphys-swapfile) and has no zram.
func TestMemorySwapFile(t *testing.T) {
	res, data := memoryFixture(t, "swapfile")

	wantSeries := map[string]float64{
		"mem.used_bytes":      971024 * kB,
		"mem.used_pct":        25,
		"mem.available_bytes": 2913072 * kB,
		"mem.cached_bytes":    2101248 * kB,
		"swap.used_bytes":     25600 * kB,
		"swap.used_pct":       25, // 25600 of 102396 kB
	}
	if !reflect.DeepEqual(res.Series, wantSeries) {
		t.Errorf("Series = %v\nwant %v", res.Series, wantSeries)
	}
	if len(data.Zram) != 0 {
		t.Errorf("Zram = %+v, want none", data.Zram)
	}
}

// testdata/memory/noswap has swap switched off: SwapTotal is 0 and there is
// no /sys/block at all.
func TestMemoryNoSwap(t *testing.T) {
	res, data := memoryFixture(t, "noswap")

	for _, key := range []string{"swap.used_bytes", "swap.used_pct"} {
		v, ok := res.Series[key]
		if !ok || v != 0 || math.IsNaN(v) {
			t.Errorf("%s = %v (present %v), want 0", key, v, ok)
		}
	}
	if data.Swap != (swapData{}) {
		t.Errorf("Swap = %+v, want all zero", data.Swap)
	}
	if res.Series["mem.used_pct"] != 25 {
		t.Errorf("mem.used_pct = %v, want 25", res.Series["mem.used_pct"])
	}

	b, err := json.Marshal(res.Data)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"zram":[]`) {
		t.Errorf("Data = %s, want an empty zram list", b)
	}
}

func TestMemoryOldKernelWithoutMemAvailable(t *testing.T) {
	res, err := collectMemory(t, fstest.MapFS{
		"proc/meminfo": {Data: []byte(
			"MemTotal:   1000 kB\n" +
				"MemFree:     100 kB\n" +
				"Buffers:      50 kB\n" +
				"Cached:      250 kB\n")},
	})
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if got := res.Series["mem.available_bytes"]; got != 400*kB {
		t.Errorf("mem.available_bytes = %v, want free + buffers + cached = %v", got, 400*kB)
	}
	if got := res.Series["mem.used_pct"]; got != 60 {
		t.Errorf("mem.used_pct = %v, want 60", got)
	}
}

func TestMemoryFreeAboveTotal(t *testing.T) {
	// The fields are not read atomically, so free can overtake total for a
	// moment. That must not wrap around.
	res, err := collectMemory(t, fstest.MapFS{
		"proc/meminfo": {Data: []byte(
			"MemTotal: 1000 kB\nMemFree: 900 kB\nMemAvailable: 1001 kB\n" +
				"SwapTotal: 100 kB\nSwapFree: 101 kB\n")},
	})
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	for _, key := range []string{"mem.used_bytes", "mem.used_pct", "swap.used_bytes", "swap.used_pct"} {
		if res.Series[key] != 0 {
			t.Errorf("%s = %v, want 0", key, res.Series[key])
		}
	}
}

func TestMemoryZramPartial(t *testing.T) {
	res, err := collectMemory(t, fstest.MapFS{
		"proc/meminfo":                    {Data: []byte("MemTotal: 1000 kB\nMemFree: 900 kB\n")},
		"sys/block/zram0/disksize":        {Data: []byte("1048576\n")},
		"sys/block/zram0/mm_stat":         {Data: []byte("0 0 4096 0 4096 0 0 0 0\n")},
		"sys/block/zram10/disksize":       {Data: []byte("2097152\n")},
		"sys/block/mmcblk0/size":          {Data: []byte("62333952\n")},
		"sys/block/zram10/comp_algorithm": {Data: []byte("lzo\n")}, // no brackets
	})
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	want := []zramDevice{
		{Device: "zram0", DiskSizeBytes: 1048576, MemUsedBytes: 4096}, // empty: no ratio
		{Device: "zram10", DiskSizeBytes: 2097152},
	}
	if got := res.Data.(memoryData).Zram; !reflect.DeepEqual(got, want) {
		t.Errorf("Zram = %+v\nwant %+v", got, want)
	}
}

func TestMemoryErrors(t *testing.T) {
	const ok = "MemTotal: 1000 kB\nMemFree: 900 kB\n"
	tests := map[string]fstest.MapFS{
		"no meminfo":    {},
		"no MemTotal":   {"proc/meminfo": {Data: []byte("MemFree: 900 kB\n")}},
		"garbage value": {"proc/meminfo": {Data: []byte("MemTotal: lots kB\nMemFree: 900 kB\n")}},
		"empty value":   {"proc/meminfo": {Data: []byte("MemTotal:\nMemFree: 900 kB\n")}},
		"garbage disksize": {
			"proc/meminfo":             {Data: []byte(ok)},
			"sys/block/zram0/disksize": {Data: []byte("big\n")},
		},
		"short mm_stat": {
			"proc/meminfo":             {Data: []byte(ok)},
			"sys/block/zram0/disksize": {Data: []byte("1048576\n")},
			"sys/block/zram0/mm_stat":  {Data: []byte("1 2\n")},
		},
	}
	for name, fsys := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := collectMemory(t, fsys); err == nil {
				t.Fatal("Collect() error = nil, want an error")
			}
		})
	}
}

func TestMemoryIsRegistered(t *testing.T) {
	for _, c := range metrics.Registered() {
		if c.Name() == "memory" {
			return
		}
	}
	t.Fatal("memory collector is not registered")
}
