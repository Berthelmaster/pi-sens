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
	"math"
	"net"
	"net/netip"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

func init() { metrics.Register(&netinfo{}) }

// netinfo reports the network interfaces: their state, MAC address, link
// speed, MTU and IP addresses, the Wi-Fi link of wireless ones and the
// default IPv4 gateway.
//
//   - Interfaces are the entries of /sys/class/net, which also gives
//     operstate, address, speed and mtu. The loopback interface and Docker's
//     per-container veth pairs are left out.
//   - Addresses come from net.Interfaces (netlink), behind netinfoAddrSource
//     so tests can fake them.
//   - Signal, link quality and noise come from /proc/net/wireless. It lists
//     every wireless interface, but one that is not connected has an
//     all-zero line and gets no Wi-Fi section. The SSID, BSSID and frequency
//     come from `iw dev <iface> link`; when iw is not installed or fails they
//     are simply left out.
//   - The default gateway is the default route with the lowest metric in
//     /proc/net/route.
//
// Series: wifi.<iface>.signal_dbm and wifi.<iface>.quality_pct for every
// connected wireless interface.
type netinfo struct {
	// addrs is nil in production, which uses net.Interfaces.
	addrs netinfoAddrSource

	// iwRetry is when to try iw again after it was not found. Starting a
	// missing binary every sample is cheap but pointless, and iw may be
	// installed later.
	iwRetry time.Time
}

// netinfoAddrSource lists the IP addresses of every interface by name.
type netinfoAddrSource interface {
	Addrs() (map[string][]netip.Prefix, error)
}

// netinfoIwRetry is how long to wait before running iw again after it was not
// found.
const netinfoIwRetry = 5 * time.Minute

// netinfoMaxQuality is the maximum link quality in /proc/net/wireless for
// cfg80211 drivers (brcmfmac on the Pi included), which derive it from the
// dBm signal as signal + 110, clamped to 0..70. Old wireless extensions
// drivers may use another maximum; the percentage is clamped to 100.
const netinfoMaxQuality = 70

const netinfoSysDir = "sys/class/net"

type netinfoData struct {
	Interfaces []netinfoInterface `json:"interfaces"`
	// DefaultGateway is missing when there is no default IPv4 route.
	DefaultGateway *netinfoGateway `json:"default_gateway,omitempty"`
}

type netinfoInterface struct {
	Name string `json:"name"`
	// OperState is the RFC 2863 state from sysfs: up, down, dormant,
	// lowerlayerdown, notpresent, testing or unknown.
	OperState string `json:"operstate"`
	MAC       string `json:"mac,omitempty"`
	// SpeedMbps is the negotiated link speed. It is missing when the kernel
	// does not know it: Wi-Fi, virtual interfaces and unplugged cables.
	SpeedMbps *int64   `json:"speed_mbps,omitempty"`
	MTU       int      `json:"mtu"`
	Wireless  bool     `json:"wireless"`
	IPv4      []string `json:"ipv4"` // CIDR notation, e.g. 192.168.1.20/24
	IPv6      []string `json:"ipv6"`
	// WiFi is set for wireless interfaces that are connected.
	WiFi *netinfoWiFi `json:"wifi,omitempty"`
}

type netinfoWiFi struct {
	// SSID, BSSID and FrequencyMHz come from iw and are missing without it.
	SSID         string `json:"ssid,omitempty"`
	BSSID        string `json:"bssid,omitempty"`
	FrequencyMHz int    `json:"frequency_mhz,omitempty"`

	SignalDBm float64 `json:"signal_dbm"`
	// LinkQuality is the raw value from /proc/net/wireless, out of 70;
	// QualityPct is the same as a percentage.
	LinkQuality float64 `json:"link_quality"`
	QualityPct  float64 `json:"quality_pct"`
	// NoiseDBm is missing when the driver does not report it, as brcmfmac
	// does not.
	NoiseDBm *float64 `json:"noise_dbm,omitempty"`
}

type netinfoGateway struct {
	Address   string `json:"address"`
	Interface string `json:"interface"`
}

func (*netinfo) Name() string { return "netinfo" }

func (n *netinfo) Collect(ctx context.Context, env *metrics.Env) (metrics.Result, error) {
	names, err := netinfoInterfaceNames(env.FS)
	if err != nil {
		return metrics.Result{}, err
	}

	src := n.addrs
	if src == nil {
		src = netinfoSystemAddrs{}
	}
	addrs, err := src.Addrs()
	if err != nil {
		return metrics.Result{}, fmt.Errorf("interface addresses: %w", err)
	}

	wireless, err := netinfoReadWireless(env.FS)
	if err != nil {
		return metrics.Result{}, err
	}

	data := netinfoData{Interfaces: []netinfoInterface{}}
	series := map[string]float64{}
	for _, name := range names {
		iface, ok, err := netinfoReadInterface(env.FS, name)
		if err != nil {
			return metrics.Result{}, err
		}
		if !ok {
			continue // went away while we were reading it
		}
		for _, p := range addrs[name] {
			if p.Addr().Is4() {
				iface.IPv4 = append(iface.IPv4, p.String())
			} else {
				iface.IPv6 = append(iface.IPv6, p.String())
			}
		}

		if w, ok := wireless[name]; ok {
			iface.Wireless = true
			n.readIwLink(ctx, env, name, &w)
			iface.WiFi = &w
			key := "wifi." + netinfoSeriesName(name)
			series[key+".signal_dbm"] = w.SignalDBm
			series[key+".quality_pct"] = w.QualityPct
		}
		data.Interfaces = append(data.Interfaces, iface)
	}

	gw, err := netinfoReadGateway(env.FS)
	if err != nil {
		return metrics.Result{}, err
	}
	data.DefaultGateway = gw

	if len(series) == 0 {
		return metrics.Result{Data: data}, nil
	}
	return metrics.Result{Data: data, Series: series}, nil
}

// netinfoInterfaceNames lists /sys/class/net in name order, without loopback
// and veth interfaces. A missing directory yields none.
func netinfoInterfaceNames(fsys fs.FS) ([]string, error) {
	entries, err := fs.ReadDir(fsys, netinfoSysDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "veth") {
			continue
		}
		// ARPHRD_LOOPBACK; lo is the only one in practice.
		if typ, _ := netinfoReadValue(fsys, path.Join(netinfoSysDir, name, "type")); name == "lo" || typ == "772" {
			continue
		}
		names = append(names, name)
	}
	return names, nil
}

// netinfoReadInterface reads the sysfs attributes of one interface. ok is
// false if the interface has no operstate, which means it was removed after
// the directory was listed.
func netinfoReadInterface(fsys fs.FS, name string) (iface netinfoInterface, ok bool, err error) {
	dir := path.Join(netinfoSysDir, name)
	state, ok := netinfoReadValue(fsys, path.Join(dir, "operstate"))
	if !ok {
		return iface, false, nil
	}
	iface = netinfoInterface{
		Name:      name,
		OperState: state,
		IPv4:      []string{},
		IPv6:      []string{},
	}

	// Interfaces without a hardware address (tun, wireguard) have an empty
	// address file.
	if mac, _ := netinfoReadValue(fsys, path.Join(dir, "address")); mac != "" && mac != "00:00:00:00:00:00" {
		iface.MAC = mac
	}

	if s, ok := netinfoReadValue(fsys, path.Join(dir, "mtu")); ok {
		mtu, err := strconv.Atoi(s)
		if err != nil {
			return iface, false, fmt.Errorf("%s/mtu: %w", dir, err)
		}
		iface.MTU = mtu
	}

	// Reading speed fails with EINVAL on interfaces that have no speed and
	// gives -1 (SPEED_UNKNOWN) when the link is down.
	if s, ok := netinfoReadValue(fsys, path.Join(dir, "speed")); ok {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil && v > 0 {
			iface.SpeedMbps = &v
		}
	}

	// wireless/ is the wireless extensions directory, phy80211 the link to
	// the cfg80211 phy. Either marks a Wi-Fi interface.
	for _, f := range []string{"wireless", "phy80211"} {
		if _, err := fs.Stat(fsys, path.Join(dir, f)); err == nil {
			iface.Wireless = true
		}
	}
	return iface, true, nil
}

// netinfoReadWireless parses /proc/net/wireless:
//
//	Inter-| sta-|   Quality        |   Discarded packets               | Missed | WE
//	 face | tus | link level noise |  nwid  crypt   frag  retry   misc | beacon | 22
//	 wlan0: 0000   58.  -52.  -256        0      0      0      0      0        0
//
// A trailing "." marks a value updated since the last read. Every wireless
// interface is listed, but the kernel prints zeroed stats for one without a
// current BSS (down or not connected):
//
//	wlan0: 0000    0     0     0        0      0      0      0      0        0
//
// Those are left out, so only connected interfaces are returned. The file is
// missing without wireless support, which yields no interfaces.
func netinfoReadWireless(fsys fs.FS) (map[string]netinfoWiFi, error) {
	b, err := fs.ReadFile(fsys, "proc/net/wireless")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]netinfoWiFi{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue // header lines have "|" but no ":"
		}
		name = strings.TrimSpace(name)
		fields := strings.Fields(rest)
		if len(fields) < 4 {
			return nil, fmt.Errorf("proc/net/wireless: want at least 4 fields for %s, got %q", name, sc.Text())
		}
		var vals [3]float64
		for i, f := range fields[1:4] {
			v, err := strconv.ParseFloat(strings.TrimRight(f, ".*"), 64)
			if err != nil {
				return nil, fmt.Errorf("proc/net/wireless: %s: %w", name, err)
			}
			vals[i] = v
		}
		link, level, noise := vals[0], vals[1], vals[2]
		if link == 0 && level == 0 && noise == 0 {
			continue // not connected
		}

		// Old wireless extensions drivers report dBm as an unsigned byte
		// (216 for -40 dBm).
		if level > 0 {
			level -= 256
		}
		w := netinfoWiFi{
			SignalDBm:   level,
			LinkQuality: link,
			QualityPct:  round2(math.Min(link/netinfoMaxQuality*100, 100)),
		}
		// -256 (or 0 after the conversion above) means the driver has no
		// noise reading.
		if noise > 0 {
			noise -= 256
		}
		if noise > -256 && noise != 0 {
			w.NoiseDBm = &noise
		}
		out[name] = w
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("proc/net/wireless: %w", err)
	}
	return out, nil
}

// readIwLink fills in the SSID, BSSID and frequency from `iw dev <iface>
// link`. Any failure leaves them out: iw is optional.
func (n *netinfo) readIwLink(ctx context.Context, env *metrics.Env, iface string, w *netinfoWiFi) {
	if env.Runner == nil || env.Now.Before(n.iwRetry) {
		return
	}
	out, err := env.Runner.Run(ctx, "iw", "dev", iface, "link")
	if err != nil {
		// ExecRunner wraps exec.ErrNotFound when iw is not installed. Only
		// the sentinel comes from os/exec; the command runs through
		// env.Runner like everywhere else.
		if errors.Is(err, exec.ErrNotFound) {
			n.iwRetry = env.Now.Add(netinfoIwRetry)
		}
		return
	}
	netinfoParseIwLink(out, w)
}

// netinfoParseIwLink parses the output of `iw dev wlan0 link`:
//
//	Connected to dc:a6:32:01:02:03 (on wlan0)
//		SSID: Home
//		freq: 5180
//		signal: -52 dBm
//		...
//
// or "Not connected.", which leaves w unchanged.
func netinfoParseIwLink(out []byte, w *netinfoWiFi) {
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if rest, ok := strings.CutPrefix(line, "Connected to "); ok {
			if f := strings.Fields(rest); len(f) > 0 {
				w.BSSID = f[0]
			}
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch key {
		case "SSID":
			// Trimming is safe: iw escapes leading and trailing spaces as \x20.
			w.SSID = netinfoUnescapeSSID(val)
		case "freq":
			// Newer iw prints fractional frequencies such as "5180.0".
			if f, err := strconv.ParseFloat(val, 64); err == nil {
				w.FrequencyMHz = int(math.Round(f))
			}
		}
	}
}

// netinfoUnescapeSSID decodes the \xNN escapes iw uses for bytes outside
// printable ASCII, so a UTF-8 SSID ("Caf\xc3\xa9") reads normally.
func netinfoUnescapeSSID(s string) string {
	if !strings.Contains(s, `\x`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+4 <= len(s) && s[i+1] == 'x' {
			if v, err := hex.DecodeString(s[i+2 : i+4]); err == nil {
				b.Write(v)
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// netinfoReadGateway finds the default route in /proc/net/route:
//
//	Iface	Destination	Gateway 	Flags	RefCnt	Use	Metric	Mask	MTU	Window	IRTT
//	wlan0	00000000	0101A8C0	0003	0	0	600	00000000	0	0	0
//
// Addresses are hex in host byte order, which is little-endian on every
// platform pisens runs on. When there are several default routes the one
// with the lowest metric wins, as it does in the kernel. A missing file or
// no default route yields nil.
func netinfoReadGateway(fsys fs.FS) (*netinfoGateway, error) {
	b, err := fs.ReadFile(fsys, "proc/net/route")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	const rtfUp, rtfGateway = 0x1, 0x2

	var best *netinfoGateway
	bestMetric := uint64(math.MaxUint64)
	sc := bufio.NewScanner(bytes.NewReader(b))
	for first := true; sc.Scan(); first = false {
		f := strings.Fields(sc.Text())
		if first || len(f) == 0 {
			continue // header
		}
		if len(f) < 8 {
			return nil, fmt.Errorf("proc/net/route: want at least 8 fields, got %q", sc.Text())
		}
		if f[1] != "00000000" || f[7] != "00000000" {
			continue // not a default route
		}
		flags, err1 := strconv.ParseUint(f[3], 16, 32)
		metric, err2 := strconv.ParseUint(f[6], 10, 64)
		gw, err3 := strconv.ParseUint(f[2], 16, 32)
		if err := errors.Join(err1, err2, err3); err != nil {
			return nil, fmt.Errorf("proc/net/route: %w", err)
		}
		if flags&(rtfUp|rtfGateway) != rtfUp|rtfGateway || metric >= bestMetric {
			continue
		}
		var ip [4]byte
		binary.LittleEndian.PutUint32(ip[:], uint32(gw))
		best = &netinfoGateway{Address: netip.AddrFrom4(ip).String(), Interface: f[0]}
		bestMetric = metric
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("proc/net/route: %w", err)
	}
	return best, nil
}

// netinfoReadValue reads a single-value sysfs file. ok is false if it is
// missing or cannot be read (speed gives EINVAL on Wi-Fi).
func netinfoReadValue(fsys fs.FS, name string) (string, bool) {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(b)), true
}

// netinfoSeriesName makes an interface name safe for a dotted series key:
// lowercase, with anything but a-z, 0-9 and _ turned into _ ("eth0.100" →
// "eth0_100").
func netinfoSeriesName(iface string) string {
	b := []byte(strings.ToLower(iface))
	for i, c := range b {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			b[i] = '_'
		}
	}
	return string(b)
}

// netinfoSystemAddrs is the real netinfoAddrSource.
type netinfoSystemAddrs struct{}

func (netinfoSystemAddrs) Addrs() (map[string][]netip.Prefix, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	out := make(map[string][]netip.Prefix, len(ifaces))
	for _, ifc := range ifaces {
		addrs, err := ifc.Addrs()
		if err != nil {
			continue // the interface went away
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip, ok := netip.AddrFromSlice(ipn.IP)
			if !ok {
				continue
			}
			ones, _ := ipn.Mask.Size()
			out[ifc.Name] = append(out[ifc.Name], netip.PrefixFrom(ip.Unmap(), ones))
		}
	}
	return out, nil
}
