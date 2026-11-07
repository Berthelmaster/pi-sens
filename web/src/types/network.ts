import { formatBitsPerSecond, num, PLACEHOLDER } from '../lib/format'
import {
  isCollectorError,
  type CollectorError,
  type Snapshot,
} from './snapshot'

/**
 * The `network` collector's section of the snapshot (networkData in
 * internal/collectors/network.go): cumulative /proc/net/dev counters and
 * rates per interface. Loopback and Docker's veth, docker* and br-*
 * interfaces are left out.
 */
export interface NetworkSection {
  /** Sorted by name; empty when there are none. */
  interfaces: NetworkInterface[]
  total: NetworkTotal
}

/** Counters since the interface came up. */
export interface NetCounters {
  rx_bytes: number
  rx_packets: number
  rx_errors: number
  rx_dropped: number
  tx_bytes: number
  tx_packets: number
  tx_errors: number
  tx_dropped: number
}

/** Per second. The `_bps` rates are bytes, not bits, per second. */
export interface NetRates {
  rx_bps: number
  tx_bps: number
  rx_pps: number
  tx_pps: number
}

export interface NetworkInterface extends NetCounters {
  name: string
  /** Missing on the first sample of the interface and after a counter reset. */
  rates?: NetRates
}

export interface NetworkTotal extends NetCounters {
  /** Missing on the collector's first sample. */
  rates?: NetRates
}

/**
 * The `netinfo` collector's section (netinfoData in
 * internal/collectors/netinfo.go): state, addresses and Wi-Fi link per
 * interface, and the default gateway.
 */
export interface NetinfoSection {
  interfaces: NetinfoInterface[]
  /** Missing when there is no default IPv4 route. */
  default_gateway?: NetGateway
}

/** RFC 2863 operational state from sysfs. */
export type OperState =
  | 'up'
  | 'down'
  | 'dormant'
  | 'lowerlayerdown'
  | 'notpresent'
  | 'testing'
  | 'unknown'

export interface NetinfoInterface {
  name: string
  operstate: OperState | string
  mac?: string
  /** Negotiated link speed; missing for Wi-Fi, virtual and unplugged links. */
  speed_mbps?: number
  mtu: number
  wireless: boolean
  /** CIDR notation, e.g. 192.168.1.20/24. */
  ipv4: string[]
  ipv6: string[]
  /** Set for wireless interfaces that are connected. */
  wifi?: NetWifi
}

export interface NetWifi {
  /** SSID, BSSID and frequency come from iw and are missing without it. */
  ssid?: string
  bssid?: string
  frequency_mhz?: number
  signal_dbm: number
  /** Raw /proc/net/wireless value, out of 70. */
  link_quality: number
  quality_pct: number
  noise_dbm?: number
}

export interface NetGateway {
  address: string
  interface: string
}

function section(snap: Snapshot | null, name: string): object | undefined {
  const s = snap?.collectors[name]
  return typeof s === 'object' && s !== null ? s : undefined
}

/** The network section of a snapshot, or undefined when it has none. */
export function networkSection(
  snap: Snapshot | null,
): Partial<NetworkSection> | CollectorError | undefined {
  return section(snap, 'network') as
    Partial<NetworkSection> | CollectorError | undefined
}

/** The netinfo section of a snapshot, or undefined when it has none. */
export function netinfoSection(
  snap: Snapshot | null,
): Partial<NetinfoSection> | CollectorError | undefined {
  return section(snap, 'netinfo') as
    Partial<NetinfoSection> | CollectorError | undefined
}

/** A non-empty trimmed string, or undefined. */
export function text(v: unknown): string | undefined {
  return typeof v === 'string' && v.trim() !== '' ? v.trim() : undefined
}

function strings(v: unknown): string[] {
  return Array.isArray(v)
    ? v.filter((s): s is string => typeof s === 'string' && s !== '')
    : []
}

/**
 * The name each interface has in the network collector's series keys,
 * `net.<name>.rx_bps`, mirroring netSeriesNames in network.go: lowercase,
 * everything other than a-z, 0-9 and _ turned into _, "total" reserved, and
 * _2, _3, ... appended to a name that is already taken. Names are handled
 * in sorted order, as the backend does, so the suffixes match.
 */
export function netSeriesNames(names: readonly string[]): Map<string, string> {
  const taken = new Set(['total'])
  const out = new Map<string, string>()
  // Go sorts by bytes; for the ASCII names interfaces have in practice that
  // is the same as comparing UTF-16 code units.
  for (const name of [...new Set(names)].sort((a, b) =>
    a < b ? -1 : a > b ? 1 : 0,
  )) {
    const base = Array.from(name.toLowerCase(), (c) =>
      /[a-z0-9_]/.test(c) ? c : '_',
    ).join('')
    let unique = base
    for (let i = 2; taken.has(unique); i++) unique = `${base}_${i}`
    taken.add(unique)
    out.set(name, unique)
  }
  return out
}

/** One interface as the widget shows it, from both collectors. */
export interface InterfaceView {
  name: string
  /** Lowercase operstate; "unknown" when netinfo has nothing on it. */
  state: string
  /** False for interfaces that are down; the widget greys those out. */
  active: boolean
  mac?: string
  speedMbps?: number
  wireless: boolean
  ipv4: string[]
  ipv6: string[]
  wifi?: NetWifi
  /** Bits per second, when the network collector has rates this sample. */
  rxBits?: number
  txBits?: number
  /** Bytes since the interface came up. */
  rxBytes?: number
  txBytes?: number
  /** Series key prefix, e.g. "net.eth0"; missing without traffic counters. */
  seriesKey?: string
  /** Whether this interface carries the default route. */
  gateway: boolean
}

/**
 * Whether an interface in this state is carrying traffic. "unknown" counts
 * as active: tun and WireGuard interfaces always report it while up.
 */
export function isActiveState(state: string): boolean {
  return state === 'up' || state === 'unknown'
}

/**
 * Joins the two collectors' interface lists by name, sorted by name. Either
 * section may be missing or failed; the interfaces of the other are still
 * shown.
 */
export function interfaceViews(
  network: Partial<NetworkSection> | undefined,
  netinfo: Partial<NetinfoSection> | undefined,
): InterfaceView[] {
  const counters = new Map<string, NetworkInterface>()
  for (const c of Array.isArray(network?.interfaces)
    ? network.interfaces
    : []) {
    if (typeof c?.name === 'string') counters.set(c.name, c)
  }
  const infos = new Map<string, NetinfoInterface>()
  for (const i of Array.isArray(netinfo?.interfaces)
    ? netinfo.interfaces
    : []) {
    if (typeof i?.name === 'string') infos.set(i.name, i)
  }
  const keys = netSeriesNames([...counters.keys()])
  const gw = text(netinfo?.default_gateway?.interface)

  const names = [...new Set([...infos.keys(), ...counters.keys()])].sort(
    (a, b) => (a < b ? -1 : a > b ? 1 : 0),
  )
  return names.map((name) => {
    const info = infos.get(name)
    const c = counters.get(name)
    const state = text(info?.operstate)?.toLowerCase() ?? 'unknown'
    const rx = num(c?.rates?.rx_bps)
    const tx = num(c?.rates?.tx_bps)
    const speed = num(info?.speed_mbps)
    const key = keys.get(name)
    return {
      name,
      state,
      active: isActiveState(state),
      mac: text(info?.mac),
      speedMbps: speed !== undefined && speed > 0 ? speed : undefined,
      wireless: info?.wireless === true || !!info?.wifi,
      ipv4: strings(info?.ipv4),
      ipv6: strings(info?.ipv6),
      wifi:
        typeof info?.wifi === 'object' && info.wifi !== null
          ? info.wifi
          : undefined,
      rxBits: rx === undefined ? undefined : rx * 8,
      txBits: tx === undefined ? undefined : tx * 8,
      rxBytes: num(c?.rx_bytes),
      txBytes: num(c?.tx_bytes),
      seriesKey: key === undefined ? undefined : `net.${key}`,
      gateway: gw === name,
    }
  })
}

/** The usable part of a section: undefined when it is missing or failed. */
export function okSection<T extends object>(
  s: T | CollectorError | undefined,
): T | undefined {
  return s && !isCollectorError(s) ? s : undefined
}

/**
 * Formats a link speed in Mb/s, e.g. 1000 -> "1 Gb/s", 2500 -> "2.5 Gb/s",
 * 100 -> "100 Mb/s".
 */
export function formatLinkSpeed(mbps: number | undefined): string {
  if (mbps === undefined || !Number.isFinite(mbps) || mbps <= 0)
    return PLACEHOLDER
  if (mbps < 1000) return formatBitsPerSecond(mbps * 1e6, 0)
  const gbps = mbps / 1000
  return `${Number.isInteger(gbps) ? gbps : gbps.toFixed(1).replace(/\.0$/, '')} Gb/s`
}

/** Number of bars in the Wi-Fi signal meter. */
export const SIGNAL_BARS = 4

/**
 * How many of SIGNAL_BARS bars a Wi-Fi signal fills. The steps follow the
 * usual reading of RSSI: -55 dBm or better is excellent, -67 good enough for
 * anything, -75 usable, -85 barely connected.
 */
export function signalBars(dbm: number | undefined): number {
  if (dbm === undefined || !Number.isFinite(dbm)) return 0
  if (dbm >= -55) return 4
  if (dbm >= -67) return 3
  if (dbm >= -75) return 2
  if (dbm >= -85) return 1
  return 0
}

/**
 * Rounds a positive value up to the next 1, 2 or 5 times a power of ten, so
 * a shared sparkline scale only changes in steps rather than on every
 * sample. Anything not above zero gives 1.
 */
export function niceCeil(v: number): number {
  if (!Number.isFinite(v) || v <= 0) return 1
  const pow = 10 ** Math.floor(Math.log10(v))
  for (const step of [1, 2, 5, 10]) {
    // A little slack so floating point noise (3 * 0.1) does not skip a step.
    if (v <= step * pow * (1 + 1e-9)) return step * pow
  }
  return 10 * pow
}

/** Human-readable operstate for the badge. */
export function stateLabel(state: string): string {
  switch (state) {
    case 'up':
      return 'Up'
    case 'down':
      return 'Down'
    case 'dormant':
      return 'Dormant'
    case 'lowerlayerdown':
      return 'No carrier'
    case 'notpresent':
      return 'Not present'
    case 'testing':
      return 'Testing'
    default:
      return 'Unknown'
  }
}

/** Tells IPv6 link-local addresses (fe80::/10) apart, to show them muted. */
export function isLinkLocal6(cidr: string): boolean {
  return /^fe[89ab][0-9a-f]:/i.test(cidr)
}
