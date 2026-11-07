import { PLACEHOLDER } from '../lib/format'
import type { CollectorError, Snapshot } from './snapshot'

/**
 * The `memory` collector's section of the snapshot (memoryData in
 * internal/collectors/memory.go). Byte counts come from /proc/meminfo.
 */
export interface MemorySection {
  total_bytes: number
  available_bytes: number
  /** total - available. */
  used_bytes: number
  used_pct: number
  free_bytes: number
  buffers_bytes: number
  /** Page cache, shared memory (tmpfs) included. */
  cached_bytes: number
  shared_bytes: number
  swap: SwapInfo
  /** Initialised zram devices, swap or not. Empty when there are none. */
  zram: ZramDevice[]
}

export interface SwapInfo {
  /** 0 when no swap is configured. */
  total_bytes: number
  free_bytes: number
  used_bytes: number
  /** 0 when there is no swap. */
  used_pct: number
}

export interface ZramDevice {
  /** e.g. "zram0". */
  device: string
  /** True when the device is in /proc/swaps. */
  swap: boolean
  /** Active compression algorithm, e.g. "zstd"; missing if unknown. */
  algorithm?: string
  disksize_bytes: number
  /** Data stored, before compression. 0 if the kernel has no mm_stat. */
  orig_data_bytes: number
  /** Data stored, after compression. */
  compr_data_bytes: number
  /** RAM the device takes, allocator overhead included. */
  mem_used_bytes: number
  /** orig / compressed; missing while the device is empty. */
  compression_ratio?: number
}

/** The memory section of a snapshot, or undefined when it has none. */
export function memorySection(
  snap: Snapshot | null,
): MemorySection | CollectorError | undefined {
  const section = snap?.collectors.memory
  if (typeof section !== 'object' || section === null) return undefined
  return section as MemorySection | CollectorError
}

/** A positive finite number field, or 0. */
export function num(v: unknown): number {
  return typeof v === 'number' && Number.isFinite(v) && v > 0 ? v : 0
}

export interface Segment {
  key: 'used' | 'cache' | 'free'
  label: string
  /** The real value, for the legend. */
  bytes: number
  /** Share of the bar, 0 to 100. */
  pct: number
}

/**
 * Splits RAM into used, buffers/cache and free for the stacked bar.
 *
 * The three do not add up to the total: "used" is total - available, and
 * the cache also holds shared memory (tmpfs), which is not reclaimable and so
 * already counts as used. The bar keeps used and free as they are and lets
 * the cache segment fill what is left, so the bar is always exactly full.
 * The legend shows the real values.
 */
export function memoryBreakdown(mem: Partial<MemorySection>): Segment[] {
  const total = num(mem.total_bytes)
  const used = num(mem.used_bytes)
  const free = num(mem.free_bytes)
  const cache = num(mem.buffers_bytes) + num(mem.cached_bytes)
  const share = (b: number) => (total > 0 ? (b / total) * 100 : 0)

  const usedPct = Math.min(100, share(used))
  const freePct = Math.min(100 - usedPct, share(free))
  const cachePct = Math.min(share(cache), 100 - usedPct - freePct)
  return [
    { key: 'used', label: 'Used', bytes: used, pct: usedPct },
    { key: 'cache', label: 'Buffers/cache', bytes: cache, pct: cachePct },
    { key: 'free', label: 'Free', bytes: free, pct: freePct },
  ]
}

export interface ZramSummary {
  /** Active algorithms of the zram swap devices, e.g. ["zstd"]. */
  algorithms: string[]
  origBytes: number
  comprBytes: number
  memUsedBytes: number
  /** orig / compressed over every device; undefined while all are empty. */
  ratio: number | undefined
}

/**
 * Totals the zram devices used as swap, or returns undefined when swap is
 * not on zram. The ratio is worked out from the byte counts so several
 * devices weigh by how much they hold.
 */
export function zramSwapSummary(
  devices: ZramDevice[] | undefined,
): ZramSummary | undefined {
  const swap = (Array.isArray(devices) ? devices : []).filter((d) => d?.swap)
  if (swap.length === 0) return undefined
  let origBytes = 0
  let comprBytes = 0
  let memUsedBytes = 0
  const algorithms = new Set<string>()
  for (const d of swap) {
    origBytes += num(d.orig_data_bytes)
    comprBytes += num(d.compr_data_bytes)
    memUsedBytes += num(d.mem_used_bytes)
    if (typeof d.algorithm === 'string' && d.algorithm)
      algorithms.add(d.algorithm)
  }
  return {
    algorithms: [...algorithms],
    origBytes,
    comprBytes,
    memUsedBytes,
    ratio: comprBytes > 0 ? origBytes / comprBytes : undefined,
  }
}

/** Formats a compression ratio, e.g. 2.843 -> "2.8×". */
export function formatRatio(ratio: number | undefined): string {
  return ratio !== undefined && Number.isFinite(ratio)
    ? `${ratio.toFixed(1)}×`
    : PLACEHOLDER
}
