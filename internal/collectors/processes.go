package collectors

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

func init() { metrics.Register(&processes{}) }

// processes reports the processes using the most CPU and the most memory,
// read from /proc/<pid>/stat, status and cmdline.
//
// CPU usage is the user and system time a process used between two samples
// divided by the time between them, so 100 means one full core and a busy
// multi-threaded process can go above it (like top). The first sample has
// nothing to compare against and has no CPU values. Neither has a process
// that started since the previous sample, or a pid that was reused by a new
// process in between: their current times become the baseline for the next
// sample.
//
// To keep a sample cheap only /proc/<pid>/stat is read for every process. It
// holds the CPU times and the resident set size in pages, which is enough to
// rank them. status (owner and VmRSS in bytes) and cmdline are only read for
// the processes that make the list. A process that exits while it is being
// read is skipped.
//
// There are no series: pids come and go, so they would not make a stable set
// of keys.
type processes struct {
	prev map[int]procTimes // keyed by pid
	// users caches /etc/passwd, uid to name. It is loaded on first use and
	// reloaded, at most once per sample, when a uid is not in it.
	users map[int]string

	statBuf []byte // reused between samples for /proc/<pid>/stat
}

const (
	// procTop is how many processes are picked by CPU and by memory. A
	// process that is in both lists is reported once.
	procTop = 15
	// procClockTicks is USER_HZ, the unit of the times in /proc/<pid>/stat.
	// It is 100 on every architecture Linux runs on the Pi.
	procClockTicks = 100
	// procMaxCmdline caps the command line read for a process; some (Java,
	// Node) run with kilobytes of arguments.
	procMaxCmdline = 1024
)

type processesData struct {
	// Count is the number of processes read this sample.
	Count int `json:"count"`
	// Processes is the union of the top procTop by CPU and by memory,
	// sorted by CPU (highest first), then memory, then pid. Empty when
	// there are none.
	Processes []procInfo `json:"processes"`
}

type procInfo struct {
	PID int `json:"pid"`
	// Name is the kernel's short name for the process (comm), at most 15
	// characters.
	Name string `json:"name"`
	// Command is the full command line, its arguments separated by spaces
	// and cut at procMaxCmdline bytes. Kernel threads and zombies have none
	// and show their name in brackets, as ps does: "[kworker/0:1]".
	Command string `json:"command"`
	// User is the name of the real uid, or the uid itself if /etc/passwd
	// does not list it.
	User string `json:"user"`
	// State is the one-letter state from stat: R running, S sleeping, D
	// waiting on I/O, Z zombie, ...
	State   string `json:"state"`
	Threads int    `json:"threads"`
	// CPUPct is omitted on the collector's first sample and for a process
	// that started since the previous one.
	CPUPct   *float64 `json:"cpu_pct,omitempty"`
	RSSBytes uint64   `json:"rss_bytes"`
}

// procTimes identifies a process across samples and holds its CPU time.
type procTimes struct {
	startTime uint64 // ticks after boot; a reused pid has a different one
	ticks     uint64 // utime + stime
}

// procStat is what one /proc/<pid>/stat gives us.
type procStat struct {
	pid      int
	comm     string
	state    string
	threads  int
	times    procTimes
	rssPages uint64
	cpuPct   float64
	hasCPU   bool
}

func (*processes) Name() string { return "processes" }

func (p *processes) Collect(ctx context.Context, env *metrics.Env) (metrics.Result, error) {
	entries, err := fs.ReadDir(env.FS, "proc")
	if err != nil {
		return metrics.Result{}, err
	}

	// CPU usage needs a previous sample of this collector and time in
	// between.
	var secs float64
	if p.prev != nil && !env.Prev.IsZero() {
		secs = env.Now.Sub(env.Prev).Seconds()
	}

	if p.statBuf == nil {
		p.statBuf = make([]byte, 4096)
	}
	all := make([]procStat, 0, len(entries))
	cur := make(map[int]procTimes, len(entries))
	for i, e := range entries {
		if i%64 == 0 {
			if err := ctx.Err(); err != nil {
				return metrics.Result{}, err
			}
		}
		pid, ok := procPID(e.Name())
		if !ok {
			continue
		}
		b, err := procReadFile(env.FS, "proc/"+e.Name()+"/stat", p.statBuf)
		if err != nil {
			continue // gone already
		}
		s, ok := parseProcStat(pid, b)
		if !ok {
			continue // gone half way, the kernel gave us an empty file
		}
		cur[pid] = s.times
		if prev, seen := p.prev[pid]; seen && secs > 0 &&
			prev.startTime == s.times.startTime && s.times.ticks >= prev.ticks {
			s.cpuPct = round2(float64(s.times.ticks-prev.ticks) / procClockTicks / secs * 100)
			s.hasCPU = true
		}
		all = append(all, s)
	}
	p.prev = cur

	data := processesData{Count: len(all), Processes: []procInfo{}}
	reloaded := false
	for _, s := range procPickTop(all) {
		info, err := p.details(env.FS, s, &reloaded)
		if err != nil {
			continue // exited since we read its stat
		}
		data.Processes = append(data.Processes, info)
	}
	sort.Slice(data.Processes, func(i, j int) bool {
		a, b := data.Processes[i], data.Processes[j]
		ca, cb := -1.0, -1.0
		if a.CPUPct != nil {
			ca = *a.CPUPct
		}
		if b.CPUPct != nil {
			cb = *b.CPUPct
		}
		if ca != cb {
			return ca > cb
		}
		if a.RSSBytes != b.RSSBytes {
			return a.RSSBytes > b.RSSBytes
		}
		return a.PID < b.PID
	})
	return metrics.Result{Data: data}, nil
}

// procPickTop returns the procTop processes using the most CPU and the
// procTop using the most memory, each process once. Processes that used no
// CPU since the last sample are not picked for CPU, so an idle system lists
// only the memory top.
func procPickTop(all []procStat) []procStat {
	byCPU := make([]int, 0, len(all))
	byMem := make([]int, 0, len(all))
	for i := range all {
		if all[i].hasCPU && all[i].cpuPct > 0 {
			byCPU = append(byCPU, i)
		}
		byMem = append(byMem, i)
	}
	// Ties go to the lower pid so the pick does not depend on the order
	// /proc lists them in.
	sort.Slice(byCPU, func(i, j int) bool {
		a, b := all[byCPU[i]], all[byCPU[j]]
		if a.cpuPct != b.cpuPct {
			return a.cpuPct > b.cpuPct
		}
		return a.pid < b.pid
	})
	sort.Slice(byMem, func(i, j int) bool {
		a, b := all[byMem[i]], all[byMem[j]]
		if a.rssPages != b.rssPages {
			return a.rssPages > b.rssPages
		}
		return a.pid < b.pid
	})

	picked := make(map[int]bool, 2*procTop)
	out := make([]procStat, 0, 2*procTop)
	for _, list := range [][]int{byCPU, byMem} {
		for _, i := range list[:min(procTop, len(list))] {
			if !picked[i] {
				picked[i] = true
				out = append(out, all[i])
			}
		}
	}
	return out
}

// details reads status and cmdline of a picked process. It fails when the
// process is gone.
func (p *processes) details(fsys fs.FS, s procStat, reloaded *bool) (procInfo, error) {
	dir := "proc/" + strconv.Itoa(s.pid) + "/"
	status, err := fs.ReadFile(fsys, dir+"status")
	if err != nil {
		return procInfo{}, err
	}
	uid, rss, err := parseProcStatus(status)
	if err != nil {
		return procInfo{}, err
	}
	cmd, err := procReadFile(fsys, dir+"cmdline", make([]byte, procMaxCmdline))
	if err != nil {
		return procInfo{}, err
	}

	info := procInfo{
		PID:      s.pid,
		Name:     s.comm,
		Command:  procCommand(cmd, s.comm),
		User:     p.userName(fsys, uid, reloaded),
		State:    s.state,
		Threads:  s.threads,
		RSSBytes: rss,
	}
	if s.hasCPU {
		v := s.cpuPct
		info.CPUPct = &v
	}
	return info, nil
}

// procCommand turns the NUL-separated cmdline into one line, falling back to
// "[comm]" when it is empty.
func procCommand(cmdline []byte, comm string) string {
	cmd := strings.TrimSpace(strings.ReplaceAll(string(cmdline), "\x00", " "))
	if cmd == "" {
		return "[" + comm + "]"
	}
	return strings.ToValidUTF8(cmd, "�")
}

// userName resolves uid through /etc/passwd. A uid that is not in the cache
// reloads it, at most once per sample (reloaded), in case the user was added
// since; a uid that is still unknown is shown as the number.
func (p *processes) userName(fsys fs.FS, uid int, reloaded *bool) string {
	if name, ok := p.users[uid]; ok {
		return name
	}
	if p.users == nil || !*reloaded {
		*reloaded = true
		p.users = readPasswd(fsys)
		if name, ok := p.users[uid]; ok {
			return name
		}
	}
	return strconv.Itoa(uid)
}

// readPasswd maps uid to user name from /etc/passwd lines like
// "pi:x:1000:1000:,,,:/home/pi:/bin/bash". A missing or unreadable file gives
// an empty map, so every user shows as a number.
func readPasswd(fsys fs.FS) map[int]string {
	users := map[int]string{}
	b, err := fs.ReadFile(fsys, "etc/passwd")
	if err != nil {
		return users
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.SplitN(sc.Text(), ":", 4)
		if len(f) < 4 || f[0] == "" {
			continue
		}
		uid, err := strconv.Atoi(f[2])
		if err != nil {
			continue
		}
		if _, dup := users[uid]; !dup { // the first entry wins, as in getpwuid
			users[uid] = f[0]
		}
	}
	return users
}

// procPID reports whether a /proc entry is a process directory.
func procPID(name string) (int, bool) {
	if name == "" || name[0] < '1' || name[0] > '9' {
		return 0, false
	}
	pid, err := strconv.Atoi(name)
	return pid, err == nil
}

// procReadFile reads name into buf and returns the part it filled. Reading
// stops when buf is full, so the content may be cut.
func procReadFile(fsys fs.FS, name string, buf []byte) ([]byte, error) {
	f, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	return buf[:n], nil
}

// parseProcStat parses /proc/<pid>/stat:
//
//	1234 (comm) S ppid pgrp session tty tpgid flags minflt cminflt majflt
//	cmajflt utime stime cutime cstime priority nice num_threads itrealvalue
//	starttime vsize rss ...
//
// comm can hold spaces and parentheses, so it runs to the last ')'. ok is
// false for anything shorter than rss.
func parseProcStat(pid int, b []byte) (s procStat, ok bool) {
	open := bytes.IndexByte(b, '(')
	end := bytes.LastIndexByte(b, ')')
	if open < 0 || end < open {
		return procStat{}, false
	}
	s.pid = pid
	s.comm = string(b[open+1 : end])

	// Fields after comm, counted from 0: state is 0, utime 11, stime 12,
	// num_threads 17, starttime 19 and rss 21.
	var v [22]uint64
	rest := b[end+1:]
	for i := 0; i < len(v); i++ {
		rest = bytes.TrimLeft(rest, " ")
		n := bytes.IndexByte(rest, ' ')
		if n < 0 {
			n = len(rest)
		}
		field := bytes.TrimRight(rest[:n], "\n")
		if len(field) == 0 {
			return procStat{}, false
		}
		switch i {
		case 0:
			s.state = string(field)
		case 11, 12, 17, 19, 21:
			var err error
			if v[i], err = strconv.ParseUint(string(field), 10, 64); err != nil {
				return procStat{}, false
			}
		}
		rest = rest[n:]
	}
	s.times = procTimes{startTime: v[19], ticks: v[11] + v[12]}
	s.threads = int(v[17])
	s.rssPages = v[21]
	return s, true
}

// parseProcStatus returns the real uid and VmRSS in bytes from
// /proc/<pid>/status. Kernel threads have no VmRSS line; their RSS is 0.
func parseProcStatus(b []byte) (uid int, rss uint64, err error) {
	foundUID := false
	for len(b) > 0 {
		line := b
		if n := bytes.IndexByte(b, '\n'); n >= 0 {
			line, b = b[:n], b[n+1:]
		} else {
			b = nil
		}
		key, val, ok := bytes.Cut(line, []byte(":"))
		if !ok {
			continue
		}
		switch string(key) {
		case "Uid":
			f := bytes.Fields(val)
			if len(f) == 0 {
				return 0, 0, fmt.Errorf("status: empty Uid line")
			}
			if uid, err = strconv.Atoi(string(f[0])); err != nil {
				return 0, 0, fmt.Errorf("status: Uid: %w", err)
			}
			foundUID = true
		case "VmRSS":
			f := bytes.Fields(val) // "6736 kB"
			if len(f) == 0 {
				return 0, 0, fmt.Errorf("status: empty VmRSS line")
			}
			kb, err := strconv.ParseUint(string(f[0]), 10, 64)
			if err != nil {
				return 0, 0, fmt.Errorf("status: VmRSS: %w", err)
			}
			rss = kb * 1024
		}
	}
	if !foundUID {
		return 0, 0, fmt.Errorf("status: no Uid line")
	}
	return uid, rss, nil
}
