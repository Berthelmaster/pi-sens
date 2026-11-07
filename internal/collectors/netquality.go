package collectors

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"math"
	"math/rand/v2"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

// netqCollector is the registered instance, kept so StartNetQuality can feed
// it.
var netqCollector = &netquality{}

func init() { metrics.Register(netqCollector) }

// NetQualityOptions configures StartNetQuality.
type NetQualityOptions struct {
	// Targets are the internet hosts to probe: IPv4 addresses or host names
	// that resolve to one. The default gateway is always probed as well.
	Targets []string
	// Interval is the time between two probe rounds.
	Interval time.Duration
	// DNSName is the host name looked up every round to time the DNS
	// resolver. Empty means no lookups.
	DNSName string
	// FS is the root filesystem, for proc/net/route and etc/resolv.conf.
	FS fs.FS
	// Logger gets the probe method and why ICMP is not used, once each.
	Logger *slog.Logger
}

// StartNetQuality starts probing the default gateway and opts.Targets in a
// background goroutine, a burst of pings and a lookup of opts.DNSName every
// opts.Interval, for the netquality collector to report. The goroutine stops
// when ctx is done; the returned channel is closed once it has. Without a
// call the netquality section is {"available": false}.
func StartNetQuality(ctx context.Context, opts NetQualityOptions) <-chan struct{} {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	opts.Logger = logger
	return netqCollector.start(ctx, opts, newNetqNetProber(logger))
}

// Probe settings. A round sends netqCount pings to every target at once, one
// after the other per target, netqSpacing apart (the shortest interval ping
// allows an unprivileged user). A ping without an answer within
// netqTimeout is lost.
const (
	netqCount   = 5
	netqSpacing = 200 * time.Millisecond
	netqTimeout = time.Second
)

// netqDNSTimeout is how long a DNS lookup may take before it counts as
// failed. It is longer than a ping's: a resolver that has to ask upstream
// takes a few round trips.
const netqDNSTimeout = 2 * time.Second

// netqSlowMs is the average round trip time, in milliseconds, above which a
// target makes the status degraded.
const netqSlowMs = 150

// Status values.
const (
	netqGood     = "good"
	netqDegraded = "degraded"
	netqDown     = "down"
	netqPending  = "pending"
)

// Target kinds.
const (
	netqGateway  = "gateway"
	netqInternet = "internet"
)

// netqDNSKey is the series key segment of the DNS lookups: netq.dns.lookup_ms.
// No target gets it.
const netqDNSKey = "dns"

// TCP ports for the fallback when ICMP is not allowed. Every target answers
// one of them, if only with a reset: most routers run DNS or a web interface.
var (
	netqGatewayPorts  = []int{53, 80}
	netqInternetPorts = []int{443}
)

// netquality reports latency, jitter and packet loss to the default gateway
// and a few internet hosts, so a flaky network or internet connection shows
// up in the history.
//
// Pinging takes up to seconds when the network is bad, far longer than a
// sample may take, so it happens in a goroutine of its own (see
// StartNetQuality) and Collect only returns the latest round.
//
// Pings are unprivileged ICMP echo requests ("udp4" ICMP sockets), which
// Linux allows for the groups in net.ipv4.ping_group_range; systemd opens
// that to every group. Where the kernel refuses them, the probes fall back to
// timing a TCP connect, which needs no privileges either: port 443 for
// internet targets and 53, then 80, for the gateway. A refused connection
// still took one round trip, so it counts as an answer. Each target reports
// the method it was measured with.
//
// Every round also looks up a host name (see netqNetProber.LookupDNS), as
// many "slow internet" moments are really a slow or broken DNS resolver.
type netquality struct {
	started  atomic.Bool
	interval atomic.Int64 // time.Duration
	latest   atomic.Pointer[netqRound]
}

// netqRound is the result of one probe round. It is never changed after it
// is stored.
type netqRound struct {
	at      time.Time
	status  string
	targets []netqTargetData
	dns     *netqDNSData
	series  map[string]float64
}

type netqData struct {
	Available bool `json:"available"`
	// Status is good, degraded or down, or pending until the first round
	// is done.
	Status    string           `json:"status,omitempty"`
	IntervalS float64          `json:"interval_s,omitempty"`
	ProbedAt  *time.Time       `json:"probed_at,omitempty"`
	Targets   []netqTargetData `json:"targets,omitempty"`
	// DNS is the latest lookup; nil when DNS lookups are off.
	DNS *netqDNSData `json:"dns,omitempty"`
}

type netqDNSData struct {
	// Name is the host name looked up.
	Name string `json:"name"`
	// Resolver is the nameserver that was asked, from /etc/resolv.conf,
	// e.g. "192.168.1.1:53".
	Resolver string `json:"resolver,omitempty"`
	// OK is whether the name resolved to an IPv4 address.
	OK       bool     `json:"ok"`
	LookupMs *float64 `json:"lookup_ms"`
	// Failures counts the failed lookups since pisens started.
	Failures int `json:"failures"`
	// LastOK is the last round in which the lookup succeeded.
	LastOK *time.Time `json:"last_ok"`
	Error  string     `json:"error,omitempty"`
}

type netqTargetData struct {
	// Name is "gateway" or the target as configured, e.g. "1.1.1.1".
	Name string `json:"name"`
	// Key is the series key segment: netq.<key>.latency_ms.
	Key  string `json:"key"`
	Kind string `json:"kind"`
	// Address is the IPv4 address that was probed.
	Address string `json:"address,omitempty"`
	// Method is icmp or tcp.
	Method    string   `json:"method,omitempty"`
	LatencyMs *float64 `json:"latency_ms"`
	JitterMs  *float64 `json:"jitter_ms"`
	LossPct   float64  `json:"loss_pct"`
	// LastOK is the last round in which at least one ping was answered.
	LastOK *time.Time `json:"last_ok"`
	Error  string     `json:"error,omitempty"`
}

func (*netquality) Name() string { return "netquality" }

func (n *netquality) Collect(context.Context, *metrics.Env) (metrics.Result, error) {
	if !n.started.Load() {
		return metrics.Result{Data: netqData{Available: false}}, nil
	}
	intervalS := time.Duration(n.interval.Load()).Seconds()
	r := n.latest.Load()
	if r == nil {
		return metrics.Result{Data: netqData{Available: true, Status: netqPending, IntervalS: intervalS}}, nil
	}
	at := r.at
	data := netqData{
		Available: true,
		Status:    r.status,
		IntervalS: intervalS,
		ProbedAt:  &at,
		Targets:   r.targets,
		DNS:       r.dns,
	}
	return metrics.Result{Data: data, Series: maps.Clone(r.series)}, nil
}

// netqProber sends the pings of one target and the DNS lookups.
type netqProber interface {
	// Probe sends count pings to host and returns what came back. ports
	// are the TCP ports to time instead when ICMP is not allowed.
	Probe(ctx context.Context, host string, count int, ports []int) netqBurst
	// LookupDNS asks server for the IPv4 address of name and returns how
	// long the answer took.
	LookupDNS(ctx context.Context, server netip.AddrPort, name string) netqLookup
}

// netqLookup is what one LookupDNS got back.
type netqLookup struct {
	// Duration is the time from sending the query to the answer; only set
	// when Err is nil.
	Duration time.Duration
	// Err says why the name did not resolve: a timeout, NXDOMAIN, no IPv4
	// address.
	Err error
}

// netqBurst is what one Probe got back.
type netqBurst struct {
	// Addr is the address host resolved to; invalid if it did not.
	Addr netip.Addr
	// Method is icmp or tcp.
	Method string
	// Sent is the number of pings sent.
	Sent int
	// RTTs are the round trip times of the answered pings, in the order
	// they were sent.
	RTTs []time.Duration
	// Err says why pings were lost, when that is known: the host did not
	// resolve, a socket failed.
	Err error
}

// netqTarget is one configured target.
type netqTarget struct {
	name  string
	key   string
	kind  string
	ports []int
}

// netqTargets returns the gateway followed by the internet targets, each
// with a series key segment of its own.
func netqTargets(hosts []string) []netqTarget {
	taken := map[string]bool{netqGateway: true, netqDNSKey: true}
	targets := []netqTarget{{name: netqGateway, key: netqGateway, kind: netqGateway, ports: netqGatewayPorts}}
	for _, h := range hosts {
		key := seriesName(h, collapseSeparators)
		if key == "" {
			key = "target"
		}
		targets = append(targets, netqTarget{
			name:  h,
			key:   uniqueName(taken, key),
			kind:  netqInternet,
			ports: netqInternetPorts,
		})
	}
	return targets
}

// start runs probe rounds with p until ctx is done, the first one right
// away. The returned channel is closed when the goroutine has stopped.
func (n *netquality) start(ctx context.Context, opts NetQualityOptions, p netqProber) <-chan struct{} {
	targets := netqTargets(opts.Targets)
	n.latest.Store(nil)
	n.interval.Store(int64(opts.Interval))
	n.started.Store(true)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(opts.Interval)
		defer ticker.Stop()
		for {
			n.round(ctx, opts.FS, targets, opts.DNSName, p, time.Now)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return done
}

// round probes every target and looks up dnsName, unless it is empty, all
// at once, and stores the result.
func (n *netquality) round(ctx context.Context, fsys fs.FS, targets []netqTarget, dnsName string, p netqProber, now func() time.Time) {
	gateway, gwErr := defaultGateway(fsys)
	var (
		resolver netip.AddrPort
		lookup   netqLookup
		nsErr    error
	)
	if dnsName != "" {
		resolver, nsErr = netqNameserver(fsys)
		lookup.Err = nsErr
	}

	bursts := make([]netqBurst, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		host := t.name
		if t.kind == netqGateway {
			if gwErr != nil {
				bursts[i] = netqBurst{Err: gwErr}
				continue
			}
			host = gateway.String()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			bursts[i] = p.Probe(ctx, host, netqCount, t.ports)
		}()
	}
	if dnsName != "" && nsErr == nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lookup = p.LookupDNS(ctx, resolver, dnsName)
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		// Shutting down: the pings were cut short, not lost.
		return
	}

	at := now().UTC()
	prev := map[string]*time.Time{}
	last := n.latest.Load()
	if last != nil {
		for _, t := range last.targets {
			prev[t.Key] = t.LastOK
		}
	}
	r := &netqRound{at: at, targets: make([]netqTargetData, len(targets)), series: map[string]float64{}}
	for i, t := range targets {
		b := bursts[i]
		latency, jitter, loss := netqSummarise(b)
		d := netqTargetData{
			Name:      t.name,
			Key:       t.key,
			Kind:      t.kind,
			Method:    b.Method,
			LatencyMs: latency,
			JitterMs:  jitter,
			LossPct:   loss,
			LastOK:    prev[t.key],
		}
		if b.Addr.IsValid() {
			d.Address = b.Addr.String()
		}
		if len(b.RTTs) > 0 {
			d.LastOK = &at
		} else if b.Err != nil {
			d.Error = b.Err.Error()
		}
		r.targets[i] = d
		if latency != nil {
			r.series["netq."+t.key+".latency_ms"] = *latency
		}
		r.series["netq."+t.key+".loss_pct"] = loss
	}
	if dnsName != "" {
		d := &netqDNSData{Name: dnsName, OK: lookup.Err == nil}
		if last != nil && last.dns != nil {
			d.Failures, d.LastOK = last.dns.Failures, last.dns.LastOK
		}
		if resolver.IsValid() {
			d.Resolver = resolver.String()
		}
		if d.OK {
			ms := netqRound3(netqMs(lookup.Duration))
			d.LookupMs = &ms
			d.LastOK = &at
			r.series["netq."+netqDNSKey+".lookup_ms"] = ms
		} else {
			d.Failures++
			d.Error = lookup.Err.Error()
		}
		r.series["netq."+netqDNSKey+".failures"] = float64(d.Failures)
		r.dns = d
	}
	r.status = netqStatus(r.targets, r.dns)
	n.latest.Store(r)
}

// netqSummarise returns the average round trip time, the jitter and the
// packet loss of a burst. Latency is nil without an answer and jitter is nil
// with fewer than two. The jitter is the mean difference between the round
// trip times of consecutive answers, as ping's mdev and RFC 3550 look at it:
// how much the delay moves from one packet to the next.
func netqSummarise(b netqBurst) (latencyMs, jitterMs *float64, lossPct float64) {
	lossPct = 100
	if b.Sent > 0 {
		lost := max(b.Sent-len(b.RTTs), 0)
		lossPct = netqRound2(float64(lost) / float64(b.Sent) * 100)
	}
	if len(b.RTTs) == 0 {
		return nil, nil, lossPct
	}
	var sum float64
	for _, rtt := range b.RTTs {
		sum += netqMs(rtt)
	}
	avg := netqRound3(sum / float64(len(b.RTTs)))
	latencyMs = &avg
	if len(b.RTTs) >= 2 {
		var diff float64
		for i := 1; i < len(b.RTTs); i++ {
			diff += math.Abs(netqMs(b.RTTs[i]) - netqMs(b.RTTs[i-1]))
		}
		j := netqRound3(diff / float64(len(b.RTTs)-1))
		jitterMs = &j
	}
	return latencyMs, jitterMs, lossPct
}

// netqStatus is down when every internet target lost every ping (or, with
// no internet targets, the gateway did), degraded when any target lost a
// ping or averaged over netqSlowMs or the DNS lookup failed, and good
// otherwise. dns is nil when there was no lookup.
func netqStatus(targets []netqTargetData, dns *netqDNSData) string {
	degraded := dns != nil && !dns.OK
	internet, internetDown := 0, 0
	for _, t := range targets {
		if t.LossPct > 0 || (t.LatencyMs != nil && *t.LatencyMs > netqSlowMs) {
			degraded = true
		}
		if t.Kind == netqInternet {
			internet++
			if t.LossPct >= 100 {
				internetDown++
			}
		}
	}
	if internet > 0 && internetDown == internet {
		return netqDown
	}
	if internet == 0 {
		for _, t := range targets {
			if t.Kind == netqGateway && t.LossPct >= 100 {
				return netqDown
			}
		}
	}
	if degraded {
		return netqDegraded
	}
	return netqGood
}

func netqMs(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

func netqRound2(v float64) float64 { return math.Round(v*100) / 100 }

func netqRound3(v float64) float64 { return math.Round(v*1000) / 1000 }

// RTF_UP and RTF_GATEWAY from linux/route.h.
const (
	rtfUp      = 0x1
	rtfGateway = 0x2
)

// errNoDefaultRoute is the gateway's error when the routing table has no
// default route, which usually means the Pi is not connected.
var errNoDefaultRoute = errors.New("no default route")

// defaultGateway returns the IPv4 default gateway from /proc/net/route: the
// gateway of the usable 0.0.0.0/0 route with the lowest metric. The file
// shows addresses as the hex of their bytes in host order, which is little
// endian on every Pi; the kernel writes them with %08X of the raw value.
func defaultGateway(fsys fs.FS) (netip.Addr, error) {
	data, err := fs.ReadFile(fsys, "proc/net/route")
	if err != nil {
		return netip.Addr{}, err
	}
	var best netip.Addr
	bestMetric := -1
	s := bufio.NewScanner(bytes.NewReader(data))
	header := true
	for s.Scan() {
		if header { // Iface Destination Gateway Flags RefCnt Use Metric Mask ...
			header = false
			continue
		}
		f := strings.Fields(s.Text())
		if len(f) < 8 {
			continue
		}
		flags, err := strconv.ParseUint(f[3], 16, 32)
		if err != nil || flags&(rtfUp|rtfGateway) != rtfUp|rtfGateway {
			continue
		}
		if f[1] != "00000000" || f[7] != "00000000" {
			continue
		}
		gw, err := netqHexAddr(f[2])
		if err != nil || gw.IsUnspecified() {
			continue
		}
		metric, err := strconv.Atoi(f[6])
		if err != nil {
			continue
		}
		if bestMetric < 0 || metric < bestMetric {
			best, bestMetric = gw, metric
		}
	}
	if err := s.Err(); err != nil {
		return netip.Addr{}, err
	}
	if !best.IsValid() {
		return netip.Addr{}, errNoDefaultRoute
	}
	return best, nil
}

// netqHexAddr parses an address as /proc/net/route shows it: "0101A8C0" is
// 192.168.1.1.
func netqHexAddr(s string) (netip.Addr, error) {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 4 {
		return netip.Addr{}, fmt.Errorf("invalid address %q", s)
	}
	var a [4]byte
	binary.LittleEndian.PutUint32(a[:], binary.BigEndian.Uint32(b))
	return netip.AddrFrom4(a), nil
}

// netqNameserver returns the first nameserver in /etc/resolv.conf, on port
// 53, which is the one the system resolver asks first. Without a nameserver
// line glibc and Go ask 127.0.0.1, so this does too.
func netqNameserver(fsys fs.FS) (netip.AddrPort, error) {
	data, err := fs.ReadFile(fsys, "etc/resolv.conf")
	if err != nil {
		return netip.AddrPort{}, err
	}
	s := bufio.NewScanner(bytes.NewReader(data))
	for s.Scan() {
		f := strings.Fields(s.Text())
		if len(f) < 2 || f[0] != "nameserver" {
			continue
		}
		if addr, err := netip.ParseAddr(f[1]); err == nil {
			return netip.AddrPortFrom(addr.Unmap(), 53), nil
		}
	}
	if err := s.Err(); err != nil {
		return netip.AddrPort{}, err
	}
	return netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), 53), nil
}

// netqNetProber is the real prober.
type netqNetProber struct {
	logger *slog.Logger

	// Replaced in tests.
	resolve    func(ctx context.Context, host string) (netip.Addr, error)
	listenICMP func() (net.PacketConn, error)
	dial       func(ctx context.Context, network, addr string) (net.Conn, error)
	spacing    time.Duration
	timeout    time.Duration
	dnsTimeout time.Duration

	// icmpDenied is set once the kernel refuses ICMP sockets; from then on
	// only TCP is used.
	icmpDenied atomic.Bool
	// logged makes sure the method is logged once, when it is known.
	logged sync.Once
}

func newNetqNetProber(logger *slog.Logger) *netqNetProber {
	var d net.Dialer
	return &netqNetProber{
		logger: logger,
		resolve: func(ctx context.Context, host string) (netip.Addr, error) {
			addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
			if err != nil {
				return netip.Addr{}, err
			}
			if len(addrs) == 0 {
				return netip.Addr{}, fmt.Errorf("%s has no IPv4 address", host)
			}
			return addrs[0].Unmap(), nil
		},
		listenICMP: func() (net.PacketConn, error) { return icmp.ListenPacket("udp4", "0.0.0.0") },
		dial:       d.DialContext,
		spacing:    netqSpacing,
		timeout:    netqTimeout,
		dnsTimeout: netqDNSTimeout,
	}
}

func (p *netqNetProber) Probe(ctx context.Context, host string, count int, ports []int) netqBurst {
	rctx, cancel := context.WithTimeout(ctx, p.timeout)
	addr, err := p.resolve(rctx, host)
	cancel()
	if err != nil {
		return netqBurst{Err: err}
	}
	if !p.icmpDenied.Load() {
		conn, err := p.listenICMP()
		if err == nil {
			defer conn.Close()
			p.logged.Do(func() { p.logger.Info("network quality probes use ICMP echo") })
			return p.pingICMP(ctx, conn, addr, count)
		}
		if netqDenied(err) {
			if !p.icmpDenied.Swap(true) {
				p.logger.Warn("ICMP echo not allowed, network quality probes time a TCP connect instead; "+
					"check sysctl net.ipv4.ping_group_range", "err", err)
			}
		} else {
			p.logger.Debug("ICMP socket failed, using TCP for this round", "err", err)
		}
	}
	return p.pingTCP(ctx, addr, count, ports)
}

// netqDenied reports whether err means the kernel does not let this process
// open ICMP sockets at all.
func netqDenied(err error) bool {
	return errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) ||
		errors.Is(err, syscall.EPROTONOSUPPORT) || errors.Is(err, syscall.EAFNOSUPPORT)
}

// pingICMP sends count echo requests to addr over conn, an unprivileged
// ICMP socket. The kernel sets the echo ID to the socket's own and only
// passes it replies to that ID; the sequence number and a random payload
// pick out the reply to each request.
func (p *netqNetProber) pingICMP(ctx context.Context, conn net.PacketConn, addr netip.Addr, count int) netqBurst {
	stop := context.AfterFunc(ctx, func() { _ = conn.SetReadDeadline(time.Now()) })
	defer stop()

	b := netqBurst{Addr: addr, Method: "icmp"}
	dst := &net.UDPAddr{IP: addr.AsSlice()}
	payload := make([]byte, 16)
	binary.BigEndian.PutUint64(payload, rand.Uint64())
	binary.BigEndian.PutUint64(payload[8:], rand.Uint64())
	buf := make([]byte, 1500)
	for seq := 1; seq <= count && ctx.Err() == nil; seq++ {
		if seq > 1 && !netqSleep(ctx, p.spacing) {
			break
		}
		msg := icmp.Message{
			Type: ipv4.ICMPTypeEcho,
			Body: &icmp.Echo{Seq: seq, Data: payload},
		}
		wb, err := msg.Marshal(nil)
		if err != nil {
			b.Err = err
			break
		}
		start := time.Now()
		b.Sent++
		if _, err := conn.WriteTo(wb, dst); err != nil {
			b.Err = err
			continue
		}
		if ctx.Err() != nil {
			break
		}
		if err := conn.SetReadDeadline(start.Add(p.timeout)); err != nil {
			b.Err = err
			continue
		}
		for {
			n, _, err := conn.ReadFrom(buf)
			if err != nil {
				break // timed out: lost
			}
			m, err := icmp.ParseMessage(ipv4.ICMPTypeEcho.Protocol(), buf[:n])
			if err != nil || m.Type != ipv4.ICMPTypeEchoReply {
				continue
			}
			echo, ok := m.Body.(*icmp.Echo)
			if !ok || echo.Seq != seq || !bytes.Equal(echo.Data, payload) {
				continue
			}
			b.RTTs = append(b.RTTs, time.Since(start))
			break
		}
	}
	return b
}

// pingTCP times count TCP connects to addr. Each ping tries ports in order
// until one answers; a refused connection answers too.
func (p *netqNetProber) pingTCP(ctx context.Context, addr netip.Addr, count int, ports []int) netqBurst {
	b := netqBurst{Addr: addr, Method: "tcp"}
	for i := 0; i < count && ctx.Err() == nil; i++ {
		if i > 0 && !netqSleep(ctx, p.spacing) {
			break
		}
		b.Sent++
		for _, port := range ports {
			dctx, cancel := context.WithTimeout(ctx, p.timeout)
			start := time.Now()
			conn, err := p.dial(dctx, "tcp4", netip.AddrPortFrom(addr, uint16(port)).String())
			rtt := time.Since(start)
			cancel()
			if err == nil {
				_ = conn.Close()
			}
			if err == nil || errors.Is(err, syscall.ECONNREFUSED) {
				b.RTTs = append(b.RTTs, rtt)
				break
			}
			b.Err = err
		}
	}
	return b
}

// netqSleep waits d, or until ctx is done. It reports whether the wait was
// complete.
func netqSleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// LookupDNS sends one query for the A records of name to server over UDP
// and times the answer. The name is looked up as it is, without the search
// domains from resolv.conf, and nothing is cached on this side: every call
// sends a query. The resolver itself may answer from its cache, which is
// what every other program on the Pi gets too.
//
// The lookup fails when no answer comes within p.dnsTimeout, when the
// answer is an error such as NXDOMAIN or SERVFAIL, or when it holds no IPv4
// address. Replies that do not match the query (another ID or question) are
// ignored.
func (p *netqNetProber) LookupDNS(ctx context.Context, server netip.AddrPort, name string) netqLookup {
	if !strings.HasSuffix(name, ".") {
		name += "."
	}
	qname, err := dnsmessage.NewName(name)
	if err != nil {
		return netqLookup{Err: err}
	}
	question := dnsmessage.Question{Name: qname, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}
	id := uint16(rand.Uint32())
	query, err := (&dnsmessage.Message{
		Header:    dnsmessage.Header{ID: id, RecursionDesired: true},
		Questions: []dnsmessage.Question{question},
	}).Pack()
	if err != nil {
		return netqLookup{Err: err}
	}

	ctx, cancel := context.WithTimeout(ctx, p.dnsTimeout)
	defer cancel()
	conn, err := p.dial(ctx, "udp", server.String())
	if err != nil {
		return netqLookup{Err: err}
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return netqLookup{Err: err}
		}
	}

	start := time.Now()
	if _, err := conn.Write(query); err != nil {
		return netqLookup{Err: netqDNSErr(err, server, p.dnsTimeout)}
	}
	buf := make([]byte, 1500)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return netqLookup{Err: netqDNSErr(err, server, p.dnsTimeout)}
		}
		elapsed := time.Since(start)
		var msg dnsmessage.Parser
		h, err := msg.Start(buf[:n])
		if err != nil || !h.Response || h.ID != id {
			continue
		}
		// Names are case-insensitive, and some resolvers change the case.
		q, err := msg.Question()
		if err != nil || q.Type != question.Type || q.Class != question.Class ||
			!strings.EqualFold(q.Name.String(), question.Name.String()) {
			continue
		}
		if h.RCode != dnsmessage.RCodeSuccess {
			return netqLookup{Err: fmt.Errorf("%s: %s", strings.TrimSuffix(name, "."), netqRCode(h.RCode))}
		}
		if h.Truncated {
			// The answer did not fit in UDP; the resolver did answer, and
			// a client would get the rest over TCP.
			return netqLookup{Duration: elapsed}
		}
		if err := msg.SkipAllQuestions(); err != nil {
			return netqLookup{Err: fmt.Errorf("bad answer from %s: %w", server, err)}
		}
		for {
			ah, err := msg.AnswerHeader()
			if errors.Is(err, dnsmessage.ErrSectionDone) {
				break
			}
			if err != nil {
				return netqLookup{Err: fmt.Errorf("bad answer from %s: %w", server, err)}
			}
			if ah.Type == dnsmessage.TypeA {
				return netqLookup{Duration: elapsed}
			}
			if err := msg.SkipAnswer(); err != nil {
				return netqLookup{Err: fmt.Errorf("bad answer from %s: %w", server, err)}
			}
		}
		return netqLookup{Err: fmt.Errorf("%s has no IPv4 address", strings.TrimSuffix(name, "."))}
	}
}

// netqDNSErr turns a timeout into "no answer from ... within ...", which says
// more than "i/o timeout".
func netqDNSErr(err error, server netip.AddrPort, timeout time.Duration) error {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return fmt.Errorf("no answer from %s within %s", server, timeout)
	}
	return err
}

// netqRCode names an error response code the way dig does.
func netqRCode(rc dnsmessage.RCode) string {
	switch rc {
	case dnsmessage.RCodeNameError:
		return "NXDOMAIN"
	case dnsmessage.RCodeServerFailure:
		return "SERVFAIL"
	case dnsmessage.RCodeRefused:
		return "REFUSED"
	case dnsmessage.RCodeFormatError:
		return "FORMERR"
	case dnsmessage.RCodeNotImplemented:
		return "NOTIMP"
	}
	return fmt.Sprintf("rcode %d", rc)
}
