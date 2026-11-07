package collectors

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

func init() { metrics.Register(&filesystem{}) }

// filesystem reports the size, usage and inodes of every real filesystem
// mounted on the machine. The mounts come from /proc/self/mountinfo and the
// numbers from statfs(2).
//
// Only the filesystem types in filesystemTypes are reported, which leaves out
// the pseudo filesystems (proc, sysfs, tmpfs, cgroup, overlay, squashfs, ...).
// Loop devices and snap mounts are skipped as well, and so are bind mounts:
// when one device is mounted more than once only the first mount point is
// kept, since they all have the same usage.
//
// statfs on a network mount whose server is gone can block for minutes (or
// forever on a hard NFS mount), and the kernel does not let us cancel it. Each
// mount is therefore statted in its own goroutine and given at most
// filesystemStatTimeout; a mount that does not answer in time is reported with
// an error and no usage, while the others are reported as usual. A mount whose
// statfs from an earlier sample is still stuck is not statted again until that
// call returns, so a dead server costs one goroutine, not one per sample.
type filesystem struct {
	// stat is nil in production, which uses the statfs syscall.
	stat fsStatter
	// timeout overrides filesystemStatTimeout in tests.
	timeout time.Duration

	mu sync.Mutex
	// pending holds the mount points whose statfs is still running, possibly
	// left behind by an earlier sample that gave up waiting.
	pending map[string]bool
}

// fsStatter is the statfs syscall, behind an interface so tests can fake it.
type fsStatter interface {
	Statfs(path string) (fsStats, error)
}

// fsStats holds the statfs fields we use. Block counts are in units of
// BlockSize bytes.
type fsStats struct {
	BlockSize uint64 // fragment size (f_frsize)
	Blocks    uint64 // total data blocks
	Bfree     uint64 // free blocks, including those reserved for root
	Bavail    uint64 // free blocks available to unprivileged users
	Files     uint64 // total inodes
	Ffree     uint64 // free inodes
}

// filesystemStatTimeout is how long one sample waits for statfs. The sampler's
// own timeout defaults to the 2 s interval, so this is capped further to half
// of the time the context has left.
const filesystemStatTimeout = time.Second

// filesystemTypes are the filesystem types worth reporting.
var filesystemTypes = map[string]bool{
	"ext2": true, "ext3": true, "ext4": true,
	"vfat": true, "exfat": true, "ntfs": true, "ntfs3": true,
	"fuseblk": true, // NTFS through ntfs-3g
	"btrfs":   true, "xfs": true, "f2fs": true,
	"nfs": true, "nfs4": true,
	"cifs": true, "smb3": true,
}

type filesystemData struct {
	Mounts []filesystemMount `json:"mounts"`
}

type filesystemMount struct {
	// Name is the key used in the series, fs.<name>.used_pct: "root" for /,
	// otherwise the mount point with "/" turned into "_" ("boot_firmware").
	Name       string `json:"name"`
	Device     string `json:"device"`
	Mountpoint string `json:"mountpoint"`
	FSType     string `json:"fstype"`
	ReadOnly   bool   `json:"read_only"`
	// Usage is missing when statfs failed or timed out; Error says why.
	*filesystemUsage
	Error string `json:"error,omitempty"`
}

type filesystemUsage struct {
	SizeBytes      uint64 `json:"size_bytes"`
	UsedBytes      uint64 `json:"used_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
	// UsedPct is used / (used + available), like df: the blocks reserved for
	// root count as neither, so a filesystem is 100% full when ordinary users
	// cannot write to it any more.
	UsedPct       float64 `json:"used_pct"`
	Inodes        uint64  `json:"inodes"`
	InodesUsed    uint64  `json:"inodes_used"`
	InodesFree    uint64  `json:"inodes_free"`
	InodesUsedPct float64 `json:"inodes_used_pct"` // 0 on filesystems without inodes, such as vfat
}

// fsMount is one line of /proc/self/mountinfo.
type fsMount struct {
	device, mountpoint, fstype string
	readOnly                   bool
}

func (*filesystem) Name() string { return "filesystem" }

func (f *filesystem) Collect(ctx context.Context, env *metrics.Env) (metrics.Result, error) {
	all, err := readMounts(env.FS)
	if err != nil {
		return metrics.Result{}, err
	}
	mounts := filterMounts(all)

	usage, errs := f.statAll(ctx, mounts)

	data := filesystemData{Mounts: make([]filesystemMount, 0, len(mounts))}
	series := map[string]float64{}
	names := map[string]bool{}
	for i, m := range mounts {
		name := fsUniqueName(names, fsMountName(m.mountpoint))
		fm := filesystemMount{
			Name:       name,
			Device:     m.device,
			Mountpoint: m.mountpoint,
			FSType:     m.fstype,
			ReadOnly:   m.readOnly,
		}
		if errs[i] != nil {
			fm.Error = errs[i].Error()
		} else {
			u := usage[i]
			fm.filesystemUsage = &u
			series["fs."+name+".used_pct"] = u.UsedPct
			series["fs."+name+".used_bytes"] = float64(u.UsedBytes)
		}
		data.Mounts = append(data.Mounts, fm)
	}
	if len(series) == 0 {
		return metrics.Result{Data: data}, nil
	}
	return metrics.Result{Data: data, Series: series}, nil
}

// statAll statfs's every mount concurrently and waits until they have all
// answered, the stat timeout expires or ctx is done. Mounts that did not
// answer get an error; their goroutines are left to finish on their own.
func (f *filesystem) statAll(ctx context.Context, mounts []fsMount) ([]filesystemUsage, []error) {
	usage := make([]filesystemUsage, len(mounts))
	errs := make([]error, len(mounts))

	stat := f.stat
	if stat == nil {
		stat = syscallStatter{}
	}
	timeout := f.timeout
	if timeout <= 0 {
		timeout = filesystemStatTimeout
	}
	if deadline, ok := ctx.Deadline(); ok {
		if left := time.Until(deadline) / 2; left < timeout {
			timeout = left
		}
	}

	type answer struct {
		i   int
		st  fsStats
		err error
	}
	answers := make(chan answer, len(mounts)) // buffered so late goroutines never block
	waiting := map[int]bool{}
	for i, m := range mounts {
		if !f.claim(m.mountpoint) {
			errs[i] = fmt.Errorf("statfs %s: still waiting for an earlier call", m.mountpoint)
			continue
		}
		waiting[i] = true
		go func() {
			st, err := stat.Statfs(m.mountpoint)
			f.release(m.mountpoint)
			answers <- answer{i, st, err}
		}()
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
wait:
	for len(waiting) > 0 {
		select {
		case a := <-answers:
			delete(waiting, a.i)
			if a.err != nil {
				errs[a.i] = fmt.Errorf("statfs %s: %w", mounts[a.i].mountpoint, a.err)
			} else {
				usage[a.i] = fsUsageOf(a.st)
			}
		case <-timer.C:
			break wait
		case <-ctx.Done():
			break wait
		}
	}
	for i := range waiting {
		errs[i] = fmt.Errorf("statfs %s: timed out after %v", mounts[i].mountpoint, timeout.Round(time.Millisecond))
	}
	return usage, errs
}

// claim marks mountpoint as being statted. It returns false if a call for it
// is still running.
func (f *filesystem) claim(mountpoint string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pending[mountpoint] {
		return false
	}
	if f.pending == nil {
		f.pending = map[string]bool{}
	}
	f.pending[mountpoint] = true
	return true
}

func (f *filesystem) release(mountpoint string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.pending, mountpoint)
}

func fsUsageOf(st fsStats) filesystemUsage {
	u := filesystemUsage{
		SizeBytes:      st.Blocks * st.BlockSize,
		UsedBytes:      subClamp(st.Blocks, st.Bfree) * st.BlockSize,
		AvailableBytes: st.Bavail * st.BlockSize,
		Inodes:         st.Files,
		InodesFree:     st.Ffree,
		InodesUsed:     subClamp(st.Files, st.Ffree),
	}
	u.UsedPct = pctOf(u.UsedBytes, u.UsedBytes+u.AvailableBytes)
	u.InodesUsedPct = pctOf(u.InodesUsed, u.Inodes)
	return u
}

// readMounts parses /proc/self/mountinfo (see proc_pid_mountinfo(5)):
//
//	36 25 179:2 / / ro,noatime shared:1 - ext4 /dev/mmcblk0p2 rw
//
// The fields before the " - " separator describe the mount in this mount
// namespace: its ID, the parent's ID, major:minor, the root of the mount
// within the filesystem, the mount point, the per-mount options and zero or
// more optional fields. The three after it describe the filesystem itself:
// its type, the source and the superblock options.
//
// read_only comes from the superblock options, not the per-mount ones.
// systemd's ProtectSystem=strict remounts everything read-only in the
// service's namespace, which only sets the per-mount flag; what we want to
// show is a filesystem that really is read-only, such as an SD card the
// kernel switched to ro after I/O errors.
//
// Spaces, tabs, newlines and backslashes in the source and mount point are
// octal-escaped by the kernel ("\040").
func readMounts(fsys fs.FS) ([]fsMount, error) {
	const path = "proc/self/mountinfo"
	b, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil, err
	}
	var mounts []fsMount
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024) // overlay options can be long
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 {
			continue
		}
		// The separator follows the six fixed fields and any optional ones.
		sep := -1
		for i := 6; i < len(fields); i++ {
			if fields[i] == "-" {
				sep = i
				break
			}
		}
		if sep < 0 || len(fields) < sep+4 {
			return nil, fmt.Errorf("%s: want 6 fields, \" - \" and 3 more, got %q", path, sc.Text())
		}
		mounts = append(mounts, fsMount{
			device:     fsUnescape(fields[sep+2]),
			mountpoint: fsUnescape(fields[4]),
			fstype:     fields[sep+1],
			readOnly:   fsHasOption(fields[sep+3], "ro"),
		})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return mounts, nil
}

// fsHasOption reports whether the comma-separated options contain opt.
func fsHasOption(options, opt string) bool {
	for _, o := range strings.Split(options, ",") {
		if o == opt {
			return true
		}
	}
	return false
}

// filterMounts keeps the mounts worth reporting, in mount order:
//
//   - When something is mounted over a mount point, only the last mount is
//     visible (and what statfs sees), so earlier ones on the same path go.
//   - Only the types in filesystemTypes are kept.
//   - Loop devices and snap mounts are dropped.
//   - Bind mounts and other repeats of a device are dropped, keeping the
//     first mount point.
func filterMounts(all []fsMount) []fsMount {
	last := map[string]int{}
	for i, m := range all {
		last[m.mountpoint] = i
	}
	seen := map[string]bool{}
	var out []fsMount
	for i, m := range all {
		switch {
		case last[m.mountpoint] != i,
			!filesystemTypes[m.fstype],
			strings.HasPrefix(m.device, "/dev/loop"),
			m.mountpoint == "/snap", strings.HasPrefix(m.mountpoint, "/snap/"),
			strings.HasPrefix(m.mountpoint, "/var/lib/snapd/"),
			seen[m.device]:
			continue
		}
		seen[m.device] = true
		out = append(out, m)
	}
	return out
}

// fsMountName turns a mount point into a series name: "/" → "root",
// "/boot/firmware" → "boot_firmware", "/media/pi/My Disk" → "media_pi_my_disk".
func fsMountName(mountpoint string) string {
	if mountpoint == "/" {
		return "root"
	}
	var b strings.Builder
	pending := false
	for _, r := range strings.ToLower(mountpoint) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if pending && b.Len() > 0 {
				b.WriteByte('_')
			}
			pending = false
			b.WriteRune(r)
		} else {
			pending = true
		}
	}
	if b.Len() == 0 {
		return "mount"
	}
	return b.String()
}

// fsUniqueName returns name, or name_2, name_3, ... if it is already in taken
// ("/mnt/a_b" and "/mnt/a/b" both sanitise to "mnt_a_b"), and marks it taken.
func fsUniqueName(taken map[string]bool, name string) string {
	unique := name
	for i := 2; taken[unique]; i++ {
		unique = name + "_" + strconv.Itoa(i)
	}
	taken[unique] = true
	return unique
}

// fsUnescape decodes the \ooo octal escapes of /proc/self/mountinfo.
func fsUnescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+4 <= len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
