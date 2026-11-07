import type { CollectorError, Snapshot } from './snapshot'

/**
 * The `filesystem` collector's section of the snapshot (filesystemData in
 * internal/collectors/filesystem.go): every real filesystem that is mounted,
 * in mount order.
 */
export interface FilesystemSection {
  mounts?: FilesystemMount[]
}

export interface FilesystemMount {
  /**
   * Key used in the series, `fs.<name>.used_pct`: "root" for /, otherwise
   * the mount point with "/" turned into "_", e.g. "boot_firmware".
   */
  name: string
  /** e.g. "/dev/mmcblk0p2". */
  device: string
  /** e.g. "/boot/firmware". */
  mountpoint: string
  /** e.g. "ext4", "vfat", "nfs4". */
  fstype: string
  read_only: boolean
  // The usage fields are left out when statfs failed or timed out; error
  // then says why.
  size_bytes?: number
  used_bytes?: number
  available_bytes?: number
  /**
   * used / (used + available), like df: blocks reserved for root count as
   * neither, so 100% means ordinary users cannot write any more.
   */
  used_pct?: number
  inodes?: number
  inodes_used?: number
  inodes_free?: number
  /** 0 on filesystems without inodes, such as vfat. */
  inodes_used_pct?: number
  error?: string
}

/**
 * The `diskio` collector's section of the snapshot (diskioData in
 * internal/collectors/diskio.go): the whole physical disks, in
 * /proc/diskstats order.
 */
export interface DiskioSection {
  devices?: DiskDevice[]
}

export interface DiskDevice {
  /** Kernel name, e.g. "mmcblk0", "nvme0n1", "sda". */
  name: string
  /** Drive model or SD card name; missing when the kernel has none. */
  model?: string
  /** Totals since boot. */
  read_bytes: number
  write_bytes: number
  reads: number
  writes: number
  /**
   * Rates since the previous sample; missing on the first sample and after
   * the counters went backwards.
   */
  rates?: DiskRates
}

export interface DiskRates {
  /** Bytes per second. */
  read_bps: number
  write_bps: number
  read_iops: number
  write_iops: number
  /** Share of the time the device had I/O in flight, 0 to 100. */
  util_pct: number
}

/** Series keys the diskio collector writes for a device. */
export function diskSeriesKey(
  device: string,
  metric: 'read_bps' | 'write_bps' | 'util_pct',
): string {
  return `disk.${device}.${metric}`
}

function section(snap: Snapshot | null, name: string): unknown {
  const s = snap?.collectors[name]
  return typeof s === 'object' && s !== null ? s : undefined
}

/** The filesystem section of a snapshot, or undefined when it has none. */
export function filesystemSection(
  snap: Snapshot | null,
): FilesystemSection | CollectorError | undefined {
  return section(snap, 'filesystem') as
    FilesystemSection | CollectorError | undefined
}

/** The diskio section of a snapshot, or undefined when it has none. */
export function diskioSection(
  snap: Snapshot | null,
): DiskioSection | CollectorError | undefined {
  return section(snap, 'diskio') as DiskioSection | CollectorError | undefined
}
