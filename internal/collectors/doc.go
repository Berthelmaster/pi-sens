// Package collectors holds the pisens collectors. main blank-imports it so
// every collector here is registered with package metrics at startup.
//
// Conventions:
//
//   - One file per collector, named after it (cpu.go, mem.go, ...), with its
//     tests in <name>_test.go. Adding a collector never edits a shared file.
//     The exception is sysfs.go, which holds the read helpers collectors share:
//     readValue and readInt for single-value files and numberedEntries for
//     <prefix><N> directory entries. Use them rather than a local copy, and
//     add a helper there only once a second collector needs it. names.go
//     likewise holds seriesName and uniqueName, which turn free text into
//     series key segments.
//
//   - Each collector registers itself from its own init function:
//
//     func init() { metrics.Register(&cpu{}) }
//
//   - Read files through env.FS, which is rooted at "/" ("proc/stat", not
//     "/proc/stat"), and run commands through env.Runner. Never use os or
//     os/exec directly, so tests can run anywhere.
//
//   - Test fixtures live in testdata/<name>/, laid out like the real root
//     filesystem (testdata/sysinfo/pi5/proc/uptime). Tests point env.FS at them
//     with os.DirFS and use metrics.FakeRunner for commands.
//
//   - Series keys are lowercase, dotted and end in a unit suffix:
//     area.thing_unit, e.g. cpu.usage_pct, mem.used_bytes, temp.cpu_c,
//     net.eth0.rx_bps, system.uptime_s.
//
//   - A collector may keep state between calls for rate calculations; the
//     sampler never runs the same collector concurrently. Use env.Now and
//     env.Prev for the elapsed time.
//
//   - Return an error rather than partial nonsense; the sampler shows it as
//     {"error": "..."} in the collector's section and keeps the others.
//
// cpufreq.go is a small reference implementation; cpu.go shows one that keeps
// state between samples.
package collectors
