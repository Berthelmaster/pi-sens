// Package metrics is the framework every collector plugs into.
//
// A Collector reads one area of the system (CPU, memory, temperature, ...) and
// returns a JSON section for the dashboard plus a flat set of numeric series
// for charts and history. Collectors register themselves with Register from an
// init function, and a Sampler runs all of them on a fixed interval and keeps
// the latest Snapshot in memory.
//
// Collectors never touch the real system directly: files are read through
// Env.FS and commands are run through Env.Runner, so tests can swap both for
// fixtures (see package collectors for the conventions).
package metrics

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"sync"
	"time"
)

// Collector gathers one section of the snapshot.
//
// The sampler never calls Collect on the same collector concurrently, so a
// collector may keep state between calls (for example the previous counter
// values for rate calculations) without its own locking. Collect should honour
// ctx: the sampler stops waiting when the per-collector timeout expires.
type Collector interface {
	// Name is the key of the collector's section in the snapshot. It must be
	// unique, lowercase and stable, e.g. "cpu" or "uptime".
	Name() string
	// Collect reads the current values.
	Collect(ctx context.Context, env *Env) (Result, error)
}

// Result is what a collector returns from one sample.
type Result struct {
	// Data is the collector's section in the snapshot. It must be JSON
	// serialisable; the sampler encodes it right after Collect returns, so the
	// collector may reuse or modify it afterwards.
	Data any
	// Series holds numeric values keyed "area.thing_unit", e.g.
	// "system.uptime_s". NaN and infinite values are dropped.
	Series map[string]float64
}

// Env gives collectors access to the system through injectable dependencies.
type Env struct {
	// FS is rooted at "/" in production, so /proc/uptime is read as
	// "proc/uptime". Tests use os.DirFS("testdata/<name>").
	FS fs.FS
	// Runner runs external commands such as vcgencmd.
	Runner CommandRunner
	// Now is the time of the current sample.
	Now time.Time
	// Prev is the Now of this collector's previous successful sample, or the
	// zero time on the first one. Use it for rates: delta / Now.Sub(Prev).
	Prev time.Time
}

var registry = struct {
	sync.Mutex
	collectors map[string]Collector
}{collectors: map[string]Collector{}}

// Register adds c to the global registry. It is meant to be called from an
// init function and panics if the name is empty or already taken.
func Register(c Collector) {
	name := c.Name()
	if name == "" {
		panic("metrics: Register called with an empty collector name")
	}
	registry.Lock()
	defer registry.Unlock()
	if _, dup := registry.collectors[name]; dup {
		panic(fmt.Sprintf("metrics: collector %q registered twice", name))
	}
	registry.collectors[name] = c
}

// Registered returns all registered collectors sorted by name.
func Registered() []Collector {
	registry.Lock()
	defer registry.Unlock()
	out := make([]Collector, 0, len(registry.collectors))
	for _, c := range registry.collectors {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}
