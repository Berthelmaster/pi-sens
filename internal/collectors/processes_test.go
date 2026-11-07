package collectors

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

var procT0 = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)

func collectProc(t testing.TB, p *processes, fsys fs.FS, prev, now time.Time) processesData {
	t.Helper()
	res, err := p.Collect(context.Background(), &metrics.Env{FS: fsys, Now: now, Prev: prev})
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if res.Series != nil {
		t.Errorf("Series = %v, want none", res.Series)
	}
	data, ok := res.Data.(processesData)
	if !ok {
		t.Fatalf("Data is %T, want processesData", res.Data)
	}
	return data
}

func pct(v float64) *float64 { return &v }

// The fixtures are two snapshots of /proc taken 2s apart:
//
//   - 520 is multi-threaded and uses two cores; its uid is not in /etc/passwd
//   - 410's comm, "(sd-pam)", has parentheses of its own
//   - 2 and 150 are kernel threads and 900 is a zombie: no cmdline, no VmRSS
//   - 600 exits and 700 starts in between; 800 is reused by a new process
//   - in the second snapshot 950 exited after its stat was read (no status)
//     and 960 while its stat was read (empty file)
func TestProcesses(t *testing.T) {
	p := &processes{}

	first := collectProc(t, p, os.DirFS("testdata/processes/first"), time.Time{}, procT0)
	if first.Count != 11 {
		t.Errorf("first Count = %d, want 11", first.Count)
	}
	var pids []int
	for _, pr := range first.Processes {
		pids = append(pids, pr.PID)
		if pr.CPUPct != nil {
			t.Errorf("first sample: pid %d has cpu_pct %v, want none", pr.PID, *pr.CPUPct)
		}
	}
	// No CPU yet, so sorted by memory.
	if want := []int{520, 300, 1, 800, 600, 410, 950, 960, 2, 150, 900}; !reflect.DeepEqual(pids, want) {
		t.Errorf("first pids = %v, want %v", pids, want)
	}

	second := collectProc(t, p, os.DirFS("testdata/processes/second"), procT0, procT0.Add(2*time.Second))
	if second.Count != 10 {
		t.Errorf("second Count = %d, want 10", second.Count)
	}
	want := []procInfo{
		{
			PID: 520, Name: "Web Content", Command: "/usr/lib/firefox/firefox -contentproc -childID 1",
			User: "1234", State: "R", Threads: 12, CPUPct: pct(200), RSSBytes: 160000 * 1024,
		},
		{
			PID: 300, Name: "pisens", Command: "/usr/local/bin/pisens --listen :8080",
			User: "pi", State: "R", Threads: 8, CPUPct: pct(75), RSSBytes: 33600 * 1024,
		},
		{
			PID: 150, Name: "kworker/0:1-events", Command: "[kworker/0:1-events]",
			User: "root", State: "I", Threads: 1, CPUPct: pct(10),
		},
		{
			PID: 1, Name: "systemd", Command: "/sbin/init splash",
			User: "root", State: "S", Threads: 1, CPUPct: pct(3), RSSBytes: 12800 * 1024,
		},
		{
			PID: 410, Name: "(sd-pam)", Command: "(sd-pam)",
			User: "pi", State: "S", Threads: 1, CPUPct: pct(0), RSSBytes: 1600 * 1024,
		},
		{PID: 2, Name: "kthreadd", Command: "[kthreadd]", User: "root", State: "S", Threads: 1, CPUPct: pct(0)},
		{PID: 900, Name: "sh", Command: "[sh]", User: "pi", State: "Z", Threads: 1, CPUPct: pct(0)},
		// New since the last sample: no CPU value yet.
		{
			PID: 800, Name: "python3", Command: "python3 -m http.server",
			User: "pi", State: "S", Threads: 1, RSSBytes: 24000 * 1024,
		},
		{PID: 700, Name: "make", Command: "make -j4", User: "pi", State: "R", Threads: 1, RSSBytes: 6400 * 1024},
	}
	if !reflect.DeepEqual(second.Processes, want) {
		t.Errorf("second Processes:\n%s\nwant:\n%s", procDump(second.Processes), procDump(want))
	}

	// 700 and 800 have a baseline now.
	third := collectProc(t, p, os.DirFS("testdata/processes/second"), procT0.Add(2*time.Second), procT0.Add(4*time.Second))
	for _, pr := range third.Processes {
		if pr.CPUPct == nil || *pr.CPUPct != 0 {
			t.Errorf("third sample: pid %d cpu_pct = %v, want 0", pr.PID, pr.CPUPct)
		}
	}
}

func procDump(ps []procInfo) string {
	var s string
	for _, p := range ps {
		cpu := "-"
		if p.CPUPct != nil {
			cpu = fmt.Sprint(*p.CPUPct)
		}
		s += fmt.Sprintf("  %d %q %q user=%s state=%s threads=%d cpu=%s rss=%d\n",
			p.PID, p.Name, p.Command, p.User, p.State, p.Threads, cpu, p.RSSBytes)
	}
	return s
}

// fakeProc is one process in a generated /proc.
type fakeProc struct {
	pid, uid        int
	comm            string
	ticks, rssPages uint64
	startTime       uint64
}

// usersFile is where the collector looks up user names.
const usersFile = "etc/passwd"

// setUsers writes a users file naming the given uids into fsys.
func setUsers(fsys fstest.MapFS, users map[int]string) {
	var b strings.Builder
	for uid, name := range users {
		fmt.Fprintf(&b, "%s:x:%d:%d::/home/%s:/bin/sh\n", name, uid, uid, name)
	}
	fsys[usersFile] = &fstest.MapFile{Data: []byte(b.String())}
}

// procFS builds a /proc with the given processes and an /etc/passwd naming
// uid 0 and 1000. RSS is 16 KiB pages, as on the Pi 5 kernel.
func procFS(procs []fakeProc) fstest.MapFS {
	m := fstest.MapFS{
		"proc/uptime": {Data: []byte("123.45 400.00\n")},
		"proc/self":   {Mode: fs.ModeSymlink, Data: []byte("1")},
	}
	setUsers(m, map[int]string{0: "root", 1000: "pi"})
	for _, p := range procs {
		dir := fmt.Sprintf("proc/%d/", p.pid)
		m[dir+"stat"] = &fstest.MapFile{Data: []byte(fmt.Sprintf(
			"%d (%s) S 1 %d %d 0 -1 4194560 1200 0 3 0 %d %d 0 0 20 0 1 0 %d 123456789 %d 18446744073709551615 1 1 0 0 0 0 0 0 0 0 0 0 17 2 0 0 0 0 0\n",
			p.pid, p.comm, p.pid, p.pid, p.ticks/2, p.ticks-p.ticks/2, p.startTime, p.rssPages))}
		m[dir+"status"] = &fstest.MapFile{Data: []byte(fmt.Sprintf(
			"Name:\t%s\nUmask:\t0022\nState:\tS (sleeping)\nTgid:\t%d\nPid:\t%d\nPPid:\t1\nUid:\t%d\t%d\t%d\t%d\nGid:\t0\t0\t0\t0\nVmRSS:\t%8d kB\nThreads:\t1\n",
			p.comm, p.pid, p.pid, p.uid, p.uid, p.uid, p.uid, p.rssPages*16))}
		m[dir+"cmdline"] = &fstest.MapFile{Data: []byte("/usr/bin/" + p.comm + "\x00--flag\x00")}
	}
	return m
}

func TestProcessesTop(t *testing.T) {
	// 40 processes: CPU grows with the pid and memory shrinks with it, except
	// that pid 40 also uses the most memory.
	gen := func(sample uint64) []fakeProc {
		var procs []fakeProc
		for pid := 1; pid <= 40; pid++ {
			rss := uint64(41 - pid)
			if pid == 40 {
				rss = 1000
			}
			procs = append(procs, fakeProc{
				pid: pid, uid: 1000, comm: fmt.Sprintf("p%d", pid),
				ticks: 1000 + sample*uint64(pid), rssPages: rss, startTime: 10,
			})
		}
		return procs
	}
	p := &processes{}
	collectProc(t, p, procFS(gen(0)), time.Time{}, procT0)
	d := collectProc(t, p, procFS(gen(1)), procT0, procT0.Add(time.Second))

	if d.Count != 40 {
		t.Errorf("Count = %d, want 40", d.Count)
	}
	// The top 15 by CPU are 26-40 and the top 15 by memory 40 and 1-14; 40
	// is listed once. Everything used some CPU, so the list is sorted by it.
	var got []int
	for _, pr := range d.Processes {
		got = append(got, pr.PID)
	}
	var want []int
	for pid := 40; pid >= 26; pid-- {
		want = append(want, pid)
	}
	for pid := 14; pid >= 1; pid-- {
		want = append(want, pid)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("pids = %v\nwant %v", got, want)
	}
	if c := d.Processes[0].CPUPct; c == nil || *c != 40 {
		t.Errorf("pid 40 cpu_pct = %v, want 40", c)
	}
}

func TestProcessesIdleSystem(t *testing.T) {
	// Nothing used CPU, so only the memory top is listed.
	var procs []fakeProc
	for pid := 1; pid <= 20; pid++ {
		procs = append(procs, fakeProc{pid: pid, comm: "idle", ticks: 5, rssPages: uint64(pid), startTime: 1})
	}
	p := &processes{}
	collectProc(t, p, procFS(procs), time.Time{}, procT0)
	d := collectProc(t, p, procFS(procs), procT0, procT0.Add(time.Second))
	if len(d.Processes) != procTop {
		t.Fatalf("got %d processes, want %d", len(d.Processes), procTop)
	}
	if d.Processes[0].PID != 20 || d.Processes[procTop-1].PID != 6 {
		t.Errorf("listed pids %d..%d, want 20..6", d.Processes[0].PID, d.Processes[procTop-1].PID)
	}
}

func TestProcessesNoCPUWithoutTimeOrAfterReset(t *testing.T) {
	p := &processes{}
	collectProc(t, p, procFS([]fakeProc{
		{pid: 10, comm: "a", ticks: 100, startTime: 1},
		{pid: 11, comm: "b", ticks: 100, startTime: 1},
	}), time.Time{}, procT0)

	// The clock did not move.
	d := collectProc(t, p, procFS([]fakeProc{{pid: 10, comm: "a", ticks: 200, startTime: 1}}), procT0, procT0)
	if c := d.Processes[0].CPUPct; c != nil {
		t.Errorf("cpu_pct = %v without elapsed time, want none", *c)
	}

	// The times went backwards for the same process: no value rather than a
	// negative or huge one.
	d = collectProc(t, p, procFS([]fakeProc{{pid: 10, comm: "a", ticks: 50, startTime: 1}}), procT0, procT0.Add(time.Second))
	if c := d.Processes[0].CPUPct; c != nil {
		t.Errorf("cpu_pct = %v after the times went backwards, want none", *c)
	}
}

func TestProcessesPasswdReload(t *testing.T) {
	fsys := procFS([]fakeProc{{pid: 10, uid: 1001, comm: "a", rssPages: 1, startTime: 1}})
	p := &processes{}
	if d := collectProc(t, p, fsys, time.Time{}, procT0); d.Processes[0].User != "1001" {
		t.Errorf("User = %q, want the uid while /etc/passwd does not list it", d.Processes[0].User)
	}

	setUsers(fsys, map[int]string{0: "root", 1001: "bob"})
	if d := collectProc(t, p, fsys, procT0, procT0.Add(time.Second)); d.Processes[0].User != "bob" {
		t.Errorf("User = %q, want bob after the user was added", d.Processes[0].User)
	}

	// Cached: a passwd that goes away does not matter any more.
	delete(fsys, usersFile)
	if d := collectProc(t, p, fsys, procT0.Add(time.Second), procT0.Add(2*time.Second)); d.Processes[0].User != "bob" {
		t.Errorf("User = %q, want the cached bob", d.Processes[0].User)
	}
}

func TestProcessesEmpty(t *testing.T) {
	d := collectProc(t, &processes{}, fstest.MapFS{"proc/uptime": {Data: []byte("1 1\n")}}, time.Time{}, procT0)
	if d.Count != 0 || d.Processes == nil || len(d.Processes) != 0 {
		t.Errorf("data = %#v, want no processes and an empty list", d)
	}
}

func TestProcessesErrors(t *testing.T) {
	env := &metrics.Env{FS: fstest.MapFS{}, Now: procT0}
	if _, err := (&processes{}).Collect(context.Background(), env); err == nil {
		t.Error("Collect() without /proc: error = nil, want an error")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	env = &metrics.Env{FS: procFS([]fakeProc{{pid: 1, comm: "a"}}), Now: procT0}
	if _, err := (&processes{}).Collect(ctx, env); err == nil {
		t.Error("Collect() with a cancelled context: error = nil, want an error")
	}
}

func TestParseProcStat(t *testing.T) {
	line := "4242 (a) (b) c) R 1 4242 4242 0 -1 4194560 1200 0 3 0 70 30 5 5 20 0 3 0 12345 123456789 77 18446744073709551615\n"
	s, ok := parseProcStat(4242, []byte(line))
	want := procStat{
		pid: 4242, comm: "a) (b) c", state: "R", threads: 3,
		times: procTimes{startTime: 12345, ticks: 100}, rssPages: 77,
	}
	if !ok || !reflect.DeepEqual(s, want) {
		t.Errorf("parseProcStat = %+v, %v\nwant %+v, true", s, ok, want)
	}

	for name, line := range map[string]string{
		"empty":     "",
		"no comm":   "1 a S 1 2 3",
		"truncated": "1 (a) S 1 1 1 0 -1 4194560 1200 0 3 0 70 30",
		"garbage":   "1 (a) S 1 1 1 0 -1 4194560 1200 0 3 0 x 30 5 5 20 0 3 0 12345 123456789 77 0",
	} {
		if _, ok := parseProcStat(1, []byte(line)); ok {
			t.Errorf("%s: parseProcStat ok = true, want false", name)
		}
	}
}

func TestParseProcStatus(t *testing.T) {
	uid, rss, err := parseProcStatus([]byte("Name:\tx\nUid:\t1000\t0\t0\t0\nVmRSS:\t    6736 kB\n"))
	if err != nil || uid != 1000 || rss != 6736*1024 {
		t.Errorf("parseProcStatus = %d, %d, %v; want 1000, %d, nil", uid, rss, err, 6736*1024)
	}
	for name, status := range map[string]string{
		"empty":     "",
		"no uid":    "Name:\tx\nVmRSS:\t1 kB\n",
		"bad uid":   "Uid:\tx\t0\t0\t0\n",
		"bad vmrss": "Uid:\t0\t0\t0\t0\nVmRSS:\tx kB\n",
	} {
		if _, _, err := parseProcStatus([]byte(status)); err == nil {
			t.Errorf("%s: parseProcStatus error = nil, want an error", name)
		}
	}
}

// BenchmarkProcesses runs a sample over a generated /proc of 300 processes.
// It measures the parsing and ranking but not the kernel's cost of
// producing the files; BenchmarkProcessesLive covers that.
func BenchmarkProcesses(b *testing.B) {
	var procs []fakeProc
	for pid := 1; pid <= 300; pid++ {
		procs = append(procs, fakeProc{
			pid: pid * 7, uid: 1000 * (pid % 2), comm: fmt.Sprintf("proc%d", pid),
			ticks: uint64(pid * 13), rssPages: uint64(pid*31) % 1000, startTime: uint64(pid),
		})
	}
	fsys := procFS(procs)
	p := &processes{}
	now := procT0
	collectProc(b, p, fsys, time.Time{}, now)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		now = now.Add(time.Second)
		collectProc(b, p, fsys, now.Add(-time.Second), now)
	}
}

// BenchmarkProcessesLive samples the real /proc of the machine it runs on.
// The target is under 20 ms per sample on a Pi 5 with about 300 processes.
func BenchmarkProcessesLive(b *testing.B) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		b.Skip("no /proc on this system")
	}
	fsys := os.DirFS("/")
	p := &processes{}
	now := time.Now()
	collectProc(b, p, fsys, time.Time{}, now)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		prev := now
		now = time.Now()
		collectProc(b, p, fsys, prev, now)
	}
}
