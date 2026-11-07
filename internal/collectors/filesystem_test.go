package collectors

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

// fakeStatter answers statfs from a table. Paths in block hang until their
// channel is closed, like a hard NFS mount whose server is gone.
type fakeStatter struct {
	stats map[string]fsStats
	errs  map[string]error
	block map[string]chan struct{}

	mu    sync.Mutex
	calls []string
}

func (f *fakeStatter) Statfs(path string) (fsStats, error) {
	f.mu.Lock()
	f.calls = append(f.calls, path)
	f.mu.Unlock()
	if ch, ok := f.block[path]; ok {
		<-ch
	}
	if err, ok := f.errs[path]; ok {
		return fsStats{}, err
	}
	if st, ok := f.stats[path]; ok {
		return st, nil
	}
	return fsStats{}, syscall.ENOENT
}

func (f *fakeStatter) callsFor(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == path {
			n++
		}
	}
	return n
}

// simpleStats is a 4 KiB-block filesystem with the given number of used and
// available blocks and no reserved ones.
func simpleStats(used, avail uint64) fsStats {
	return fsStats{BlockSize: 4096, Blocks: used + avail, Bfree: avail, Bavail: avail, Files: 1000, Ffree: 750}
}

func collectFS(t *testing.T, f *filesystem, fsys fstest.MapFS) (metrics.Result, filesystemData) {
	t.Helper()
	var env *metrics.Env
	if fsys == nil {
		env = &metrics.Env{FS: os.DirFS("testdata/filesystem/pi"), Now: time.Now()}
	} else {
		env = &metrics.Env{FS: fsys, Now: time.Now()}
	}
	res, err := f.Collect(context.Background(), env)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	data, ok := res.Data.(filesystemData)
	if !ok {
		t.Fatalf("Data is %T, want filesystemData", res.Data)
	}
	return res, data
}

// The fixture is a Pi with an SD card, an NVMe drive, a USB stick, a NAS
// mounted over NFS and SMB, snaps, Docker and a few awkward cases; see the
// comments in TestFilesystem for what is kept.
func piStatter() *fakeStatter {
	return &fakeStatter{
		stats: map[string]fsStats{
			"/": {
				BlockSize: 4096, Blocks: 7_500_000, Bfree: 5_000_000, Bavail: 4_620_000,
				Files: 1_900_000, Ffree: 1_710_000,
			},
			"/boot/firmware":    {BlockSize: 512, Blocks: 1_046_528, Bfree: 921_600, Bavail: 921_600},
			"/srv/data":         simpleStats(100, 300),
			"/media/pi/My Disk": simpleStats(50, 50),
			"/mnt/nas":          simpleStats(900, 100),
			"/mnt/usb_b":        simpleStats(1, 3),
			"/mnt/usb/b":        simpleStats(3, 1),
		},
		errs: map[string]error{"/mnt/media": syscall.EHOSTDOWN},
	}
}

func TestFilesystem(t *testing.T) {
	stat := piStatter()
	res, data := collectFS(t, &filesystem{stat: stat}, nil)

	type row struct{ name, device, mountpoint, fstype string }
	want := []row{
		{"root", "/dev/mmcblk0p2", "/", "ext4"},
		{"boot_firmware", "/dev/mmcblk0p1", "/boot/firmware", "vfat"},
		{"srv_data", "/dev/nvme0n1p1", "/srv/data", "btrfs"},
		// Skipped: the snap squashfs and the ext4 image on /dev/loop1, and
		// the bind mounts of the SD card and the NVMe drive.
		{"media_pi_my_disk", "/dev/sda1", "/media/pi/My Disk", "exfat"}, // \040 unescaped
		{"mnt_nas", "nas.local:/volume1/backup", "/mnt/nas", "nfs4"},
		{"mnt_media", "//nas.local/media", "/mnt/media", "cifs"},
		// Skipped: /dev/sdb1 on /mnt/usb, hidden by the tmpfs mounted over it.
		{"mnt_usb_b", "/dev/sdc1", "/mnt/usb_b", "xfs"},
		{"mnt_usb_b_2", "/dev/sdd1", "/mnt/usb/b", "f2fs"}, // same key as /mnt/usb_b
	}
	var got []row
	for _, m := range data.Mounts {
		got = append(got, row{m.Name, m.Device, m.Mountpoint, m.FSType})
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mounts:\n got %v\nwant %v", got, want)
	}

	wantSeries := map[string]float64{
		"fs.root.used_pct":               35.11, // 2.5M of 7.12M blocks; the 380k reserved count as neither
		"fs.root.used_bytes":             10_240_000_000,
		"fs.boot_firmware.used_pct":      11.94,
		"fs.boot_firmware.used_bytes":    63_963_136,
		"fs.srv_data.used_pct":           25,
		"fs.srv_data.used_bytes":         409_600,
		"fs.media_pi_my_disk.used_pct":   50,
		"fs.media_pi_my_disk.used_bytes": 204_800,
		"fs.mnt_nas.used_pct":            90,
		"fs.mnt_nas.used_bytes":          3_686_400,
		"fs.mnt_usb_b.used_pct":          25,
		"fs.mnt_usb_b.used_bytes":        4096,
		"fs.mnt_usb_b_2.used_pct":        75,
		"fs.mnt_usb_b_2.used_bytes":      12_288,
		// mnt_media failed and has no series.
	}
	if !reflect.DeepEqual(res.Series, wantSeries) {
		t.Errorf("Series = %v\nwant %v", res.Series, wantSeries)
	}

	if len(data.Mounts) != len(want) {
		t.FailNow()
	}
	wantRoot := filesystemMount{
		Name: "root", Device: "/dev/mmcblk0p2", Mountpoint: "/", FSType: "ext4",
		filesystemUsage: &filesystemUsage{
			SizeBytes: 30_720_000_000, UsedBytes: 10_240_000_000, AvailableBytes: 18_923_520_000,
			UsedPct: 35.11, Inodes: 1_900_000, InodesUsed: 190_000, InodesFree: 1_710_000, InodesUsedPct: 10,
		},
	}
	if !reflect.DeepEqual(data.Mounts[0], wantRoot) {
		t.Errorf("root = %+v\nwant %+v", data.Mounts[0], wantRoot)
	}
	if boot := data.Mounts[1]; boot.filesystemUsage == nil || boot.Inodes != 0 || boot.InodesUsedPct != 0 {
		t.Errorf("boot_firmware = %+v, want usage without inodes", boot)
	}

	media := data.Mounts[5]
	if !media.ReadOnly {
		t.Errorf("mnt_media ReadOnly = false, want true (mounted ro)")
	}
	if media.filesystemUsage != nil || !strings.Contains(media.Error, "host is down") {
		t.Errorf("mnt_media = %+v, want no usage and the statfs error", media)
	}
	b, err := json.Marshal(media)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "size_bytes") {
		t.Errorf("failed mount JSON = %s, want no usage fields", b)
	}
}

func TestFilesystemJSON(t *testing.T) {
	_, data := collectFS(t, &filesystem{stat: piStatter()}, nil)
	b, err := json.Marshal(data.Mounts[2])
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"name":"srv_data","device":"/dev/nvme0n1p1","mountpoint":"/srv/data","fstype":"btrfs",` +
		`"read_only":false,"size_bytes":1638400,"used_bytes":409600,"available_bytes":1228800,"used_pct":25,` +
		`"inodes":1000,"inodes_used":250,"inodes_free":750,"inodes_used_pct":25}`
	if string(b) != want {
		t.Errorf("JSON =\n%s\nwant\n%s", b, want)
	}
}

func TestFilesystemNoMounts(t *testing.T) {
	fsys := fstest.MapFS{"proc/self/mounts": {Data: []byte("proc /proc proc rw 0 0\ntmpfs /run tmpfs rw 0 0\n")}}
	res, _ := collectFS(t, &filesystem{stat: &fakeStatter{}}, fsys)
	if len(res.Series) != 0 {
		t.Errorf("Series = %v, want none", res.Series)
	}
	b, _ := json.Marshal(res.Data)
	if string(b) != `{"mounts":[]}` {
		t.Errorf("Data = %s, want {\"mounts\":[]}", b)
	}
}

func TestFilesystemHungMount(t *testing.T) {
	fsys := fstest.MapFS{"proc/self/mounts": {Data: []byte(
		"/dev/mmcblk0p2 / ext4 rw 0 0\n" +
			"nas:/export /mnt/nas nfs rw,hard 0 0\n")}}
	hang := make(chan struct{})
	stat := &fakeStatter{
		stats: map[string]fsStats{"/": simpleStats(1, 1), "/mnt/nas": simpleStats(1, 3)},
		block: map[string]chan struct{}{"/mnt/nas": hang},
	}
	f := &filesystem{stat: stat, timeout: 50 * time.Millisecond}

	start := time.Now()
	res, data := collectFS(t, f, fsys)
	if d := time.Since(start); d > time.Second {
		t.Fatalf("Collect took %v with a hung mount, want about the 50ms timeout", d)
	}
	if _, ok := res.Series["fs.root.used_pct"]; !ok {
		t.Errorf("Series = %v, want root reported despite the hung mount", res.Series)
	}
	if _, ok := res.Series["fs.mnt_nas.used_pct"]; ok {
		t.Errorf("Series has fs.mnt_nas.used_pct for a hung mount")
	}
	if nas := data.Mounts[1]; !strings.Contains(nas.Error, "timed out") {
		t.Errorf("mnt_nas Error = %q, want a timeout", nas.Error)
	}

	// The stuck call is not repeated: the next sample reports it right away.
	start = time.Now()
	_, data = collectFS(t, f, fsys)
	if d := time.Since(start); d > 40*time.Millisecond {
		t.Errorf("second Collect took %v, want no wait for a mount that is still hanging", d)
	}
	if nas := data.Mounts[1]; !strings.Contains(nas.Error, "still waiting") {
		t.Errorf("mnt_nas Error = %q, want it still waiting", nas.Error)
	}
	if n := stat.callsFor("/mnt/nas"); n != 1 {
		t.Errorf("statfs /mnt/nas called %d times, want 1 while it hangs", n)
	}

	// Once the server is back the mount is statted again.
	close(hang)
	deadline := time.Now().Add(5 * time.Second)
	for {
		res, _ = collectFS(t, f, fsys)
		if res.Series["fs.mnt_nas.used_pct"] == 25 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Series = %v, want mnt_nas back after the hang ended", res.Series)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestFilesystemTimeoutFollowsContext(t *testing.T) {
	fsys := fstest.MapFS{"proc/self/mounts": {Data: []byte("nas:/export /mnt/nas nfs rw 0 0\n")}}
	hang := make(chan struct{})
	defer close(hang)
	f := &filesystem{stat: &fakeStatter{block: map[string]chan struct{}{"/mnt/nas": hang}}}

	// The sampler gives each collector its own deadline; statfs gets half of it
	// so the section is still reported before the sampler gives up.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	res, err := f.Collect(ctx, &metrics.Env{FS: fsys})
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if ctx.Err() != nil {
		t.Fatal("Collect returned after its context expired")
	}
	if m := res.Data.(filesystemData).Mounts[0]; !strings.Contains(m.Error, "timed out") {
		t.Errorf("Error = %q, want a timeout", m.Error)
	}
}

func TestFilesystemErrors(t *testing.T) {
	tests := map[string]fstest.MapFS{
		"missing mounts": {},
		"short line":     {"proc/self/mounts": {Data: []byte("/dev/sda1 /mnt ext4\n")}},
	}
	for name, fsys := range tests {
		t.Run(name, func(t *testing.T) {
			env := &metrics.Env{FS: fsys}
			if _, err := (&filesystem{stat: &fakeStatter{}}).Collect(context.Background(), env); err == nil {
				t.Fatal("Collect() error = nil, want an error")
			}
		})
	}
}

func TestFsMountName(t *testing.T) {
	tests := map[string]string{
		"/":                 "root",
		"/boot/firmware":    "boot_firmware",
		"/home":             "home",
		"/mnt/usb-drive.1":  "mnt_usb_drive_1",
		"/media/pi/My Disk": "media_pi_my_disk",
		"/mnt/":             "mnt",
		"/--":               "mount",
	}
	for in, want := range tests {
		if got := fsMountName(in); got != want {
			t.Errorf("fsMountName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFsUnescape(t *testing.T) {
	tests := map[string]string{
		`/media/My\040Disk`: "/media/My Disk",
		`/a\011b\134c`:      "/a\tb\\c",
		`/plain`:            "/plain",
		`/trailing\04`:      `/trailing\04`,
		`/bad\999`:          `/bad\999`,
	}
	for in, want := range tests {
		if got := fsUnescape(in); got != want {
			t.Errorf("fsUnescape(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFilesystemRealStatfs(t *testing.T) {
	st, err := syscallStatter{}.Statfs(".")
	if errors.Is(err, errors.ErrUnsupported) {
		t.Skip("statfs is only implemented on Linux")
	}
	if err != nil {
		t.Fatalf("Statfs(.) error = %v", err)
	}
	if st.BlockSize == 0 || st.Blocks == 0 {
		t.Errorf("Statfs(.) = %+v, want a block size and blocks", st)
	}
}

func TestFilesystemIsRegistered(t *testing.T) {
	for _, c := range metrics.Registered() {
		if c.Name() == "filesystem" {
			return
		}
	}
	t.Fatal("filesystem collector is not registered")
}
