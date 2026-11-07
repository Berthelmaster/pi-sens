package collectors

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"strings"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

func init() { metrics.Register(memory{}) }

// memory reports RAM and swap usage from /proc/meminfo, plus the zram devices
// under /sys/block. Raspberry Pi OS swaps to zram by default on recent images,
// so swap usage alone does not say how much RAM the swap really takes; the
// zram section shows the compressed size next to the original.
type memory struct{}

type memoryData struct {
	TotalBytes     uint64   `json:"total_bytes"`
	AvailableBytes uint64   `json:"available_bytes"`
	UsedBytes      uint64   `json:"used_bytes"` // total - available
	UsedPct        float64  `json:"used_pct"`
	FreeBytes      uint64   `json:"free_bytes"`
	BuffersBytes   uint64   `json:"buffers_bytes"`
	CachedBytes    uint64   `json:"cached_bytes"`
	SharedBytes    uint64   `json:"shared_bytes"`
	Swap           swapData `json:"swap"`
	// Zram lists the initialised zram devices, swap or not. Empty when there
	// are none.
	Zram []zramDevice `json:"zram"`
}

type swapData struct {
	TotalBytes uint64 `json:"total_bytes"`
	FreeBytes  uint64 `json:"free_bytes"`
	UsedBytes  uint64 `json:"used_bytes"`
	// UsedPct is 0 when there is no swap.
	UsedPct float64 `json:"used_pct"`
}

type zramDevice struct {
	Device string `json:"device"` // "zram0"
	// Swap is true when the device is listed in /proc/swaps.
	Swap          bool   `json:"swap"`
	Algorithm     string `json:"algorithm,omitempty"`
	DiskSizeBytes uint64 `json:"disksize_bytes"`
	// OrigDataBytes is the data stored before compression, ComprDataBytes
	// after, and MemUsedBytes the RAM the device takes including allocator
	// overhead. All three are 0 if the kernel has no mm_stat.
	OrigDataBytes  uint64 `json:"orig_data_bytes"`
	ComprDataBytes uint64 `json:"compr_data_bytes"`
	MemUsedBytes   uint64 `json:"mem_used_bytes"`
	// CompressionRatio is orig / compressed; omitted while the device is empty.
	CompressionRatio float64 `json:"compression_ratio,omitempty"`
}

func (memory) Name() string { return "memory" }

func (memory) Collect(_ context.Context, env *metrics.Env) (metrics.Result, error) {
	info, err := readMeminfo(env.FS)
	if err != nil {
		return metrics.Result{}, err
	}
	zram, err := readZram(env.FS)
	if err != nil {
		return metrics.Result{}, err
	}

	d := memoryData{
		TotalBytes:     info["MemTotal"],
		AvailableBytes: info["MemAvailable"],
		FreeBytes:      info["MemFree"],
		BuffersBytes:   info["Buffers"],
		CachedBytes:    info["Cached"],
		SharedBytes:    info["Shmem"],
		Zram:           zram,
	}
	d.UsedBytes = subClamp(d.TotalBytes, d.AvailableBytes)
	d.UsedPct = pctOf(d.UsedBytes, d.TotalBytes)

	d.Swap.TotalBytes = info["SwapTotal"]
	d.Swap.FreeBytes = info["SwapFree"]
	d.Swap.UsedBytes = subClamp(d.Swap.TotalBytes, d.Swap.FreeBytes)
	d.Swap.UsedPct = pctOf(d.Swap.UsedBytes, d.Swap.TotalBytes)

	return metrics.Result{
		Data: d,
		Series: map[string]float64{
			"mem.used_bytes":      float64(d.UsedBytes),
			"mem.used_pct":        d.UsedPct,
			"mem.available_bytes": float64(d.AvailableBytes),
			"mem.cached_bytes":    float64(d.CachedBytes),
			"swap.used_bytes":     float64(d.Swap.UsedBytes),
			"swap.used_pct":       d.Swap.UsedPct,
		},
	}, nil
}

// subClamp returns a - b, or 0 if b > a. The meminfo fields are not read
// atomically, so free can briefly exceed total.
func subClamp(a, b uint64) uint64 {
	if b > a {
		return 0
	}
	return a - b
}

// pctOf returns part as a percentage of total, rounded to two decimals, and 0
// when total is 0 (no swap configured).
func pctOf(part, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return round2(float64(part) / float64(total) * 100)
}

// readMeminfo parses /proc/meminfo into bytes:
//
//	MemTotal:        8255824 kB
//	HugePages_Total:       0
//
// Values with a kB unit are converted to bytes, others are kept as they are.
// MemTotal and MemFree are required. Kernels before 3.14 have no
// MemAvailable; it is then estimated as free + buffers + cached.
func readMeminfo(fsys fs.FS) (map[string]uint64, error) {
	b, err := fs.ReadFile(fsys, "proc/meminfo")
	if err != nil {
		return nil, err
	}
	info := map[string]uint64{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		key, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			return nil, fmt.Errorf("proc/meminfo: %s: no value", key)
		}
		v, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("proc/meminfo: %s: %w", key, err)
		}
		if len(fields) > 1 && fields[1] == "kB" {
			v *= 1024
		}
		info[key] = v
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("proc/meminfo: %w", err)
	}
	for _, key := range []string{"MemTotal", "MemFree"} {
		if _, ok := info[key]; !ok {
			return nil, fmt.Errorf("proc/meminfo: no %s", key)
		}
	}
	if _, ok := info["MemAvailable"]; !ok {
		info["MemAvailable"] = info["MemFree"] + info["Buffers"] + info["Cached"]
	}
	return info, nil
}

// readZram returns the zram devices in /sys/block that have a disk size set.
// Uninitialised devices (disksize 0) are skipped, and a missing /sys/block
// yields none.
func readZram(fsys fs.FS) ([]zramDevice, error) {
	devices := []zramDevice{}
	entries, err := fs.ReadDir(fsys, "sys/block")
	if errors.Is(err, fs.ErrNotExist) {
		return devices, nil
	}
	if err != nil {
		return nil, err
	}
	swaps, err := readSwapDevices(fsys)
	if err != nil {
		return nil, err
	}

	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "zram") {
			continue
		}
		dir := path.Join("sys/block", name)
		size, ok, err := zramReadValue(fsys, path.Join(dir, "disksize"))
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		dev := zramDevice{Device: name, Swap: swaps["/dev/"+name]}
		if dev.DiskSizeBytes, err = strconv.ParseUint(size, 10, 64); err != nil {
			return nil, fmt.Errorf("%s/disksize: %w", dir, err)
		}
		if dev.DiskSizeBytes == 0 {
			continue
		}

		algs, _, err := zramReadValue(fsys, path.Join(dir, "comp_algorithm"))
		if err != nil {
			return nil, err
		}
		dev.Algorithm = selectedZramAlgorithm(algs)

		// mm_stat: orig_data_size compr_data_size mem_used_total mem_limit
		// mem_used_max same_pages pages_compacted huge_pages huge_pages_since
		stat, ok, err := zramReadValue(fsys, path.Join(dir, "mm_stat"))
		if err != nil {
			return nil, err
		}
		if ok {
			fields := strings.Fields(stat)
			if len(fields) < 3 {
				return nil, fmt.Errorf("%s/mm_stat: want at least 3 fields, got %d", dir, len(fields))
			}
			for i, dst := range []*uint64{&dev.OrigDataBytes, &dev.ComprDataBytes, &dev.MemUsedBytes} {
				if *dst, err = strconv.ParseUint(fields[i], 10, 64); err != nil {
					return nil, fmt.Errorf("%s/mm_stat: %w", dir, err)
				}
			}
			if dev.ComprDataBytes > 0 {
				dev.CompressionRatio = round2(float64(dev.OrigDataBytes) / float64(dev.ComprDataBytes))
			}
		}
		devices = append(devices, dev)
	}
	return devices, nil
}

// zramReadValue reads a single-value sysfs file. ok is false if it does not
// exist.
func zramReadValue(fsys fs.FS, name string) (s string, ok bool, err error) {
	b, err := fs.ReadFile(fsys, name)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(string(b)), true, nil
}

// selectedZramAlgorithm picks the active algorithm out of comp_algorithm,
// which lists them all with the active one in brackets: "lzo lz4 [zstd]".
func selectedZramAlgorithm(s string) string {
	for _, f := range strings.Fields(s) {
		if strings.HasPrefix(f, "[") && strings.HasSuffix(f, "]") {
			return strings.Trim(f, "[]")
		}
	}
	return ""
}

// readSwapDevices returns the file names listed in /proc/swaps:
//
//	Filename        Type       Size     Used    Priority
//	/dev/zram0      partition  2097136  468864  100
//
// A missing file yields none.
func readSwapDevices(fsys fs.FS) (map[string]bool, error) {
	b, err := fs.ReadFile(fsys, "proc/swaps")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	swaps := map[string]bool{}
	for i, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if i == 0 || len(fields) == 0 {
			continue // header
		}
		swaps[fields[0]] = true
	}
	return swaps, nil
}
