package collectors

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

var sysinfoNow = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)

func collectSysinfo(t *testing.T, s *sysinfo, fsys fs.FS) (metrics.Result, sysinfoData) {
	t.Helper()
	res, err := s.Collect(context.Background(), &metrics.Env{FS: fsys, Now: sysinfoNow})
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	data, ok := res.Data.(sysinfoData)
	if !ok {
		t.Fatalf("Data is %T, want sysinfoData", res.Data)
	}
	return res, data
}

// sysinfoMinimal holds only the files sysinfo requires.
func sysinfoMinimal() fstest.MapFS {
	return fstest.MapFS{
		"proc/uptime":  {Data: []byte("100.5 300.0\n")},
		"proc/loadavg": {Data: []byte("0.00 0.01 0.05 1/90 1234\n")},
		"proc/1/comm":  {Data: []byte("init\n")},
	}
}

// testdata/sysinfo/pi5 is a Raspberry Pi 5 (8 GB) on Raspberry Pi OS trixie
// with two SSH sessions open.
func TestSysinfoPi5(t *testing.T) {
	res, data := collectSysinfo(t, &sysinfo{}, os.DirFS("testdata/sysinfo/pi5"))

	wantSeries := map[string]float64{
		"system.uptime_s": 354127.42,
		"system.procs":    3,
	}
	if !reflect.DeepEqual(res.Series, wantSeries) {
		t.Errorf("Series = %v, want %v", res.Series, wantSeries)
	}

	users := 2
	want := sysinfoData{
		Hostname: "pi5-lab",
		Kernel:   "6.12.47+rpt-rpi-2712",
		Arch:     "aarch64",
		OS:       "Debian GNU/Linux 13 (trixie)",
		Board: boardInfo{
			Model:          "Raspberry Pi 5 Model B Rev 1.0", // trailing NUL stripped
			Serial:         "6c1f0a3e9b52d8e4",
			Revision:       "d04170",
			TotalRAMBytes:  8255824 * kB,
			Type:           "5",
			Processor:      "BCM2712",
			Manufacturer:   "Sony UK",
			FittedRAMBytes: 8 << 30,
			PCBRevision:    "1.0",
		},
		BootTime: time.Date(2026, 11, 3, 9, 37, 52, 0, time.UTC), // btime 1793698672
		UptimeS:  354127.42,
		Procs:    3,
		Threads:  575,
		Users:    &users,
	}
	if !reflect.DeepEqual(data, want) {
		t.Errorf("Data = %+v\nwant %+v", data, want)
	}
}

// testdata/sysinfo/generic is an x86 VM: no device tree, no Pi lines in
// /proc/cpuinfo, os-release only under /usr/lib and no utmp.
func TestSysinfoNonPi(t *testing.T) {
	res, data := collectSysinfo(t, &sysinfo{}, os.DirFS("testdata/sysinfo/generic"))

	if res.Series["system.procs"] != 2 || res.Series["system.uptime_s"] != 8123.5 {
		t.Errorf("Series = %v, want procs 2 and uptime 8123.5", res.Series)
	}
	want := sysinfoData{
		Hostname: "ci-runner",
		Kernel:   "6.8.0-49-generic",
		Arch:     "x86_64",
		OS:       "Ubuntu 24.04.1 LTS",
		Board:    boardInfo{TotalRAMBytes: 4015632 * kB},
		BootTime: time.Unix(1794469876, 0).UTC(),
		UptimeS:  8123.5,
		Procs:    2,
		Threads:  143,
	}
	if !reflect.DeepEqual(data, want) {
		t.Errorf("Data = %+v\nwant %+v", data, want)
	}

	b, err := json.Marshal(res.Data)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"model"`, `"serial"`, `"revision"`, `"type"`, `"processor"`, `"manufacturer"`, `"fitted_ram_bytes"`, `"pcb_revision"`, `"users"`} {
		if strings.Contains(string(b), key) {
			t.Errorf("Data = %s, want no %s", b, key)
		}
	}
}

func TestSysinfoFallbacks(t *testing.T) {
	fsys := sysinfoMinimal()
	// Pi kernel without a device tree mounted: board from /proc/cpuinfo,
	// except the revision, which only the device tree has here.
	fsys["proc/cpuinfo"] = &fstest.MapFile{Data: []byte(
		"processor\t: 0\nRevision\t: 0000\nSerial\t\t: 00000000aabbccdd\nModel\t\t: Raspberry Pi 4 Model B Rev 1.4\n")}
	fsys["proc/device-tree/system/linux,revision"] = &fstest.MapFile{Data: []byte{0x00, 0xc0, 0x31, 0x14}}
	fsys["etc/os-release"] = &fstest.MapFile{Data: []byte("# comment\nNAME='Alpine Linux'\nVERSION=3.20\n")}

	res, data := collectSysinfo(t, &sysinfo{}, fsys)

	if data.Arch != runtime.GOARCH {
		t.Errorf("Arch = %q, want the build architecture %q", data.Arch, runtime.GOARCH)
	}
	if want := sysinfoNow.Add(-101 * time.Second); !data.BootTime.Equal(want) {
		t.Errorf("BootTime = %v, want now - 100.5 s truncated = %v", data.BootTime, want)
	}
	if data.OS != "Alpine Linux 3.20" {
		t.Errorf("OS = %q, want NAME and VERSION", data.OS)
	}
	wantBoard := boardInfo{
		Model: "Raspberry Pi 4 Model B Rev 1.4", Serial: "00000000aabbccdd", Revision: "c03114",
		Type: "4B", Processor: "BCM2711", Manufacturer: "Sony UK", FittedRAMBytes: 4 << 30, PCBRevision: "1.4",
	}
	if data.Board != wantBoard {
		t.Errorf("Board = %+v, want %+v", data.Board, wantBoard)
	}
	if data.Hostname != "" || data.Kernel != "" || data.Users != nil {
		t.Errorf("Data = %+v, want no hostname, kernel or users", data)
	}
	if res.Series["system.uptime_s"] != 100.5 || res.Series["system.procs"] != 1 {
		t.Errorf("Series = %v", res.Series)
	}
}

func TestDecodeRevision(t *testing.T) {
	tests := []struct {
		code string
		want boardInfo
	}{
		{"d04170", boardInfo{Type: "5", Processor: "BCM2712", Manufacturer: "Sony UK", FittedRAMBytes: 8 << 30, PCBRevision: "1.0"}},
		{"c03114", boardInfo{Type: "4B", Processor: "BCM2711", Manufacturer: "Sony UK", FittedRAMBytes: 4 << 30, PCBRevision: "1.4"}},
		{"a02082", boardInfo{Type: "3B", Processor: "BCM2837", Manufacturer: "Sony UK", FittedRAMBytes: 1 << 30, PCBRevision: "1.2"}},
		{"902120", boardInfo{Type: "Zero 2 W", Processor: "BCM2837", Manufacturer: "Sony UK", FittedRAMBytes: 512 << 20, PCBRevision: "1.0"}},
		{"a22042", boardInfo{Type: "2B", Processor: "BCM2837", Manufacturer: "Embest", FittedRAMBytes: 1 << 30, PCBRevision: "1.2"}},
		// Warranty bit 25 set by overclocking: still decoded.
		{"2a02082", boardInfo{Type: "3B", Processor: "BCM2837", Manufacturer: "Sony UK", FittedRAMBytes: 1 << 30, PCBRevision: "1.2"}},
		// Values the docs do not list stay empty: memory 7, manufacturer 9,
		// processor 9, type 0xff.
		{"f99ff1", boardInfo{PCBRevision: "1.1"}},
		// Old style (Pi 1 Model B rev 2.0, 512 MB): not decoded.
		{"000e", boardInfo{}},
		{"1000002", boardInfo{}}, // old style with the warranty bit
		{"", boardInfo{}},
		{"not-hex", boardInfo{}},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			bi := boardInfo{Revision: tt.code}
			decodeRevision(&bi)
			tt.want.Revision = tt.code
			if bi != tt.want {
				t.Errorf("decodeRevision(%q) = %+v, want %+v", tt.code, bi, tt.want)
			}
		})
	}
}

func TestSysinfoCachesStaticFields(t *testing.T) {
	fsys := sysinfoMinimal()
	fsys["proc/sys/kernel/hostname"] = &fstest.MapFile{Data: []byte("old\n")}
	fsys["proc/sys/kernel/osrelease"] = &fstest.MapFile{Data: []byte("6.1.0\n")}
	fsys["proc/stat"] = &fstest.MapFile{Data: []byte("btime 1000\n")}
	s := &sysinfo{}
	collectSysinfo(t, s, fsys)

	fsys["proc/sys/kernel/hostname"] = &fstest.MapFile{Data: []byte("new\n")}
	fsys["proc/sys/kernel/osrelease"] = &fstest.MapFile{Data: []byte("6.12.0\n")}
	fsys["proc/stat"] = &fstest.MapFile{Data: []byte("btime 2000\n")} // NTP stepped the clock
	fsys["proc/uptime"] = &fstest.MapFile{Data: []byte("160.0 400.0\n")}
	fsys["proc/2/comm"] = &fstest.MapFile{Data: []byte("sh\n")}
	res, data := collectSysinfo(t, s, fsys)

	if data.Kernel != "6.1.0" {
		t.Errorf("Kernel = %q, want the cached 6.1.0", data.Kernel)
	}
	if data.Hostname != "new" || !data.BootTime.Equal(time.Unix(2000, 0)) || data.Procs != 2 {
		t.Errorf("Data = %+v, want hostname, boot time and procs re-read", data)
	}
	if res.Series["system.uptime_s"] != 160 {
		t.Errorf("system.uptime_s = %v, want 160", res.Series["system.uptime_s"])
	}
}

func TestSysinfoUtmp(t *testing.T) {
	record := func(typ uint16, user string) []byte {
		r := make([]byte, utmpRecordSize)
		r[0], r[1] = byte(typ), byte(typ>>8)
		copy(r[utmpUserOffset:], user)
		return r
	}
	var b []byte
	b = append(b, record(2, "reboot")...)
	b = append(b, record(utmpUserProcess, "pi")...)
	b = append(b, record(utmpUserProcess, "")...) // no user: not counted
	b = append(b, record(8, "")...)
	b = append(b, record(utmpUserProcess, "pi")[:100]...) // torn write

	fsys := sysinfoMinimal()
	fsys["var/run/utmp"] = &fstest.MapFile{Data: b}
	_, data := collectSysinfo(t, &sysinfo{}, fsys)
	if data.Users == nil || *data.Users != 1 {
		t.Errorf("Users = %v, want 1", data.Users)
	}

	fsys = sysinfoMinimal()
	fsys["run/utmp"] = &fstest.MapFile{Data: nil}
	_, data = collectSysinfo(t, &sysinfo{}, fsys)
	if data.Users == nil || *data.Users != 0 {
		t.Errorf("Users = %v, want 0 for an empty utmp", data.Users)
	}
}

func TestSysinfoErrors(t *testing.T) {
	with := func(name, content string) fstest.MapFS {
		fsys := sysinfoMinimal()
		fsys[name] = &fstest.MapFile{Data: []byte(content)}
		return fsys
	}
	without := func(name string) fstest.MapFS {
		fsys := sysinfoMinimal()
		delete(fsys, name)
		return fsys
	}
	tests := map[string]fstest.MapFS{
		"empty root":      {},
		"no uptime":       without("proc/uptime"),
		"empty uptime":    with("proc/uptime", "\n"),
		"garbage uptime":  with("proc/uptime", "abc 12\n"),
		"no loadavg":      without("proc/loadavg"),
		"garbage loadavg": with("proc/loadavg", "0.1 0.2 0.3 lots\n"),
		"garbage btime":   with("proc/stat", "cpu 1 2 3 4\nbtime yesterday\n"),
		"garbage meminfo": with("proc/meminfo", "MemTotal: lots kB\nMemFree: 1 kB\n"),
	}
	for name, fsys := range tests {
		t.Run(name, func(t *testing.T) {
			env := &metrics.Env{FS: fsys, Now: sysinfoNow}
			if _, err := (&sysinfo{}).Collect(context.Background(), env); err == nil {
				t.Fatal("Collect() error = nil, want an error")
			}
		})
	}
}

func TestSysinfoIsRegistered(t *testing.T) {
	for _, c := range metrics.Registered() {
		if c.Name() == "sysinfo" {
			return
		}
	}
	t.Fatal("sysinfo collector is not registered")
}
