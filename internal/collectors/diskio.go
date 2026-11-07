package collectors

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"math"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

func init() { metrics.Register(&diskio{}) }

// diskio reports read and write throughput, IOPS and utilisation of the
// physical disks from /proc/diskstats, plus the drive model from sysfs.
//
// Only whole physical devices are kept: SD cards and eMMC (mmcblk0), NVMe
// namespaces (nvme0n1) and SCSI/SATA/USB disks (sda), plus the virtio and
// Xen disks of a VM. Partitions are left out because their I/O is already
// counted in the disk, and so are loop, ram, zram, device-mapper and md
// devices, which sit on top of a real disk or in RAM.
//
// /proc/diskstats holds counters since boot, so the rates are the difference
// between two samples divided by the time between them (env.Prev to
// env.Now). The first sample, a device that just appeared and a device whose
// counters went backwards (removed and plugged back in, or a 32-bit counter
// wrapping) have no rates for that sample.
type diskio struct {
	prev map[string]diskioCounters // keyed by device name
}

// diskioSectorBytes is the unit of the sector columns in /proc/diskstats.
// The kernel always counts in 512-byte sectors, whatever the device's
// logical or physical block size is.
const diskioSectorBytes = 512

type diskioData struct {
	Devices []diskioDevice `json:"devices"`
}

type diskioDevice struct {
	Name string `json:"name"` // "mmcblk0", "nvme0n1", "sda"
	// Model is the drive model from sysfs ("Samsung SSD 980 500GB") or the
	// card name for SD and eMMC ("SD64G"); empty if the kernel has none.
	Model string `json:"model,omitempty"`
	// The totals since boot.
	ReadBytes  uint64 `json:"read_bytes"`
	WriteBytes uint64 `json:"write_bytes"`
	Reads      uint64 `json:"reads"`
	Writes     uint64 `json:"writes"`
	// Rates covers the time since the previous sample; omitted when there
	// is nothing to compare against.
	Rates *diskioRates `json:"rates,omitempty"`
}

type diskioRates struct {
	ReadBps   float64 `json:"read_bps"` // bytes per second
	WriteBps  float64 `json:"write_bps"`
	ReadIOPS  float64 `json:"read_iops"`
	WriteIOPS float64 `json:"write_iops"`
	// UtilPct is the share of the time the device had I/O in flight, capped
	// at 100.
	UtilPct float64 `json:"util_pct"`
}

// diskioCounters are the /proc/diskstats columns we use.
type diskioCounters struct {
	reads, readSectors, writes, writeSectors uint64
	ioTicks                                  uint64 // ms with I/O in flight
}

// diskioWholeDisk matches the names of whole physical disks. Partitions
// (sda1, mmcblk0p1, nvme0n1p1) and the eMMC boot and RPMB areas
// (mmcblk0boot0, mmcblk0rpmb) do not match.
var diskioWholeDisk = regexp.MustCompile(`^(?:(?:sd|hd|vd|xvd)[a-z]+|mmcblk[0-9]+|nvme[0-9]+n[0-9]+)$`)

func (*diskio) Name() string { return "diskio" }

func (d *diskio) Collect(_ context.Context, env *metrics.Env) (metrics.Result, error) {
	names, counters, err := diskioReadStats(env.FS)
	if err != nil {
		return metrics.Result{}, err
	}

	var elapsed float64 // seconds; 0 means no rates this time
	if !env.Prev.IsZero() && env.Now.After(env.Prev) {
		elapsed = env.Now.Sub(env.Prev).Seconds()
	}

	data := diskioData{Devices: make([]diskioDevice, 0, len(names))}
	series := map[string]float64{}
	for _, name := range names {
		cur := counters[name]
		dev := diskioDevice{
			Name:       name,
			Model:      diskioModel(env.FS, name),
			ReadBytes:  cur.readSectors * diskioSectorBytes,
			WriteBytes: cur.writeSectors * diskioSectorBytes,
			Reads:      cur.reads,
			Writes:     cur.writes,
		}
		if prev, ok := d.prev[name]; ok && elapsed > 0 {
			if r, ok := diskioRatesSince(prev, cur, elapsed); ok {
				dev.Rates = &r
				key := "disk." + name + "."
				series[key+"read_bps"] = r.ReadBps
				series[key+"write_bps"] = r.WriteBps
				series[key+"util_pct"] = r.UtilPct
			}
		}
		data.Devices = append(data.Devices, dev)
	}

	d.prev = counters
	if len(series) == 0 {
		series = nil
	}
	return metrics.Result{Data: data, Series: series}, nil
}

// diskioRatesSince returns the rates between two samples of the same device
// taken elapsed seconds apart. ok is false if any counter went backwards.
func diskioRatesSince(prev, cur diskioCounters, elapsed float64) (r diskioRates, ok bool) {
	if cur.reads < prev.reads || cur.readSectors < prev.readSectors ||
		cur.writes < prev.writes || cur.writeSectors < prev.writeSectors ||
		cur.ioTicks < prev.ioTicks {
		return diskioRates{}, false
	}
	perSec := func(v uint64) float64 { return float64(v) / elapsed }
	// io_ticks is only updated when I/O starts or ends, so on a busy device
	// it can run slightly ahead of the wall clock between two samples.
	util := math.Min(perSec(cur.ioTicks-prev.ioTicks)/1000*100, 100)
	return diskioRates{
		ReadBps:   math.Round(perSec((cur.readSectors - prev.readSectors) * diskioSectorBytes)),
		WriteBps:  math.Round(perSec((cur.writeSectors - prev.writeSectors) * diskioSectorBytes)),
		ReadIOPS:  round2(perSec(cur.reads - prev.reads)),
		WriteIOPS: round2(perSec(cur.writes - prev.writes)),
		UtilPct:   round2(util),
	}, true
}

// diskioReadStats parses the whole-disk lines of /proc/diskstats:
//
//	major minor name reads reads_merged sectors_read ms_reading
//	    writes writes_merged sectors_written ms_writing
//	    in_flight io_ticks time_in_queue [discard and flush columns]
//
// It returns the device names in file order and their counters. Lines of
// other devices are skipped without being parsed.
func diskioReadStats(fsys fs.FS) ([]string, map[string]diskioCounters, error) {
	b, err := fs.ReadFile(fsys, "proc/diskstats")
	if err != nil {
		return nil, nil, err
	}
	var names []string
	counters := map[string]diskioCounters{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 || !diskioWholeDisk.MatchString(fields[2]) {
			continue
		}
		name := fields[2]
		if len(fields) < 14 {
			return nil, nil, fmt.Errorf("proc/diskstats: %s: want at least 11 columns, got %d", name, len(fields)-3)
		}
		if _, dup := counters[name]; dup {
			return nil, nil, fmt.Errorf("proc/diskstats: %s listed twice", name)
		}
		var v [11]uint64
		for i := range v {
			if v[i], err = strconv.ParseUint(fields[i+3], 10, 64); err != nil {
				return nil, nil, fmt.Errorf("proc/diskstats: %s: %w", name, err)
			}
		}
		names = append(names, name)
		counters[name] = diskioCounters{
			reads:        v[0],
			readSectors:  v[2],
			writes:       v[4],
			writeSectors: v[6],
			ioTicks:      v[9],
		}
	}
	if err := sc.Err(); err != nil {
		return nil, nil, fmt.Errorf("proc/diskstats: %w", err)
	}
	return names, counters, nil
}

// diskioModel returns the drive model of a block device: device/model for
// SCSI, SATA, USB and NVMe, device/name for SD cards and eMMC. Empty if
// neither can be read.
func diskioModel(fsys fs.FS, dev string) string {
	dir := path.Join("sys/block", dev, "device")
	for _, f := range []string{"model", "name"} {
		if b, err := fs.ReadFile(fsys, path.Join(dir, f)); err == nil {
			if s := strings.TrimSpace(string(b)); s != "" {
				return s
			}
		}
	}
	return ""
}
