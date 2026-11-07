package collectors

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"reflect"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

var diskioT0 = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)

func collectDiskIO(t *testing.T, d *diskio, fsys fs.FS, prev, now time.Time) metrics.Result {
	t.Helper()
	res, err := d.Collect(context.Background(), &metrics.Env{FS: fsys, Prev: prev, Now: now})
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	return res
}

func diskioDataOf(t *testing.T, res metrics.Result) diskioData {
	t.Helper()
	data, ok := res.Data.(diskioData)
	if !ok {
		t.Fatalf("Data is %T, want diskioData", res.Data)
	}
	return data
}

// The fixtures are two /proc/diskstats snapshots of a Pi 5 taken 2 s apart,
// with an SD card, an NVMe drive and a USB SSD, plus partitions, loop, ram,
// zram and device-mapper devices that must all be skipped. A second USB disk
// (sdb) is plugged in between the two samples.
func TestDiskIO(t *testing.T) {
	d := &diskio{}

	first := collectDiskIO(t, d, os.DirFS("testdata/diskio/first"), time.Time{}, diskioT0)
	if len(first.Series) != 0 {
		t.Errorf("first Series = %v, want none without a previous sample", first.Series)
	}
	var names []string
	for _, dev := range diskioDataOf(t, first).Devices {
		names = append(names, dev.Name)
		if dev.Rates != nil {
			t.Errorf("first sample: %s has rates %+v, want none", dev.Name, dev.Rates)
		}
	}
	if want := []string{"mmcblk0", "nvme0n1", "sda"}; !reflect.DeepEqual(names, want) {
		t.Errorf("first devices = %v, want %v", names, want)
	}

	second := collectDiskIO(t, d, os.DirFS("testdata/diskio/second"), diskioT0, diskioT0.Add(2*time.Second))
	wantSeries := map[string]float64{
		// +80 sectors read, +600 written, io_ticks +1500 ms.
		"disk.mmcblk0.read_bps":  20480,
		"disk.mmcblk0.write_bps": 153600,
		"disk.mmcblk0.util_pct":  75,
		// The NVMe drive has 4096-byte logical blocks, but diskstats still
		// counts 512-byte sectors: +4096 read and +2048 written.
		"disk.nvme0n1.read_bps":  1048576,
		"disk.nvme0n1.write_bps": 524288,
		"disk.nvme0n1.util_pct":  25,
		// io_ticks ran 2010 ms in 2 s; capped at 100.
		"disk.sda.read_bps":  19660800,
		"disk.sda.write_bps": 0,
		"disk.sda.util_pct":  100,
	}
	if !reflect.DeepEqual(second.Series, wantSeries) {
		t.Errorf("second Series = %v\nwant %v", second.Series, wantSeries)
	}

	want := diskioData{Devices: []diskioDevice{
		{
			Name: "mmcblk0", Model: "SN64G",
			ReadBytes: 3145808 * 512, WriteBytes: 1049176 * 512, Reads: 52320, Writes: 21030,
			Rates: &diskioRates{ReadBps: 20480, WriteBps: 153600, ReadIOPS: 5, WriteIOPS: 15, UtilPct: 75},
		},
		{
			Name: "nvme0n1", Model: "Samsung SSD 980 500GB",
			ReadBytes: 804096 * 512, WriteBytes: 402048 * 512, Reads: 10400, Writes: 5100,
			Rates: &diskioRates{ReadBps: 1048576, WriteBps: 524288, ReadIOPS: 200, WriteIOPS: 50, UtilPct: 25},
		},
		{
			Name: "sda", Model: "Extreme Pro",
			ReadBytes: 384000 * 512, WriteBytes: 80 * 512, Reads: 1500, Writes: 10,
			Rates: &diskioRates{ReadBps: 19660800, ReadIOPS: 150, UtilPct: 100},
		},
		// New since the first sample, so no rates yet; no sysfs model either.
		{Name: "sdb", ReadBytes: 2400 * 512, Reads: 120},
	}}
	if got := diskioDataOf(t, second); !reflect.DeepEqual(got, want) {
		gj, _ := json.Marshal(got)
		wj, _ := json.Marshal(want)
		t.Errorf("second Data = %s\nwant %s", gj, wj)
	}
}

func diskioFS(stats string) fstest.MapFS {
	return fstest.MapFS{"proc/diskstats": {Data: []byte(stats)}}
}

func TestDiskIOCounterReset(t *testing.T) {
	d := &diskio{}
	collectDiskIO(t, d, diskioFS(
		"8 0 sda 1000 0 80000 0 500 0 40000 0 0 9000 0 0 0 0 0 0 0\n"+
			"8 16 sdb 100 0 800 0 0 0 0 0 0 100 0 0 0 0 0 0 0\n"),
		time.Time{}, diskioT0)

	// sda was unplugged and plugged back in, so its counters restarted.
	res := collectDiskIO(t, d, diskioFS(
		"8 0 sda 10 0 800 0 0 0 0 0 0 40 0 0 0 0 0 0 0\n"+
			"8 16 sdb 110 0 1824 0 0 0 0 0 0 200 0 0 0 0 0 0 0\n"),
		diskioT0, diskioT0.Add(time.Second))
	want := map[string]float64{
		"disk.sdb.read_bps":  524288,
		"disk.sdb.write_bps": 0,
		"disk.sdb.util_pct":  10,
	}
	if !reflect.DeepEqual(res.Series, want) {
		t.Errorf("Series = %v, want %v (no rates for sda)", res.Series, want)
	}

	// The restarted values are the new baseline.
	res = collectDiskIO(t, d, diskioFS(
		"8 0 sda 30 0 1824 0 4 0 64 0 0 540 0 0 0 0 0 0 0\n"+
			"8 16 sdb 110 0 1824 0 0 0 0 0 0 200 0 0 0 0 0 0 0\n"),
		diskioT0.Add(time.Second), diskioT0.Add(3*time.Second))
	data := diskioDataOf(t, res)
	wantRates := diskioRates{ReadBps: 262144, WriteBps: 16384, ReadIOPS: 10, WriteIOPS: 2, UtilPct: 25}
	if r := data.Devices[0].Rates; r == nil || *r != wantRates {
		t.Errorf("sda Rates = %+v, want %+v", r, wantRates)
	}
}

func TestDiskIONoElapsedTime(t *testing.T) {
	d := &diskio{}
	stats := diskioFS("259 0 nvme0n1 1 0 8 0 1 0 8 0 0 1 0\n") // old kernel: 11 columns
	collectDiskIO(t, d, stats, time.Time{}, diskioT0)
	if res := collectDiskIO(t, d, stats, diskioT0, diskioT0); len(res.Series) != 0 {
		t.Errorf("Series = %v with no time between samples, want none", res.Series)
	}
}

func TestDiskIOWholeDisks(t *testing.T) {
	keep := []string{"sda", "sdz", "sdaa", "mmcblk0", "mmcblk1", "nvme0n1", "nvme1n2", "vda", "xvdb", "hda"}
	skip := []string{
		"sda1", "sdaa12", "mmcblk0p1", "mmcblk0p10", "mmcblk0boot0", "mmcblk0rpmb",
		"nvme0n1p1", "nvme0", "nvme0c0n1", "loop0", "ram0", "zram0", "dm-0", "md0",
		"sr0", "fd0", "mtdblock0", "nbd0", "sd", "mmcblk",
	}
	for _, n := range keep {
		if !diskioWholeDisk.MatchString(n) {
			t.Errorf("%s is skipped, want it kept", n)
		}
	}
	for _, n := range skip {
		if diskioWholeDisk.MatchString(n) {
			t.Errorf("%s is kept, want it skipped", n)
		}
	}
}

func TestDiskIOEmpty(t *testing.T) {
	res := collectDiskIO(t, &diskio{}, diskioFS("7 0 loop0 1 0 8 0 0 0 0 0 0 1 0 0 0 0 0 0 0\n"), time.Time{}, diskioT0)
	if len(res.Series) != 0 {
		t.Errorf("Series = %v, want none", res.Series)
	}
	b, err := json.Marshal(res.Data)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); got != `{"devices":[]}` {
		t.Errorf("Data = %s, want {\"devices\":[]}", got)
	}
}

func TestDiskIOErrors(t *testing.T) {
	tests := map[string]fs.FS{
		"no diskstats": fstest.MapFS{},
		"short line":   diskioFS("8 0 sda 1 0 8 0 1 0 8 0 0 1\n"),
		"garbage":      diskioFS("8 0 sda 1 0 x 0 1 0 8 0 0 1 0 0 0 0 0 0 0\n"),
		"negative":     diskioFS("8 0 sda 1 0 -8 0 1 0 8 0 0 1 0 0 0 0 0 0 0\n"),
		"listed twice": diskioFS("8 0 sda 1 0 8 0 1 0 8 0 0 1 0\n8 0 sda 1 0 8 0 1 0 8 0 0 1 0\n"),
	}
	for name, fsys := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := (&diskio{}).Collect(context.Background(), &metrics.Env{FS: fsys, Now: diskioT0}); err == nil {
				t.Fatal("Collect() error = nil, want an error")
			}
		})
	}
	// Bad lines for devices that are skipped anyway do not matter.
	if _, err := (&diskio{}).Collect(context.Background(), &metrics.Env{FS: diskioFS("7 0 loop0 x\n8 1 sda1\n"), Now: diskioT0}); err != nil {
		t.Errorf("Collect() error = %v for bad lines of skipped devices, want none", err)
	}
}

func TestDiskIOIsRegistered(t *testing.T) {
	for _, c := range metrics.Registered() {
		if c.Name() == "diskio" {
			return
		}
	}
	t.Fatal("diskio collector is not registered")
}
