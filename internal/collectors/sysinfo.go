package collectors

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

func init() { metrics.Register(&sysinfo{}) }

// sysinfo describes the machine: host, kernel, OS and board, plus uptime and
// process counts. It replaces the old uptime collector, so the uptime section
// and the system.uptime_s series now live here.
//
// Fields that cannot change while pisens runs (kernel, architecture, OS,
// board, total RAM) are read on the first successful sample and cached. The
// hostname, boot time, uptime and counts are read every time. Boot time is
// deliberately not cached: the kernel derives btime from the wall clock, so it
// moves when NTP corrects the clock after an early boot without an RTC.
//
// Only /proc/uptime, /proc/loadavg and the /proc listing are required. The
// rest is omitted when missing, so non-Pi hosts get a section without a board
// model, serial or revision.
type sysinfo struct {
	static *sysinfoStatic
}

// sysinfoStatic holds the cached fields.
type sysinfoStatic struct {
	kernel, arch, os string
	board            boardInfo
}

type sysinfoData struct {
	Hostname string    `json:"hostname,omitempty"`
	Kernel   string    `json:"kernel,omitempty"` // /proc/sys/kernel/osrelease
	Arch     string    `json:"arch"`             // kernel architecture, e.g. "aarch64"
	OS       string    `json:"os,omitempty"`     // os-release PRETTY_NAME
	Board    boardInfo `json:"board"`
	BootTime time.Time `json:"boot_time"`
	UptimeS  float64   `json:"uptime_s"`
	Procs    int       `json:"procs"`
	Threads  int       `json:"threads"`
	// Users counts login sessions in utmp, like uptime(1) does. Omitted when
	// the system keeps no utmp.
	Users *int `json:"users,omitempty"`
}

type boardInfo struct {
	Model    string `json:"model,omitempty"`    // "Raspberry Pi 5 Model B Rev 1.0"
	Serial   string `json:"serial,omitempty"`   // hex, as printed by the firmware
	Revision string `json:"revision,omitempty"` // Pi revision code, e.g. "d04170"
	// TotalRAMBytes is MemTotal: the RAM the kernel can use, a little under
	// what is fitted.
	TotalRAMBytes uint64 `json:"total_ram_bytes,omitempty"`
}

func (*sysinfo) Name() string { return "sysinfo" }

func (s *sysinfo) Collect(_ context.Context, env *metrics.Env) (metrics.Result, error) {
	up, err := readUptime(env.FS)
	if err != nil {
		return metrics.Result{}, err
	}
	load, err := readLoadAvg(env.FS)
	if err != nil {
		return metrics.Result{}, err
	}
	procs, err := countProcs(env.FS)
	if err != nil {
		return metrics.Result{}, err
	}
	if s.static == nil {
		st, err := readSysinfoStatic(env.FS)
		if err != nil {
			return metrics.Result{}, err
		}
		s.static = &st
	}

	d := sysinfoData{
		Kernel:  s.static.kernel,
		Arch:    s.static.arch,
		OS:      s.static.os,
		Board:   s.static.board,
		UptimeS: up,
		Procs:   procs,
		// nr_threads: every task the scheduler knows, threads included.
		Threads: load.TotalTasks,
	}
	if d.Hostname, _, err = sysinfoReadString(env.FS, "proc/sys/kernel/hostname"); err != nil {
		return metrics.Result{}, err
	}

	btime, ok, err := readBootTime(env.FS)
	if err != nil {
		return metrics.Result{}, err
	}
	if ok {
		d.BootTime = time.Unix(btime, 0).UTC()
	} else {
		d.BootTime = env.Now.Add(-time.Duration(up * float64(time.Second))).Truncate(time.Second)
	}

	users, ok, err := countUtmpUsers(env.FS)
	if err != nil {
		return metrics.Result{}, err
	}
	if ok {
		d.Users = &users
	}

	return metrics.Result{
		Data: d,
		Series: map[string]float64{
			"system.uptime_s": up,
			"system.procs":    float64(procs),
		},
	}, nil
}

// readSysinfoStatic reads the fields that are cached for the process lifetime.
func readSysinfoStatic(fsys fs.FS) (sysinfoStatic, error) {
	var st sysinfoStatic
	var err error
	if st.kernel, _, err = sysinfoReadString(fsys, "proc/sys/kernel/osrelease"); err != nil {
		return st, err
	}
	// /proc/sys/kernel/arch exists from Linux 6.1. Before that, fall back to
	// the architecture pisens was built for, which is close enough.
	var ok bool
	if st.arch, ok, err = sysinfoReadString(fsys, "proc/sys/kernel/arch"); err != nil {
		return st, err
	}
	if !ok {
		st.arch = runtime.GOARCH
	}
	if st.os, err = readOSName(fsys); err != nil {
		return st, err
	}
	if st.board, err = readBoard(fsys); err != nil {
		return st, err
	}
	return st, nil
}

// readUptime returns the first field of /proc/uptime, the seconds since boot:
// "354127.42 1398765.10".
func readUptime(fsys fs.FS) (float64, error) {
	b, err := fs.ReadFile(fsys, "proc/uptime")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return 0, fmt.Errorf("proc/uptime: empty")
	}
	secs, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, fmt.Errorf("proc/uptime: %w", err)
	}
	return secs, nil
}

// readBootTime returns the btime line of /proc/stat in Unix seconds. ok is
// false if the file or the line is missing.
func readBootTime(fsys fs.FS) (btime int64, ok bool, err error) {
	b, err := fs.ReadFile(fsys, "proc/stat")
	if errors.Is(err, fs.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024) // the intr line can be long
	for sc.Scan() {
		v, found := strings.CutPrefix(sc.Text(), "btime ")
		if !found {
			continue
		}
		btime, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			return 0, false, fmt.Errorf("proc/stat: btime: %w", err)
		}
		return btime, true, nil
	}
	if err := sc.Err(); err != nil {
		return 0, false, fmt.Errorf("proc/stat: %w", err)
	}
	return 0, false, nil
}

// countProcs counts the numeric directories in /proc, one per process.
func countProcs(fsys fs.FS) (int, error) {
	entries, err := fs.ReadDir(fsys, "proc")
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := strconv.ParseUint(e.Name(), 10, 32); err == nil {
			n++
		}
	}
	return n, nil
}

// readOSName returns PRETTY_NAME from os-release, or NAME and VERSION when
// there is no PRETTY_NAME. /etc/os-release wins over /usr/lib/os-release, as
// os-release(5) asks. It is empty if neither file exists.
func readOSName(fsys fs.FS) (string, error) {
	var b []byte
	var err error
	for _, name := range []string{"etc/os-release", "usr/lib/os-release"} {
		b, err = fs.ReadFile(fsys, name)
		if !errors.Is(err, fs.ErrNotExist) {
			break
		}
	}
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}

	vars := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		vars[key] = osReleaseUnquote(value)
	}
	if v := vars["PRETTY_NAME"]; v != "" {
		return v, nil
	}
	return strings.TrimSpace(vars["NAME"] + " " + vars["VERSION"]), nil
}

// osReleaseUnquote strips the shell-style quotes around an os-release value.
func osReleaseUnquote(v string) string {
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		if s, err := strconv.Unquote(v); err == nil {
			return s
		}
		return v[1 : len(v)-1]
	}
	if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
		return v[1 : len(v)-1]
	}
	return v
}

// readBoard reads the board model, serial and revision, and the total RAM.
//
// The device tree is tried first for the model and serial (Pi and other ARM
// boards), then the Model and Serial lines Raspberry Pi kernels add to
// /proc/cpuinfo. The revision code comes from /proc/cpuinfo, falling back to
// the device tree's linux,revision cell. x86 hosts usually have none of these.
func readBoard(fsys fs.FS) (boardInfo, error) {
	var bi boardInfo
	cpuinfo, err := readCPUInfoBoard(fsys)
	if err != nil {
		return bi, err
	}
	if bi.Model, err = readDeviceTreeString(fsys, "proc/device-tree/model"); err != nil {
		return bi, err
	}
	if bi.Model == "" {
		bi.Model = cpuinfo["Model"]
	}
	if bi.Serial, err = readDeviceTreeString(fsys, "proc/device-tree/serial-number"); err != nil {
		return bi, err
	}
	if bi.Serial == "" {
		bi.Serial = cpuinfo["Serial"]
	}
	bi.Revision = cpuinfo["Revision"]
	if bi.Revision == "" {
		b, err := fs.ReadFile(fsys, "proc/device-tree/system/linux,revision")
		switch {
		case err == nil && len(b) == 4:
			if rev := binary.BigEndian.Uint32(b); rev != 0 {
				bi.Revision = fmt.Sprintf("%04x", rev) // as /proc/cpuinfo prints it
			}
		case err != nil && !errors.Is(err, fs.ErrNotExist):
			return bi, err
		}
	}

	info, err := readMeminfo(fsys)
	switch {
	case err == nil:
		bi.TotalRAMBytes = info["MemTotal"]
	case !errors.Is(err, fs.ErrNotExist):
		return bi, err
	}
	return bi, nil
}

// readCPUInfoBoard returns the Model, Serial and Revision lines of
// /proc/cpuinfo that describe the board rather than a core:
//
//	Revision	: d04170
//	Serial		: 6c1f0a3e9b52d8e4
//	Model		: Raspberry Pi 5 Model B Rev 1.0
//
// Values made only of zeros, which some ARM kernels print when they do not
// know, are left out. A missing file yields none.
func readCPUInfoBoard(fsys fs.FS) (map[string]string, error) {
	out := map[string]string{}
	b, err := fs.ReadFile(fsys, "proc/cpuinfo")
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		switch key {
		case "Model", "Serial", "Revision":
			if strings.Trim(value, "0") != "" {
				out[key] = value
			}
		}
	}
	return out, nil
}

// readDeviceTreeString reads a device-tree string property, which ends in a
// NUL. It is empty if the property does not exist.
func readDeviceTreeString(fsys fs.FS, name string) (string, error) {
	b, err := fs.ReadFile(fsys, name)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(strings.TrimRight(string(b), "\x00")), nil
}

// sysinfoReadString reads a one-line file. ok is false if it does not exist.
func sysinfoReadString(fsys fs.FS, name string) (s string, ok bool, err error) {
	b, err := fs.ReadFile(fsys, name)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(string(b)), true, nil
}

// utmp record layout used by glibc on Linux, the same on 32 and 64-bit
// (including arm and arm64): 384 bytes starting with a 16-bit ut_type, with
// ut_user at byte 44 (after ut_pid, ut_line and ut_id).
const (
	utmpRecordSize  = 384
	utmpUserOffset  = 44
	utmpUserProcess = 7 // USER_PROCESS: a logged-in session
)

// countUtmpUsers counts the USER_PROCESS records in /run/utmp (or the older
// /var/run/utmp path). ok is false when neither exists, which is normal on
// systems that track sessions only in systemd-logind.
func countUtmpUsers(fsys fs.FS) (n int, ok bool, err error) {
	var b []byte
	for _, name := range []string{"run/utmp", "var/run/utmp"} {
		b, err = fs.ReadFile(fsys, name)
		if !errors.Is(err, fs.ErrNotExist) {
			break
		}
	}
	if errors.Is(err, fs.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	// A trailing partial record can show up while another process writes;
	// it is ignored. The type is host-endian, and every Pi is little-endian.
	// Like uptime(1), records without a user name are not counted.
	for off := 0; off+utmpRecordSize <= len(b); off += utmpRecordSize {
		if binary.LittleEndian.Uint16(b[off:]) == utmpUserProcess && b[off+utmpUserOffset] != 0 {
			n++
		}
	}
	return n, true, nil
}
