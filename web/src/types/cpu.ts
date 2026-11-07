import { formatHz, PLACEHOLDER } from '../lib/format'
import type { CollectorError, Snapshot } from './snapshot'

/**
 * The `cpu` collector's section of the snapshot (cpuData in
 * internal/collectors/cpu.go).
 */
export interface CpuSection {
  /** Online cores in /proc/stat. */
  cores: number
  /**
   * Usage since the previous sample. Missing on the first sample, which has
   * nothing to compare against.
   */
  usage?: CpuUsage
  /** Usage per core; cores without a usable previous sample are left out. */
  per_core?: CpuCoreUsage[]
  load: LoadAvg
}

/** Shares of the time since the previous sample, adding up to 100. */
export interface CpuUsage {
  /** Everything except idle and iowait. */
  usage_pct: number
  /** user + nice, guest time included. */
  user_pct: number
  system_pct: number
  iowait_pct: number
  /** irq + softirq. */
  irq_pct: number
  steal_pct: number
  idle_pct: number
}

export interface CpuCoreUsage extends CpuUsage {
  core: number
}

/** /proc/loadavg. */
export interface LoadAvg {
  load1: number
  load5: number
  load15: number
  running_tasks: number
  total_tasks: number
}

/**
 * The `cpufreq` collector's section (cpufreqData in
 * internal/collectors/cpufreq.go). `cores` is empty on machines without
 * cpufreq, such as most VMs.
 */
export interface CpufreqSection {
  /** Mean current frequency of all cores; missing when there are none. */
  avg_mhz?: number
  cores: CpufreqCore[]
}

export interface CpufreqCore {
  core: number
  cur_mhz: number
  min_mhz?: number
  max_mhz?: number
  governor?: string
}

function section(snap: Snapshot | null, name: string): object | undefined {
  const s = snap?.collectors[name]
  return typeof s === 'object' && s !== null ? s : undefined
}

/** The cpu section of a snapshot, or undefined when it has none. */
export function cpuSection(
  snap: Snapshot | null,
): CpuSection | CollectorError | undefined {
  return section(snap, 'cpu') as CpuSection | CollectorError | undefined
}

/** The cpufreq section of a snapshot, or undefined when it has none. */
export function cpufreqSection(
  snap: Snapshot | null,
): CpufreqSection | CollectorError | undefined {
  return section(snap, 'cpufreq') as CpufreqSection | CollectorError | undefined
}

/**
 * iowait is shown once it reaches this share at any point in the history
 * window. Using the window rather than only the latest sample keeps the
 * row from blinking in and out while I/O comes and goes.
 */
export const IOWAIT_SHOWN_PCT = 1

export interface CoreRow {
  core: number
  /** Usage percentage; undefined until the collector has two samples. */
  usage?: number
  /** Current frequency in MHz; undefined without cpufreq. */
  mhz?: number
}

/** A finite number, or undefined. */
export function num(v: unknown): number | undefined {
  return typeof v === 'number' && Number.isFinite(v) ? v : undefined
}

/**
 * One row per core, joining usage from the cpu section with frequencies
 * from the cpufreq section by core number. Either side may be missing. On
 * the first sample the cpu section has no per-core usage yet, so rows fall
 * back to its core count to keep the list from jumping in a sample later.
 */
export function coreRows(
  cpu: Partial<CpuSection> | undefined,
  freq: Partial<CpufreqSection> | undefined,
): CoreRow[] {
  const rows = new Map<number, CoreRow>()
  const row = (core: number) => {
    let r = rows.get(core)
    if (!r) rows.set(core, (r = { core }))
    return r
  }
  for (const c of Array.isArray(cpu?.per_core) ? cpu.per_core : []) {
    if (num(c?.core) !== undefined) row(c.core).usage = num(c.usage_pct)
  }
  for (const c of Array.isArray(freq?.cores) ? freq.cores : []) {
    if (num(c?.core) !== undefined) row(c.core).mhz = num(c.cur_mhz)
  }
  const count = num(cpu?.cores) ?? 0
  if (rows.size === 0) {
    for (let i = 0; i < count; i++) row(i)
  }
  return [...rows.values()].sort((a, b) => a.core - b.core)
}

/** The highest scaling_max_freq of any core in MHz, or undefined. */
export function maxMhz(
  freq: Partial<CpufreqSection> | undefined,
): number | undefined {
  const values = (Array.isArray(freq?.cores) ? freq.cores : [])
    .map((c) => num(c?.max_mhz))
    .filter((v): v is number => v !== undefined && v > 0)
  return values.length > 0 ? Math.max(...values) : undefined
}

/**
 * The scaling governor, or the distinct ones joined with commas when cores
 * differ. undefined when no core reports one.
 */
export function governors(
  freq: Partial<CpufreqSection> | undefined,
): string | undefined {
  const names = new Set<string>()
  for (const c of Array.isArray(freq?.cores) ? freq.cores : []) {
    const g = typeof c?.governor === 'string' ? c.governor.trim() : ''
    if (g) names.add(g)
  }
  return names.size > 0 ? [...names].join(', ') : undefined
}

/** Whether iowait is worth a row: latest is the current value. */
export function showIowait(
  latest: number | undefined,
  history: readonly number[],
): boolean {
  if (latest === undefined) return false
  return (
    latest >= IOWAIT_SHOWN_PCT || history.some((v) => v >= IOWAIT_SHOWN_PCT)
  )
}

/** A load average with two decimals, as uptime and top show it. */
export function formatLoad(v: number | undefined): string {
  return v === undefined ? PLACEHOLDER : v.toFixed(2)
}

/** A frequency in MHz, e.g. 2400 -> "2.4 GHz". */
export function formatMhz(mhz: number | undefined): string {
  return formatHz(mhz === undefined ? undefined : mhz * 1e6)
}
