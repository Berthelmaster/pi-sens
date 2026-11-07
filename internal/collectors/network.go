package collectors

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

func init() { metrics.Register(&network{skip: netDefaultSkip}) }

// network reports throughput per network interface from /proc/net/dev.
//
// /proc/net/dev holds cumulative counters since the interface came up, so the
// rates are the difference between two samples divided by the time between
// them. The first sample has nothing to compare against and omits the rates.
// So does an interface that just appeared, or one whose counters went
// backwards (it was removed and re-created under the same name, or a driver
// with 32-bit counters wrapped): its current counters become the baseline for
// the next sample instead of producing a spike. Interfaces that disappear are
// simply no longer reported.
//
// The totals add up the interfaces that are reported, so loopback and the
// virtual interfaces container runtimes create (skipped by default) are not
// counted twice next to the physical interface their traffic leaves through.
type network struct {
	// skip reports whether an interface is left out entirely.
	skip func(iface string) bool
	prev map[string]netCounters
}

// netDefaultSkip leaves out loopback and the per-container veth pairs and
// bridges Docker creates.
func netDefaultSkip(iface string) bool {
	return iface == "lo" ||
		strings.HasPrefix(iface, "veth") ||
		strings.HasPrefix(iface, "docker") ||
		strings.HasPrefix(iface, "br-")
}

type networkData struct {
	// Interfaces is sorted by name; empty when there are none.
	Interfaces []netInterface `json:"interfaces"`
	Total      netTotal       `json:"total"`
}

type netInterface struct {
	Name string `json:"name"`
	netCounters
	// Rates is omitted on the first sample of the interface and after its
	// counters were reset.
	Rates *netRates `json:"rates,omitempty"`
}

// netTotal sums the reported interfaces. Rates only covers the interfaces
// that have rates this sample; it is omitted on the collector's first sample.
type netTotal struct {
	netCounters
	Rates *netRates `json:"rates,omitempty"`
}

// netCounters are the cumulative /proc/net/dev counters we report.
type netCounters struct {
	RxBytes   uint64 `json:"rx_bytes"`
	RxPackets uint64 `json:"rx_packets"`
	RxErrors  uint64 `json:"rx_errors"`
	RxDropped uint64 `json:"rx_dropped"`
	TxBytes   uint64 `json:"tx_bytes"`
	TxPackets uint64 `json:"tx_packets"`
	TxErrors  uint64 `json:"tx_errors"`
	TxDropped uint64 `json:"tx_dropped"`
}

func (c *netCounters) add(o netCounters) {
	c.RxBytes += o.RxBytes
	c.RxPackets += o.RxPackets
	c.RxErrors += o.RxErrors
	c.RxDropped += o.RxDropped
	c.TxBytes += o.TxBytes
	c.TxPackets += o.TxPackets
	c.TxErrors += o.TxErrors
	c.TxDropped += o.TxDropped
}

// netRates are per second. Bps is bytes, not bits, per second.
type netRates struct {
	RxBps float64 `json:"rx_bps"`
	TxBps float64 `json:"tx_bps"`
	RxPps float64 `json:"rx_pps"`
	TxPps float64 `json:"tx_pps"`
}

func (*network) Name() string { return "network" }

func (n *network) Collect(_ context.Context, env *metrics.Env) (metrics.Result, error) {
	all, err := readProcNetDev(env.FS)
	if err != nil {
		return metrics.Result{}, err
	}
	cur := make(map[string]netCounters, len(all))
	for name, c := range all {
		if n.skip == nil || !n.skip(name) {
			cur[name] = c
		}
	}

	// Rates need a previous sample of this collector and time in between.
	var secs float64
	if n.prev != nil && !env.Prev.IsZero() {
		secs = env.Now.Sub(env.Prev).Seconds()
	}

	data := networkData{Interfaces: make([]netInterface, 0, len(cur))}
	series := map[string]float64{}
	var total netRates
	keys := netSeriesNames(cur)
	for _, name := range netSortedNames(cur) {
		c := cur[name]
		iface := netInterface{Name: name, netCounters: c}
		data.Total.add(c)
		if r, ok := n.ratesSince(name, c, secs); ok {
			iface.Rates = &r
			total.RxBps += r.RxBps
			total.TxBps += r.TxBps
			total.RxPps += r.RxPps
			total.TxPps += r.TxPps
			series["net."+keys[name]+".rx_bps"] = r.RxBps
			series["net."+keys[name]+".tx_bps"] = r.TxBps
		}
		data.Interfaces = append(data.Interfaces, iface)
	}
	if secs > 0 {
		total = netRates{
			RxBps: round2(total.RxBps), TxBps: round2(total.TxBps),
			RxPps: round2(total.RxPps), TxPps: round2(total.TxPps),
		}
		data.Total.Rates = &total
		series["net.total.rx_bps"] = total.RxBps
		series["net.total.tx_bps"] = total.TxBps
	}

	n.prev = cur
	if len(series) == 0 {
		series = nil
	}
	return metrics.Result{Data: data, Series: series}, nil
}

// ratesSince returns the rates of an interface since the previous sample,
// taken secs ago. ok is false when the interface was not there last time, no
// time has passed or a counter went backwards.
func (n *network) ratesSince(name string, cur netCounters, secs float64) (r netRates, ok bool) {
	prev, seen := n.prev[name]
	if !seen || secs <= 0 ||
		cur.RxBytes < prev.RxBytes || cur.TxBytes < prev.TxBytes ||
		cur.RxPackets < prev.RxPackets || cur.TxPackets < prev.TxPackets {
		return netRates{}, false
	}
	rate := func(p, c uint64) float64 { return round2(float64(c-p) / secs) }
	return netRates{
		RxBps: rate(prev.RxBytes, cur.RxBytes),
		TxBps: rate(prev.TxBytes, cur.TxBytes),
		RxPps: rate(prev.RxPackets, cur.RxPackets),
		TxPps: rate(prev.TxPackets, cur.TxPackets),
	}, true
}

// netSeriesNames maps each interface to the name used in its series keys,
// net.<name>.rx_bps. Interface names can contain characters that do not
// belong in a dotted key (a VLAN is "eth0.100"), so everything other than
// a-z, 0-9 and _ becomes _. "total" is reserved for the sums, and a name that
// is already taken gets _2, _3, ... appended.
func netSeriesNames(ifaces map[string]netCounters) map[string]string {
	taken := map[string]bool{"total": true}
	out := make(map[string]string, len(ifaces))
	for _, name := range netSortedNames(ifaces) {
		base := strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
				return r
			}
			return '_'
		}, strings.ToLower(name))
		unique := base
		for i := 2; taken[unique]; i++ {
			unique = base + "_" + strconv.Itoa(i)
		}
		taken[unique] = true
		out[name] = unique
	}
	return out
}

func netSortedNames[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// readProcNetDev parses /proc/net/dev, keyed by interface name:
//
//	Inter-|   Receive                                                |  Transmit
//	 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
//	  eth0: 620372707 1139193    0    0    0     0          0         0 251385308  895725    0    0    0     0       0          0
//
// Old kernels print no space after the colon once the byte counter is wide.
func readProcNetDev(fsys fs.FS) (map[string]netCounters, error) {
	b, err := fs.ReadFile(fsys, "proc/net/dev")
	if err != nil {
		return nil, err
	}
	out := map[string]netCounters{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := sc.Text()
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue // the two header lines use | rather than :
		}
		name = strings.TrimSpace(name)
		fields := strings.Fields(rest)
		if name == "" || len(fields) < 16 {
			return nil, fmt.Errorf("proc/net/dev: malformed line %q", strings.TrimSpace(line))
		}
		var v [12]uint64
		for i := range v {
			if v[i], err = strconv.ParseUint(fields[i], 10, 64); err != nil {
				return nil, fmt.Errorf("proc/net/dev: %s: %w", name, err)
			}
		}
		out[name] = netCounters{
			RxBytes: v[0], RxPackets: v[1], RxErrors: v[2], RxDropped: v[3],
			TxBytes: v[8], TxPackets: v[9], TxErrors: v[10], TxDropped: v[11],
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("proc/net/dev: %w", err)
	}
	return out, nil
}
