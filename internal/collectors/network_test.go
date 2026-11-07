package collectors

import (
	"context"
	"io/fs"
	"os"
	"reflect"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

var netT0 = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)

// collectNet runs one sample at now, with prev as the previous sample's time
// (zero on the first one), the way the sampler does.
func collectNet(t *testing.T, n *network, fsys fs.FS, prev, now time.Time) metrics.Result {
	t.Helper()
	env := &metrics.Env{FS: fsys, Now: now, Prev: prev}
	res, err := n.Collect(context.Background(), env)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	return res
}

func netDataOf(t *testing.T, res metrics.Result) networkData {
	t.Helper()
	data, ok := res.Data.(networkData)
	if !ok {
		t.Fatalf("Data is %T, want networkData", res.Data)
	}
	return data
}

func netNames(d networkData) []string {
	var names []string
	for _, i := range d.Interfaces {
		names = append(names, i.Name)
	}
	return names
}

// The fixtures are two /proc/net/dev snapshots taken 2s apart. lo, docker0,
// veth* and br-* are skipped, usb0 disappears and wg0 appears in between.
func TestNetwork(t *testing.T) {
	n := &network{skip: netDefaultSkip}

	first := collectNet(t, n, os.DirFS("testdata/network/first"), time.Time{}, netT0)
	if first.Series != nil {
		t.Errorf("first Series = %v, want none", first.Series)
	}
	d := netDataOf(t, first)
	if want := []string{"eth0", "eth0.100", "usb0", "wlan0"}; !reflect.DeepEqual(netNames(d), want) {
		t.Errorf("first interfaces = %v, want %v", netNames(d), want)
	}
	for _, i := range d.Interfaces {
		if i.Rates != nil {
			t.Errorf("first sample: %s has rates %+v, want none", i.Name, *i.Rates)
		}
	}
	if d.Total.Rates != nil {
		t.Errorf("first sample: total rates = %+v, want none", *d.Total.Rates)
	}

	second := collectNet(t, n, os.DirFS("testdata/network/second"), netT0, netT0.Add(2*time.Second))
	wantSeries := map[string]float64{
		"net.eth0.rx_bps":     125000, // 250000 bytes in 2s
		"net.eth0.tx_bps":     50000,
		"net.eth0_100.rx_bps": 200, // the VLAN's dot does not end up in the key
		"net.eth0_100.tx_bps": 0,
		"net.wlan0.rx_bps":    50.5,
		"net.wlan0.tx_bps":    0.5,
		// wg0 is new: it is listed but has no rate yet and does not count
		// towards the total rates, so its 5 MB since boot is no spike.
		"net.total.rx_bps": 125250.5,
		"net.total.tx_bps": 50000.5,
	}
	if !reflect.DeepEqual(second.Series, wantSeries) {
		t.Errorf("second Series = %v\nwant %v", second.Series, wantSeries)
	}

	d = netDataOf(t, second)
	wantIfaces := []netInterface{
		{
			Name: "eth0",
			netCounters: netCounters{
				RxBytes: 1250000, RxPackets: 5400, RxErrors: 1, RxDropped: 3,
				TxBytes: 600000, TxPackets: 3200, TxDropped: 1,
			},
			Rates: &netRates{RxBps: 125000, TxBps: 50000, RxPps: 200, TxPps: 100},
		},
		{
			Name:        "eth0.100",
			netCounters: netCounters{RxBytes: 700, RxPackets: 7, TxBytes: 200, TxPackets: 2},
			Rates:       &netRates{RxBps: 200, RxPps: 2},
		},
		{
			Name:        "wg0",
			netCounters: netCounters{RxBytes: 5000000, RxPackets: 4000, TxBytes: 4000000, TxPackets: 3500, TxDropped: 2},
		},
		{
			Name:        "wlan0",
			netCounters: netCounters{RxBytes: 2101, RxPackets: 21, TxBytes: 1001, TxPackets: 11},
			Rates:       &netRates{RxBps: 50.5, TxBps: 0.5, RxPps: 0.5, TxPps: 0.5},
		},
	}
	if !reflect.DeepEqual(d.Interfaces, wantIfaces) {
		t.Errorf("second Interfaces = %+v\nwant %+v", d.Interfaces, wantIfaces)
	}
	wantTotal := netTotal{
		netCounters: netCounters{
			RxBytes: 6252801, RxPackets: 9428, RxErrors: 1, RxDropped: 3,
			TxBytes: 4601201, TxPackets: 6713, TxDropped: 3,
		},
		Rates: &netRates{RxBps: 125250.5, TxBps: 50000.5, RxPps: 202.5, TxPps: 100.5},
	}
	if !reflect.DeepEqual(d.Total, wantTotal) {
		t.Errorf("second Total = %+v (rates %+v)\nwant %+v (rates %+v)", d.Total, d.Total.Rates, wantTotal, wantTotal.Rates)
	}

	// wg0 has a baseline now and gets a rate on the next sample.
	third := collectNet(t, n, os.DirFS("testdata/network/second"), netT0.Add(2*time.Second), netT0.Add(4*time.Second))
	if got, ok := third.Series["net.wg0.rx_bps"]; !ok || got != 0 {
		t.Errorf("third net.wg0.rx_bps = %v (present %v), want 0", got, ok)
	}
}

func netDevFS(lines ...string) fstest.MapFS {
	dev := "Inter-|   Receive                                                |  Transmit\n" +
		" face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed\n"
	for _, l := range lines {
		dev += l + "\n"
	}
	return fstest.MapFS{"proc/net/dev": {Data: []byte(dev)}}
}

func TestNetworkCounterReset(t *testing.T) {
	n := &network{skip: netDefaultSkip}
	collectNet(t, n, netDevFS(
		"  eth0: 1000000 1000 0 0 0 0 0 0 1000000 1000 0 0 0 0 0 0",
		" wlan0: 5000 50 0 0 0 0 0 0 5000 50 0 0 0 0 0 0",
	), time.Time{}, netT0)

	// wlan0 was removed and re-created, so its counters start over.
	res := collectNet(t, n, netDevFS(
		"  eth0: 1001000 1010 0 0 0 0 0 0 1000500 1005 0 0 0 0 0 0",
		" wlan0: 100 1 0 0 0 0 0 0 100 1 0 0 0 0 0 0",
	), netT0, netT0.Add(time.Second))
	for k, v := range res.Series {
		if v < 0 {
			t.Errorf("%s = %v, want no negative values", k, v)
		}
	}
	if _, ok := res.Series["net.wlan0.rx_bps"]; ok {
		t.Errorf("net.wlan0.rx_bps reported after a reset, want it omitted")
	}
	want := map[string]float64{"net.eth0.rx_bps": 1000, "net.eth0.tx_bps": 500, "net.total.rx_bps": 1000, "net.total.tx_bps": 500}
	for k, v := range want {
		if res.Series[k] != v {
			t.Errorf("%s = %v, want %v", k, res.Series[k], v)
		}
	}

	// The reset values are the new baseline.
	res = collectNet(t, n, netDevFS(
		"  eth0: 1001000 1010 0 0 0 0 0 0 1000500 1005 0 0 0 0 0 0",
		" wlan0: 300 3 0 0 0 0 0 0 100 1 0 0 0 0 0 0",
	), netT0.Add(time.Second), netT0.Add(2*time.Second))
	if got := res.Series["net.wlan0.rx_bps"]; got != 200 {
		t.Errorf("net.wlan0.rx_bps = %v, want 200", got)
	}
}

func TestNetworkNoElapsedTime(t *testing.T) {
	n := &network{skip: netDefaultSkip}
	fsys := netDevFS("  eth0: 1000 10 0 0 0 0 0 0 1000 10 0 0 0 0 0 0")
	collectNet(t, n, fsys, time.Time{}, netT0)
	res := collectNet(t, n, fsys, netT0, netT0) // clock did not move
	if res.Series != nil {
		t.Errorf("Series = %v, want none without elapsed time", res.Series)
	}
}

func TestNetworkAllInterfacesGone(t *testing.T) {
	n := &network{skip: netDefaultSkip}
	collectNet(t, n, netDevFS("  eth0: 1000 10 0 0 0 0 0 0 1000 10 0 0 0 0 0 0"), time.Time{}, netT0)
	res := collectNet(t, n, netDevFS("    lo: 1000 10 0 0 0 0 0 0 1000 10 0 0 0 0 0 0"), netT0, netT0.Add(time.Second))
	d := netDataOf(t, res)
	if len(d.Interfaces) != 0 || d.Interfaces == nil {
		t.Errorf("Interfaces = %#v, want an empty list", d.Interfaces)
	}
	want := map[string]float64{"net.total.rx_bps": 0, "net.total.tx_bps": 0}
	if !reflect.DeepEqual(res.Series, want) {
		t.Errorf("Series = %v, want %v", res.Series, want)
	}
}

func TestNetworkSkip(t *testing.T) {
	for name, want := range map[string]bool{
		"lo": true, "veth9f2c1d0": true, "docker0": true, "docker_gwbridge": true, "br-3f1e2d4c5b6a": true,
		"eth0": false, "wlan0": false, "end0": false, "wg0": false, "usb0": false, "bridge0": false, "lo0": false,
	} {
		if got := netDefaultSkip(name); got != want {
			t.Errorf("netDefaultSkip(%q) = %v, want %v", name, got, want)
		}
	}

	// Without a skip function every interface is reported.
	res := collectNet(t, &network{}, netDevFS("    lo: 1 1 0 0 0 0 0 0 1 1 0 0 0 0 0 0"), time.Time{}, netT0)
	if got := netNames(netDataOf(t, res)); !reflect.DeepEqual(got, []string{"lo"}) {
		t.Errorf("interfaces = %v, want [lo]", got)
	}
}

func TestNetworkSeriesNames(t *testing.T) {
	got := netSeriesNames(map[string]netCounters{
		"eth0": {}, "eth0.100": {}, "eth0_100": {}, "total": {}, "WLAN-ap": {},
	})
	want := map[string]string{
		"eth0": "eth0", "eth0.100": "eth0_100", "eth0_100": "eth0_100_2", "total": "total_2", "WLAN-ap": "wlan_ap",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("netSeriesNames = %v, want %v", got, want)
	}
}

func TestNetworkOldKernelFormat(t *testing.T) {
	// No space between the colon and a wide byte counter.
	res := collectNet(t, &network{}, netDevFS("  eth0:4294967296 10 0 0 0 0 0 0 1000 10 0 0 0 0 0 0"), time.Time{}, netT0)
	if d := netDataOf(t, res); len(d.Interfaces) != 1 || d.Interfaces[0].RxBytes != 4294967296 {
		t.Errorf("Interfaces = %+v, want eth0 with 4294967296 rx bytes", d.Interfaces)
	}
}

func TestNetworkErrors(t *testing.T) {
	tests := map[string]fstest.MapFS{
		"missing file":    {},
		"too few columns": netDevFS("  eth0: 1 2 3 4"),
		"garbage counter": netDevFS("  eth0: 1 2 3 4 5 6 7 8 x 10 11 12 13 14 15 16"),
		"no name":         netDevFS("  : 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16"),
	}
	for name, fsys := range tests {
		t.Run(name, func(t *testing.T) {
			env := &metrics.Env{FS: fsys, Now: netT0}
			if _, err := (&network{}).Collect(context.Background(), env); err == nil {
				t.Fatal("Collect() error = nil, want an error")
			}
		})
	}
}

func TestNetworkIsRegistered(t *testing.T) {
	for _, c := range metrics.Registered() {
		if c.Name() == "network" {
			return
		}
	}
	t.Fatal("network collector is not registered")
}
