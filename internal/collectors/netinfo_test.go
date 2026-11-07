package collectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

// fakeAddrs is a netinfoAddrSource with fixed addresses.
type fakeAddrs struct {
	addrs map[string][]string
	err   error
}

func (f fakeAddrs) Addrs() (map[string][]netip.Prefix, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := map[string][]netip.Prefix{}
	for name, list := range f.addrs {
		for _, s := range list {
			out[name] = append(out[name], netip.MustParsePrefix(s))
		}
	}
	return out, nil
}

// iwNotFound is what ExecRunner returns when iw is not installed.
var iwNotFound = fmt.Errorf("iw: %w", &exec.Error{Name: "iw", Err: exec.ErrNotFound})

const iwLinkWlan0 = `Connected to dc:a6:32:01:02:03 (on wlan0)
	SSID: Caf\xc3\xa9 Wi-Fi
	freq: 5180.0
	RX: 81516932 bytes (77521 packets)
	TX: 4119371 bytes (21329 packets)
	signal: -52 dBm
	rx bitrate: 433.3 MBit/s VHT-MCS 9 80MHz short GI VHT-NSS 1
	tx bitrate: 390.0 MBit/s VHT-MCS 8 80MHz short GI VHT-NSS 1

	bss flags:	short-slot-time
	dtim period:	1
	beacon int:	100
`

func collectNetinfo(t *testing.T, n *netinfo, fixture string, runner metrics.CommandRunner) (metrics.Result, netinfoData) {
	t.Helper()
	env := &metrics.Env{FS: os.DirFS("testdata/netinfo/" + fixture), Runner: runner, Now: time.Now()}
	res, err := n.Collect(context.Background(), env)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	data, ok := res.Data.(netinfoData)
	if !ok {
		t.Fatalf("Data is %T, want netinfoData", res.Data)
	}
	return res, data
}

func int64p(v int64) *int64       { return &v }
func float64p(v float64) *float64 { return &v }

// testdata/netinfo/wired is a Pi on Ethernet running Docker (docker0 and a
// container's veth) and WireGuard, with Wi-Fi disabled.
func TestNetinfoWired(t *testing.T) {
	addrs := fakeAddrs{addrs: map[string][]string{
		"lo":          {"127.0.0.1/8", "::1/128"},
		"eth0":        {"192.168.1.20/24", "2a02:aa7:1234::20/64", "fe80::da3a:ddff:fe12:3456/64"},
		"docker0":     {"172.17.0.1/16"},
		"veth3f2a1b9": {"fe80::681f:cff:fe22:9e41/64"},
		"wg0":         {"10.0.0.2/24"},
	}}
	runner := &metrics.FakeRunner{}
	res, data := collectNetinfo(t, &netinfo{addrs: addrs}, "wired", runner)

	if len(res.Series) != 0 {
		t.Errorf("Series = %v, want none without Wi-Fi", res.Series)
	}
	if calls := runner.Calls(); len(calls) != 0 {
		t.Errorf("ran %v, want no commands without Wi-Fi", calls)
	}

	want := netinfoData{
		Interfaces: []netinfoInterface{
			{
				Name: "docker0", OperState: "down", MAC: "02:42:9c:4e:1a:07", MTU: 1500,
				IPv4: []string{"172.17.0.1/16"}, IPv6: []string{},
			},
			{
				Name: "eth0", OperState: "up", MAC: "d8:3a:dd:12:34:56", SpeedMbps: int64p(1000), MTU: 1500,
				IPv4: []string{"192.168.1.20/24"},
				IPv6: []string{"2a02:aa7:1234::20/64", "fe80::da3a:ddff:fe12:3456/64"},
			},
			// lo and the veth are left out.
			{
				Name: "wg0", OperState: "unknown", MTU: 1420, // no MAC
				IPv4: []string{"10.0.0.2/24"}, IPv6: []string{},
			},
		},
		DefaultGateway: &netinfoGateway{Address: "192.168.1.1", Interface: "eth0"},
	}
	if !reflect.DeepEqual(data, want) {
		t.Errorf("Data =\n%s\nwant\n%s", netinfoJSON(t, data), netinfoJSON(t, want))
	}
}

// testdata/netinfo/wifi is a Pi on Wi-Fi with the Ethernet cable unplugged
// and a phone tethered over USB as a backup uplink.
func TestNetinfoWiFi(t *testing.T) {
	addrs := fakeAddrs{addrs: map[string][]string{
		"wlan0": {"192.168.1.21/24", "fe80::da3a:ddff:fe12:3457/64"},
		"usb0":  {"192.168.42.17/24"},
	}}
	runner := &metrics.FakeRunner{Responses: map[string]metrics.FakeResponse{
		"iw dev wlan0 link": {Output: iwLinkWlan0},
	}}
	res, data := collectNetinfo(t, &netinfo{addrs: addrs}, "wifi", runner)

	wantSeries := map[string]float64{
		"wifi.wlan0.signal_dbm":  -52,
		"wifi.wlan0.quality_pct": 82.86, // 58 of 70
	}
	if !reflect.DeepEqual(res.Series, wantSeries) {
		t.Errorf("Series = %v, want %v", res.Series, wantSeries)
	}

	want := netinfoData{
		Interfaces: []netinfoInterface{
			{
				// Unplugged: speed is -1 and left out.
				Name: "eth0", OperState: "down", MAC: "d8:3a:dd:12:34:56", MTU: 1500,
				IPv4: []string{}, IPv6: []string{},
			},
			{
				Name: "usb0", OperState: "unknown", MAC: "ee:2b:7c:51:0a:3d", SpeedMbps: int64p(425), MTU: 1500,
				IPv4: []string{"192.168.42.17/24"}, IPv6: []string{},
			},
			{
				Name: "wlan0", OperState: "up", MAC: "d8:3a:dd:12:34:57", MTU: 1500, Wireless: true,
				IPv4: []string{"192.168.1.21/24"}, IPv6: []string{"fe80::da3a:ddff:fe12:3457/64"},
				WiFi: &netinfoWiFi{
					SSID: "Café Wi-Fi", BSSID: "dc:a6:32:01:02:03", FrequencyMHz: 5180,
					SignalDBm: -52, LinkQuality: 58, QualityPct: 82.86,
					// brcmfmac reports no noise (-256).
				},
			},
		},
		// wlan0 (metric 600) beats usb0 (metric 750).
		DefaultGateway: &netinfoGateway{Address: "192.168.1.1", Interface: "wlan0"},
	}
	if !reflect.DeepEqual(data, want) {
		t.Errorf("Data =\n%s\nwant\n%s", netinfoJSON(t, data), netinfoJSON(t, want))
	}
}

func TestNetinfoWithoutIw(t *testing.T) {
	addrs := fakeAddrs{}
	runner := &metrics.FakeRunner{Responses: map[string]metrics.FakeResponse{
		"iw dev wlan0 link": {Err: iwNotFound},
	}}
	n := &netinfo{addrs: addrs}
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	collect := func() (metrics.Result, netinfoData) {
		t.Helper()
		env := &metrics.Env{FS: os.DirFS("testdata/netinfo/wifi"), Runner: runner, Now: now}
		res, err := n.Collect(context.Background(), env)
		if err != nil {
			t.Fatalf("Collect() error = %v, want iw to be optional", err)
		}
		return res, res.Data.(netinfoData)
	}

	res, data := collect()
	if res.Series["wifi.wlan0.signal_dbm"] != -52 {
		t.Errorf("Series = %v, want the signal without iw", res.Series)
	}
	wantWiFi := &netinfoWiFi{SignalDBm: -52, LinkQuality: 58, QualityPct: 82.86}
	if w := data.Interfaces[2].WiFi; !reflect.DeepEqual(w, wantWiFi) {
		t.Errorf("WiFi = %+v, want %+v", w, wantWiFi)
	}
	b, err := json.Marshal(data.Interfaces[2].WiFi)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"signal_dbm":-52,"link_quality":58,"quality_pct":82.86}`; string(b) != want {
		t.Errorf("WiFi JSON = %s, want %s", b, want)
	}

	// A missing iw is not retried every sample...
	now = now.Add(time.Minute)
	collect()
	if calls := runner.Calls(); len(calls) != 1 {
		t.Errorf("ran %v, want iw tried once while it is missing", calls)
	}
	// ...but is tried again later, in case it was installed.
	now = now.Add(netinfoIwRetry)
	runner.Responses["iw dev wlan0 link"] = metrics.FakeResponse{Output: iwLinkWlan0}
	_, data = collect()
	if got := data.Interfaces[2].WiFi.SSID; got != "Café Wi-Fi" {
		t.Errorf("SSID = %q after iw was installed, want Café Wi-Fi", got)
	}
}

func TestNetinfoIwFailures(t *testing.T) {
	tests := map[string]metrics.CommandRunner{
		"no runner": nil,
		"iw fails": &metrics.FakeRunner{Responses: map[string]metrics.FakeResponse{
			"iw dev wlan0 link": {Err: errors.New("iw: exit status 237: command failed: No such device (-19)")},
		}},
		"not connected": &metrics.FakeRunner{Responses: map[string]metrics.FakeResponse{
			"iw dev wlan0 link": {Output: "Not connected.\n"},
		}},
	}
	for name, runner := range tests {
		t.Run(name, func(t *testing.T) {
			n := &netinfo{addrs: fakeAddrs{}}
			_, data := collectNetinfo(t, n, "wifi", runner)
			w := data.Interfaces[2].WiFi
			if w == nil || w.SSID != "" || w.SignalDBm != -52 {
				t.Errorf("WiFi = %+v, want the signal and no SSID", w)
			}
			if !n.iwRetry.IsZero() {
				t.Errorf("iwRetry = %v, want iw retried next sample after a failure other than not found", n.iwRetry)
			}
		})
	}
}

func TestNetinfoWireless(t *testing.T) {
	const hdr = "Inter-| sta-|   Quality        |   Discarded packets               | Missed | WE\n" +
		" face | tus | link level noise |  nwid  crypt   frag  retry   misc | beacon | 22\n"
	tests := map[string]struct {
		line string
		want netinfoWiFi
	}{
		"cfg80211 with noise": {
			" wlp1s0: 0000   45.  -65.  -95.        0      0      0      0      0        0\n",
			netinfoWiFi{SignalDBm: -65, LinkQuality: 45, QualityPct: 64.29, NoiseDBm: float64p(-95)},
		},
		"old unsigned dBm": {
			" wlan1: 0000   70   216   164        0      0      0      0      0        0\n",
			netinfoWiFi{SignalDBm: -40, LinkQuality: 70, QualityPct: 100, NoiseDBm: float64p(-92)},
		},
		"no noise": {
			" wlan0: 0000   20.  -90.  -256        0      0      0      0      0        0\n",
			netinfoWiFi{SignalDBm: -90, LinkQuality: 20, QualityPct: 28.57},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			fsys := fstest.MapFS{"proc/net/wireless": {Data: []byte(hdr + tt.line)}}
			got, err := netinfoReadWireless(fsys)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 {
				t.Fatalf("got %v, want one interface", got)
			}
			for _, w := range got {
				if !reflect.DeepEqual(w, tt.want) {
					t.Errorf("got %+v, want %+v", w, tt.want)
				}
			}
		})
	}
}

func TestNetinfoSeriesName(t *testing.T) {
	fsys := fstest.MapFS{
		"sys/class/net/wlan0.ap/operstate": {Data: []byte("up\n")},
		"proc/net/wireless": {Data: []byte(
			" wlan0.ap: 0000   70.  -30.  -256        0      0      0      0      0        0\n")},
	}
	res, err := (&netinfo{addrs: fakeAddrs{}}).Collect(context.Background(), &metrics.Env{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]float64{"wifi.wlan0_ap.signal_dbm": -30, "wifi.wlan0_ap.quality_pct": 100}
	if !reflect.DeepEqual(res.Series, want) {
		t.Errorf("Series = %v, want %v", res.Series, want)
	}
}

func TestNetinfoEmpty(t *testing.T) {
	// No sysfs, no wireless extensions and no routes: an empty section.
	res, err := (&netinfo{addrs: fakeAddrs{}}).Collect(context.Background(), &metrics.Env{FS: fstest.MapFS{}})
	if err != nil {
		t.Fatalf("Collect() error = %v, want an empty section", err)
	}
	if len(res.Series) != 0 {
		t.Errorf("Series = %v, want none", res.Series)
	}
	b, err := json.Marshal(res.Data)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"interfaces":[]}` {
		t.Errorf("Data = %s, want {\"interfaces\":[]}", b)
	}
}

func TestNetinfoGateway(t *testing.T) {
	const hdr = "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n"
	tests := map[string]struct {
		routes string
		want   *netinfoGateway
	}{
		"none": {"eth0\t0001A8C0\t00000000\t0001\t0\t0\t100\t00FFFFFF\t0\t0\t0\n", nil},
		"down route skipped": {
			"eth0\t00000000\t0101A8C0\t0002\t0\t0\t100\t00000000\t0\t0\t0\n", nil,
		},
		"lowest metric": {
			"wlan0\t00000000\t0101A8C0\t0003\t0\t0\t600\t00000000\t0\t0\t0\n" +
				"eth0\t00000000\tFE01A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n",
			&netinfoGateway{Address: "192.168.1.254", Interface: "eth0"},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := netinfoReadGateway(fstest.MapFS{"proc/net/route": {Data: []byte(hdr + tt.routes)}})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestNetinfoErrors(t *testing.T) {
	tests := map[string]struct {
		fsys  fstest.MapFS
		addrs netinfoAddrSource
	}{
		"addresses fail": {fstest.MapFS{}, fakeAddrs{err: errors.New("netlink: permission denied")}},
		"bad mtu": {fstest.MapFS{
			"sys/class/net/eth0/operstate": {Data: []byte("up\n")},
			"sys/class/net/eth0/mtu":       {Data: []byte("big\n")},
		}, fakeAddrs{}},
		"bad wireless line": {fstest.MapFS{
			"proc/net/wireless": {Data: []byte(" wlan0: 0000 x\n")},
		}, fakeAddrs{}},
		"bad route line": {fstest.MapFS{
			"proc/net/route": {Data: []byte("Iface\tDestination\neth0\t00000000\n")},
		}, fakeAddrs{}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := (&netinfo{addrs: tt.addrs}).Collect(context.Background(), &metrics.Env{FS: tt.fsys})
			if err == nil {
				t.Fatal("Collect() error = nil, want an error")
			}
		})
	}
}

func TestNetinfoUnescapeSSID(t *testing.T) {
	tests := map[string]string{
		`Home`:                 "Home",
		`Caf\xc3\xa9`:          "Café",
		`\xf0\x9f\x93\xb6 net`: "📶 net",
		`trailing\x4`:          `trailing\x4`,
		`bad\xzz`:              `bad\xzz`,
		`back\\slash\x5c`:      `back\\slash\`,
	}
	for in, want := range tests {
		if got := netinfoUnescapeSSID(in); got != want {
			t.Errorf("netinfoUnescapeSSID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNetinfoSystemAddrs(t *testing.T) {
	addrs, err := netinfoSystemAddrs{}.Addrs()
	if err != nil {
		t.Skipf("net.Interfaces: %v", err)
	}
	for name, list := range addrs {
		for _, p := range list {
			if !p.IsValid() || p.Addr().Is4In6() {
				t.Errorf("%s: address %v, want a valid, unmapped prefix", name, p)
			}
		}
	}
}

func TestNetinfoIsRegistered(t *testing.T) {
	for _, c := range metrics.Registered() {
		if c.Name() == "netinfo" {
			return
		}
	}
	t.Fatal("netinfo collector is not registered")
}

func netinfoJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
