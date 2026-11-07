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
  /** MemTotal: the RAM the kernel can use, a little under what is fitted. */
  total_ram_bytes?: number
  // Decoded from a new-style revision code; missing for older boards.
  /** Board type, e.g. "5", "4B", "Zero 2 W". */
  type?: string
  /** SoC, e.g. "BCM2712". */
  processor?: string
  /** e.g. "Sony UK". */
  manufacturer?: string
  /** RAM fitted to the board, e.g. 8589934592 for 8 GB. */
  fitted_ram_bytes?: number
  /** PCB revision, e.g. "1.4". */
  pcb_revision?: string
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
