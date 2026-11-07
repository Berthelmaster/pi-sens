import {
  isCollectorError,
  type CollectorError,
  type Snapshot,
} from './snapshot'

/**
 * The `sysinfo` collector's section of the snapshot (sysinfoData in
 * internal/collectors/sysinfo.go). Fields the backend could not read are left
 * out, so every one of them may be missing.
 */
export interface SysinfoSection {
  hostname?: string
  /** Kernel release, e.g. "6.12.47+rpt-rpi-2712". */
  kernel?: string
  /** Kernel architecture, e.g. "aarch64". */
  arch?: string
  /** os-release PRETTY_NAME, e.g. "Debian GNU/Linux 13 (trixie)". */
  os?: string
  board?: BoardInfo
  /** RFC 3339 time the system booted. */
  boot_time?: string
  /** Seconds since boot at the snapshot's timestamp. */
  uptime_s?: number
  procs?: number
  /** Every task the scheduler knows, threads included. */
  threads?: number
  /** Login sessions in utmp; missing when the system keeps no utmp. */
  users?: number
}

/** Board details; empty on hosts that are not a Raspberry Pi. */
export interface BoardInfo {
  /** e.g. "Raspberry Pi 5 Model B Rev 1.0". */
  model?: string
  serial?: string
  /** Pi revision code, e.g. "d04170". */
  revision?: string
  total_ram_bytes?: number
}

/** The sysinfo section of a snapshot, or undefined when it has none. */
export function sysinfoSection(
  snap: Snapshot | null,
): SysinfoSection | CollectorError | undefined {
  const section = snap?.collectors.sysinfo
  if (typeof section !== 'object' || section === null) return undefined
  return section as SysinfoSection | CollectorError
}

/** The Pi's hostname from a snapshot, or null when it is not known. */
export function hostnameOf(snap: Snapshot | null): string | null {
  const section = sysinfoSection(snap)
  if (!section || isCollectorError(section)) return null
  const name = section.hostname
  return typeof name === 'string' && name.trim() ? name.trim() : null
}
