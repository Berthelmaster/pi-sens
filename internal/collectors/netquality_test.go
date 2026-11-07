package collectors

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"reflect"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

// None of these tests send a packet: the rounds use fakeNetqProber, and the
// real prober is tested with a fake ICMP socket and dialer.

var netqT0 = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)

func netqDur(v float64) time.Duration { return time.Duration(v * float64(time.Millisecond)) }

func netqF(v float64) *float64 { return &v }

func TestDefaultGateway(t *testing.T) {
	// The tun0 route has a lower metric but is not up, and the wlan0 one has
	// a higher metric than eth0.
	got, err := defaultGateway(os.DirFS("testdata/netquality/pi5"))
	if err != nil {
		t.Fatal(err)
	}
	if want := netip.MustParseAddr("192.168.1.1"); got != want {
		t.Errorf("gateway = %s, want %s", got, want)
	}

	if _, err := defaultGateway(os.DirFS("testdata/netquality/nodefault")); !errors.Is(err, errNoDefaultRoute) {
		t.Errorf("without a default route: err = %v, want %v", err, errNoDefaultRoute)
	}
	if _, err := defaultGateway(os.DirFS("testdata/netquality/missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("without the file: err = %v, want not exist", err)
	}
}

func TestNetqHexAddr(t *testing.T) {
	for in, want := range map[string]string{
		"0101A8C0": "192.168.1.1",
		"FE01A8C0": "192.168.1.254",
		"010012AC": "172.18.0.1",
		"00000000": "0.0.0.0",
	} {
		got, err := netqHexAddr(in)
		if err != nil || got.String() != want {
			t.Errorf("netqHexAddr(%q) = %v, %v; want %s", in, got, err, want)
		}
	}
	for _, in := range []string{"", "0101A8", "ZZZZZZZZ", "0101A8C000"} {
		if _, err := netqHexAddr(in); err == nil {
			t.Errorf("netqHexAddr(%q) succeeded, want an error", in)
		}
	}
}

func TestNetqSummarise(t *testing.T) {
	tests := []struct {
		name    string
		burst   netqBurst
		latency *float64
		jitter  *float64
		loss    float64
	}{
		{
			name:    "all answered",
			burst:   netqBurst{Sent: 5, RTTs: []time.Duration{netqDur(10), netqDur(12), netqDur(11), netqDur(15), netqDur(13)}},
			latency: netqF(12.2),
			// |12-10| + |11-12| + |15-11| + |13-15| = 9 over 4 differences.
			jitter: netqF(2.25),
			loss:   0,
		},
		{
			name:    "two lost",
			burst:   netqBurst{Sent: 5, RTTs: []time.Duration{netqDur(20), netqDur(30), netqDur(25)}},
			latency: netqF(25),
			jitter:  netqF(7.5),
			loss:    40,
		},
		{
			name:    "one answer has no jitter",
			burst:   netqBurst{Sent: 5, RTTs: []time.Duration{netqDur(1.2345)}},
			latency: netqF(1.235),
			loss:    80,
		},
		{
			name:  "none answered",
			burst: netqBurst{Sent: 5},
			loss:  100,
		},
		{
			name:  "nothing sent",
			burst: netqBurst{Err: errors.New("no such host")},
			loss:  100,
		},
		{
			name:    "one of three lost",
			burst:   netqBurst{Sent: 3, RTTs: []time.Duration{netqDur(5), netqDur(5)}},
			latency: netqF(5),
			jitter:  netqF(0),
			loss:    33.33,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			latency, jitter, loss := netqSummarise(tt.burst)
			if !reflect.DeepEqual(latency, tt.latency) {
				t.Errorf("latency = %v, want %v", netqDeref(latency), netqDeref(tt.latency))
			}
			if !reflect.DeepEqual(jitter, tt.jitter) {
				t.Errorf("jitter = %v, want %v", netqDeref(jitter), netqDeref(tt.jitter))
			}
			if loss != tt.loss {
				t.Errorf("loss = %v, want %v", loss, tt.loss)
			}
		})
	}
}

func netqDeref(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func TestNetqStatus(t *testing.T) {
	gw := func(loss float64, latency *float64) netqTargetData {
		return netqTargetData{Kind: netqGateway, LossPct: loss, LatencyMs: latency}
	}
	inet := func(loss float64, latency *float64) netqTargetData {
		return netqTargetData{Kind: netqInternet, LossPct: loss, LatencyMs: latency}
	}
	tests := []struct {
		name    string
		targets []netqTargetData
		want    string
	}{
		{"all fine", []netqTargetData{gw(0, netqF(1)), inet(0, netqF(12)), inet(0, netqF(15))}, netqGood},
		{"one lost ping", []netqTargetData{gw(0, netqF(1)), inet(20, netqF(12)), inet(0, netqF(15))}, netqDegraded},
		{"gateway lost pings", []netqTargetData{gw(40, netqF(1)), inet(0, netqF(12)), inet(0, netqF(15))}, netqDegraded},
		{"slow", []netqTargetData{gw(0, netqF(1)), inet(0, netqF(netqSlowMs+1)), inet(0, netqF(15))}, netqDegraded},
		{"just under slow", []netqTargetData{gw(0, netqF(1)), inet(0, netqF(netqSlowMs)), inet(0, netqF(15))}, netqGood},
		{"one internet target down", []netqTargetData{gw(0, netqF(1)), inet(100, nil), inet(0, netqF(15))}, netqDegraded},
		{"all internet targets down", []netqTargetData{gw(0, netqF(1)), inet(100, nil), inet(100, nil)}, netqDown},
		{"everything down", []netqTargetData{gw(100, nil), inet(100, nil), inet(100, nil)}, netqDown},
		{"only the gateway, up", []netqTargetData{gw(0, netqF(1))}, netqGood},
		{"only the gateway, down", []netqTargetData{gw(100, nil)}, netqDown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := netqStatus(tt.targets); got != tt.want {
				t.Errorf("status = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNetqTargets(t *testing.T) {
	got := netqTargets([]string{"1.1.1.1", "8.8.8.8", "Gateway", "one.one.one.one", "1-1-1-1", "---"})
	var keys []string
	for _, tg := range got {
		keys = append(keys, tg.key)
	}
	want := []string{"gateway", "1_1_1_1", "8_8_8_8", "gateway_2", "one_one_one_one", "1_1_1_1_2", "target"}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("keys = %q, want %q", keys, want)
	}
	if got[0].kind != netqGateway || !reflect.DeepEqual(got[0].ports, []int{53, 80}) {
		t.Errorf("first target = %+v, want the gateway on ports 53 and 80", got[0])
	}
	if got[1].kind != netqInternet || !reflect.DeepEqual(got[1].ports, []int{443}) {
		t.Errorf("second target = %+v, want an internet target on port 443", got[1])
	}
}

// fakeNetqProber answers Probe from a table of bursts by host. Hosts that are
// not in it lose every ping.
type fakeNetqProber struct {
	mu     sync.Mutex
	bursts map[string]netqBurst
	calls  []string
	// probed gets a value after every Probe, if set.
	probed chan string
}

func (f *fakeNetqProber) set(host string, b netqBurst) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bursts[host] = b
}

func (f *fakeNetqProber) Probe(_ context.Context, host string, count int, ports []int) netqBurst {
	f.mu.Lock()
	f.calls = append(f.calls, host)
	b, ok := f.bursts[host]
	f.mu.Unlock()
	if !ok {
		b = netqBurst{Method: "icmp", Sent: count}
	}
	if f.probed != nil {
		f.probed <- host
	}
	return b
}

func newFakeNetqProber() *fakeNetqProber {
	return &fakeNetqProber{bursts: map[string]netqBurst{}}
}

func netqAnswered(addr string, method string, rtts ...float64) netqBurst {
	b := netqBurst{Addr: netip.MustParseAddr(addr), Method: method, Sent: netqCount}
	for _, v := range rtts {
		b.RTTs = append(b.RTTs, netqDur(v))
	}
	return b
}

func netqCollectData(t *testing.T, n *netquality) (netqData, map[string]float64) {
	t.Helper()
	res, err := n.Collect(context.Background(), &metrics.Env{Now: netqT0})
	if err != nil {
		t.Fatal(err)
	}
	// Through JSON, as the sampler sends it.
	raw, err := json.Marshal(res.Data)
	if err != nil {
		t.Fatal(err)
	}
	var data netqData
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	return data, res.Series
}

func TestNetqRound(t *testing.T) {
	n := &netquality{}
	n.started.Store(true)
	targets := netqTargets([]string{"1.1.1.1", "8.8.8.8"})
	fsys := os.DirFS("testdata/netquality/pi5")
	p := newFakeNetqProber()
	p.set("192.168.1.1", netqAnswered("192.168.1.1", "icmp", 1, 2, 1, 2, 1))
	p.set("1.1.1.1", netqAnswered("1.1.1.1", "icmp", 10, 12, 11, 15, 13))
	p.set("8.8.8.8", netqAnswered("8.8.8.8", "tcp", 20, 30, 25))

	now := netqT0
	clock := func() time.Time { return now }
	n.round(context.Background(), fsys, targets, p, clock)

	data, series := netqCollectData(t, n)
	if data.Status != netqDegraded {
		t.Errorf("status = %q, want degraded (8.8.8.8 lost two pings)", data.Status)
	}
	if data.ProbedAt == nil || !data.ProbedAt.Equal(netqT0) {
		t.Errorf("probed_at = %v, want %v", data.ProbedAt, netqT0)
	}
	wantSeries := map[string]float64{
		"netq.gateway.latency_ms": 1.4,
		"netq.gateway.loss_pct":   0,
		"netq.1_1_1_1.latency_ms": 12.2,
		"netq.1_1_1_1.loss_pct":   0,
		"netq.8_8_8_8.latency_ms": 25,
		"netq.8_8_8_8.loss_pct":   40,
	}
	if !reflect.DeepEqual(series, wantSeries) {
		t.Errorf("series = %v, want %v", series, wantSeries)
	}
	if len(data.Targets) != 3 {
		t.Fatalf("targets = %+v, want 3", data.Targets)
	}
	gw := data.Targets[0]
	if gw.Name != "gateway" || gw.Kind != netqGateway || gw.Address != "192.168.1.1" || gw.Method != "icmp" {
		t.Errorf("gateway = %+v", gw)
	}
	if tcp := data.Targets[2]; tcp.Method != "tcp" || tcp.JitterMs == nil || *tcp.JitterMs != 7.5 {
		t.Errorf("8.8.8.8 = %+v, want method tcp and jitter 7.5", tcp)
	}
	for _, tg := range data.Targets {
		if tg.LastOK == nil || !tg.LastOK.Equal(netqT0) {
			t.Errorf("%s last_ok = %v, want %v", tg.Name, tg.LastOK, netqT0)
		}
	}

	// The internet goes: both internet targets lose everything, the
	// gateway still answers. last_ok keeps the last good round.
	now = netqT0.Add(10 * time.Second)
	p.set("1.1.1.1", netqBurst{Addr: netip.MustParseAddr("1.1.1.1"), Method: "icmp", Sent: 5})
	p.set("8.8.8.8", netqBurst{Addr: netip.MustParseAddr("8.8.8.8"), Method: "tcp", Sent: 5, Err: errors.New("i/o timeout")})
	n.round(context.Background(), fsys, targets, p, clock)

	data, series = netqCollectData(t, n)
	if data.Status != netqDown {
		t.Errorf("status = %q, want down", data.Status)
	}
	wantSeries = map[string]float64{
		"netq.gateway.latency_ms": 1.4,
		"netq.gateway.loss_pct":   0,
		"netq.1_1_1_1.loss_pct":   100,
		"netq.8_8_8_8.loss_pct":   100,
	}
	if !reflect.DeepEqual(series, wantSeries) {
		t.Errorf("series = %v, want %v", series, wantSeries)
	}
	if !data.Targets[0].LastOK.Equal(now) {
		t.Errorf("gateway last_ok = %v, want %v", data.Targets[0].LastOK, now)
	}
	for _, tg := range data.Targets[1:] {
		if tg.LastOK == nil || !tg.LastOK.Equal(netqT0) {
			t.Errorf("%s last_ok = %v, want the first round %v", tg.Name, tg.LastOK, netqT0)
		}
		if tg.LatencyMs != nil || tg.JitterMs != nil {
			t.Errorf("%s latency %v jitter %v, want none", tg.Name, netqDeref(tg.LatencyMs), netqDeref(tg.JitterMs))
		}
	}
	if got := data.Targets[2].Error; got != "i/o timeout" {
		t.Errorf("8.8.8.8 error = %q, want i/o timeout", got)
	}
}

func TestNetqRoundWithoutDefaultRoute(t *testing.T) {
	n := &netquality{}
	n.started.Store(true)
	p := newFakeNetqProber()
	p.set("1.1.1.1", netqAnswered("1.1.1.1", "icmp", 10, 10, 10, 10, 10))
	n.round(context.Background(), os.DirFS("testdata/netquality/nodefault"), netqTargets([]string{"1.1.1.1"}), p, func() time.Time { return netqT0 })

	if !reflect.DeepEqual(p.calls, []string{"1.1.1.1"}) {
		t.Errorf("probed %q, want only 1.1.1.1", p.calls)
	}
	data, series := netqCollectData(t, n)
	gw := data.Targets[0]
	if gw.Error != errNoDefaultRoute.Error() || gw.LossPct != 100 || gw.LastOK != nil {
		t.Errorf("gateway = %+v, want 100%% loss and error %q", gw, errNoDefaultRoute)
	}
	if series["netq.gateway.loss_pct"] != 100 {
		t.Errorf("series = %v, want netq.gateway.loss_pct 100", series)
	}
	if data.Status != netqDegraded {
		t.Errorf("status = %q, want degraded", data.Status)
	}
}

func TestNetqNotStarted(t *testing.T) {
	data, series := netqCollectData(t, &netquality{})
	if data.Available || data.Status != "" || len(series) != 0 {
		t.Errorf("data = %+v, series = %v; want unavailable and no series", data, series)
	}
}

// TestNetqStartStops runs the goroutine with a fake prober, collects while it
// probes, and checks it stops when the context is cancelled. Run with -race.
func TestNetqStartStops(t *testing.T) {
	n := &netquality{}
	p := newFakeNetqProber()
	p.probed = make(chan string, 64)
	p.set("192.168.1.1", netqAnswered("192.168.1.1", "icmp", 1, 1, 1, 1, 1))
	p.set("1.1.1.1", netqAnswered("1.1.1.1", "icmp", 10, 10, 10, 10, 10))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := n.start(ctx, NetQualityOptions{
		Targets:  []string{"1.1.1.1"},
		Interval: 5 * time.Millisecond,
		FS:       os.DirFS("testdata/netquality/pi5"),
	}, p)

	data, _ := netqCollectData(t, n)
	if !data.Available || data.IntervalS != 0.005 {
		t.Errorf("data = %+v, want available with interval_s 0.005", data)
	}

	// Several rounds, collecting all the while.
	deadline := time.After(5 * time.Second)
	for probes := 0; probes < 6; {
		select {
		case <-p.probed:
			probes++
		case <-deadline:
			t.Fatal("the prober was not called six times within 5s")
		}
		netqCollectData(t, n)
	}
	cancel()
	// Unblock a probe that is waiting to report.
	go func() {
		for range p.probed {
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the probe goroutine did not stop within 5s of cancelling")
	}
	close(p.probed)

	data, series := netqCollectData(t, n)
	if data.Status != netqGood || series["netq.1_1_1_1.latency_ms"] != 10 {
		t.Errorf("status %q series %v, want good with netq.1_1_1_1.latency_ms 10", data.Status, series)
	}
}

func TestNetqPendingBeforeFirstRound(t *testing.T) {
	n := &netquality{}
	n.started.Store(true)
	data, series := netqCollectData(t, n)
	if !data.Available || data.Status != netqPending || len(data.Targets) != 0 || len(series) != 0 {
		t.Errorf("data = %+v, series = %v; want available, pending, nothing else", data, series)
	}
}

func testNetqNetProber() *netqNetProber {
	p := newNetqNetProber(slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.resolve = func(_ context.Context, host string) (netip.Addr, error) {
		return netip.ParseAddr(host)
	}
	p.spacing = 0
	p.timeout = 50 * time.Millisecond
	p.listenICMP = func() (net.PacketConn, error) { panic("unexpected ICMP socket") }
	p.dial = func(context.Context, string, string) (net.Conn, error) { panic("unexpected TCP connect") }
	return p
}

// TestNetqProberFallsBackToTCP checks that a refused ICMP socket switches the
// prober to TCP connects for good, on the gateway ports in order, with a
// refused connection counting as an answer.
func TestNetqProberFallsBackToTCP(t *testing.T) {
	p := testNetqNetProber()
	listens := 0
	p.listenICMP = func() (net.PacketConn, error) {
		listens++
		return nil, &net.OpError{Op: "listen", Net: "udp4", Err: os.NewSyscallError("socket", syscall.EACCES)}
	}
	var mu sync.Mutex
	var dialed []string
	p.dial = func(_ context.Context, network, addr string) (net.Conn, error) {
		mu.Lock()
		dialed = append(dialed, addr)
		mu.Unlock()
		if network != "tcp4" {
			t.Errorf("network = %q, want tcp4", network)
		}
		switch addr {
		case "192.168.1.1:53": // filtered
			return nil, &net.OpError{Op: "dial", Err: os.ErrDeadlineExceeded}
		case "192.168.1.1:80":
			return nil, &net.OpError{Op: "dial", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
		case "1.1.1.1:443":
			c1, c2 := net.Pipe()
			_ = c2.Close()
			return c1, nil
		}
		t.Errorf("unexpected dial to %s", addr)
		return nil, errors.New("unexpected")
	}

	b := p.Probe(context.Background(), "192.168.1.1", 3, netqGatewayPorts)
	if b.Method != "tcp" || b.Sent != 3 || len(b.RTTs) != 3 || b.Addr.String() != "192.168.1.1" {
		t.Errorf("gateway burst = %+v, want 3 of 3 answered over tcp", b)
	}
	want := []string{"192.168.1.1:53", "192.168.1.1:80", "192.168.1.1:53", "192.168.1.1:80", "192.168.1.1:53", "192.168.1.1:80"}
	if !reflect.DeepEqual(dialed, want) {
		t.Errorf("dialed %q, want %q", dialed, want)
	}

	b = p.Probe(context.Background(), "1.1.1.1", 2, netqInternetPorts)
	if b.Method != "tcp" || b.Sent != 2 || len(b.RTTs) != 2 {
		t.Errorf("internet burst = %+v, want 2 of 2 answered over tcp", b)
	}
	if listens != 1 {
		t.Errorf("ICMP socket tried %d times, want once", listens)
	}
}

func TestNetqProberTCPLoss(t *testing.T) {
	p := testNetqNetProber()
	p.icmpDenied.Store(true)
	timeout := &net.OpError{Op: "dial", Err: os.ErrDeadlineExceeded}
	p.dial = func(context.Context, string, string) (net.Conn, error) { return nil, timeout }
	b := p.Probe(context.Background(), "1.1.1.1", 2, netqInternetPorts)
	if b.Sent != 2 || len(b.RTTs) != 0 || !errors.Is(b.Err, os.ErrDeadlineExceeded) {
		t.Errorf("burst = %+v, want 2 sent, none answered, the timeout as error", b)
	}
}

func TestNetqProberResolveError(t *testing.T) {
	p := testNetqNetProber()
	b := p.Probe(context.Background(), "not-an-ip", 5, netqInternetPorts)
	if b.Err == nil || b.Sent != 0 || b.Addr.IsValid() {
		t.Errorf("burst = %+v, want an error and nothing sent", b)
	}
	if _, _, loss := netqSummarise(b); loss != 100 {
		t.Errorf("loss = %v, want 100", loss)
	}
}

// fakeICMPConn plays the kernel's side of an unprivileged ICMP socket: every
// echo request it is sent gets a stray reply that must be ignored, then the
// real reply unless its sequence number is in drop.
type fakeICMPConn struct {
	net.PacketConn // nil; only the methods below are used

	mu      sync.Mutex
	drop    map[int]bool
	replies [][]byte
	sentTo  []string
	closed  bool
}

func (c *fakeICMPConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	m, err := icmp.ParseMessage(1, b)
	if err != nil {
		return 0, err
	}
	echo := m.Body.(*icmp.Echo)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sentTo = append(c.sentTo, addr.String())
	stray, _ := (&icmp.Message{Type: ipv4.ICMPTypeEchoReply, Body: &icmp.Echo{Seq: echo.Seq, Data: []byte("someone else")}}).Marshal(nil)
	c.replies = append(c.replies, stray)
	if !c.drop[echo.Seq] {
		reply, _ := (&icmp.Message{Type: ipv4.ICMPTypeEchoReply, Body: echo}).Marshal(nil)
		c.replies = append(c.replies, reply)
	}
	return len(b), nil
}

func (c *fakeICMPConn) ReadFrom(b []byte) (int, net.Addr, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.replies) == 0 {
		return 0, nil, os.ErrDeadlineExceeded
	}
	n := copy(b, c.replies[0])
	c.replies = c.replies[1:]
	return n, &net.UDPAddr{}, nil
}

func (c *fakeICMPConn) SetReadDeadline(time.Time) error { return nil }

func (c *fakeICMPConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func TestNetqProberICMP(t *testing.T) {
	p := testNetqNetProber()
	conn := &fakeICMPConn{drop: map[int]bool{2: true, 4: true}}
	p.listenICMP = func() (net.PacketConn, error) { return conn, nil }

	b := p.Probe(context.Background(), "1.1.1.1", 5, netqInternetPorts)
	if b.Method != "icmp" || b.Sent != 5 || len(b.RTTs) != 3 {
		t.Errorf("burst = %+v, want 3 of 5 answered over icmp", b)
	}
	if _, _, loss := netqSummarise(b); loss != 40 {
		t.Errorf("loss = %v, want 40", loss)
	}
	if len(conn.sentTo) != 5 || conn.sentTo[0] != "1.1.1.1:0" {
		t.Errorf("sent to %q, want 1.1.1.1 five times", conn.sentTo)
	}
	if !conn.closed {
		t.Error("the ICMP socket was not closed")
	}
}
