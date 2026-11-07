import type { CollectorError, Snapshot } from './snapshot'

/**
 * The `processes` collector's section of the snapshot (processesData in
 * internal/collectors/processes.go).
 */
export interface ProcessesSection {
  /** Number of processes read this sample. */
  count: number
  /**
   * The union of the top 15 by CPU and the top 15 by memory, each process
   * once. Empty when there are none.
   */
  processes: ProcessInfo[]
}

export interface ProcessInfo {
  pid: number
  /** The kernel's short name (comm), at most 15 characters. */
  name: string
  /**
   * Full command line, cut at 1 KiB. Kernel threads and zombies show their
   * name in brackets: "[kworker/0:1]". By default the values of secret
   * looking arguments are replaced by "***"; with --process-cmdline=off it
   * is just the name.
   */
  command: string
  /** User name of the real uid, or the uid as a number string. */
  user: string
  /** One-letter state: R running, S sleeping, D waiting on I/O, Z zombie, ... */
  state: string
  threads: number
  /**
   * 100 is one full core, so a multi-threaded process can go above it.
   * Missing on the collector's first sample and for a process that started
   * since the previous one.
   */
  cpu_pct?: number
  rss_bytes: number
}

/** The processes section of a snapshot, or undefined when it has none. */
export function processesSection(
  snap: Snapshot | null,
): ProcessesSection | CollectorError | undefined {
  const section = snap?.collectors.processes
  if (typeof section !== 'object' || section === null) return undefined
  return section as ProcessesSection | CollectorError
}

export type ProcessSortKey = 'cpu' | 'mem'
export type SortDirection = 'asc' | 'desc'

export interface ProcessSort {
  key: ProcessSortKey
  dir: SortDirection
}

export const DEFAULT_SORT: ProcessSort = { key: 'cpu', dir: 'desc' }

/**
 * The sort after clicking a column header: the same column flips direction,
 * a new one starts high to low.
 */
export function nextSort(
  current: ProcessSort,
  key: ProcessSortKey,
): ProcessSort {
  if (current.key !== key) return { key, dir: 'desc' }
  return { key, dir: current.dir === 'desc' ? 'asc' : 'desc' }
}

function finite(v: unknown): number | undefined {
  return typeof v === 'number' && Number.isFinite(v) ? v : undefined
}

/**
 * Returns a sorted copy of the list. A missing CPU value sorts last in both
 * directions, since it means "not known yet", not zero. Ties fall back to
 * the other column, high to low, then the pid, so rows do not swap places
 * between updates when their values are equal.
 */
export function sortProcesses(
  list: readonly ProcessInfo[],
  sort: ProcessSort,
): ProcessInfo[] {
  const sign = sort.dir === 'desc' ? -1 : 1
  const cpu = (p: ProcessInfo) => finite(p.cpu_pct)
  const mem = (p: ProcessInfo) => finite(p.rss_bytes) ?? 0

  const byCpu = (a: ProcessInfo, b: ProcessInfo, s: number) => {
    const ca = cpu(a)
    const cb = cpu(b)
    if (ca === undefined || cb === undefined) {
      if (ca === cb) return 0
      return ca === undefined ? 1 : -1
    }
    return (ca - cb) * s
  }
  const byMem = (a: ProcessInfo, b: ProcessInfo, s: number) =>
    (mem(a) - mem(b)) * s

  return [...list].sort((a, b) => {
    const first = sort.key === 'cpu' ? byCpu(a, b, sign) : byMem(a, b, sign)
    if (first !== 0) return first
    const second = sort.key === 'cpu' ? byMem(a, b, -1) : byCpu(a, b, -1)
    if (second !== 0) return second
    return a.pid - b.pid
  })
}

const stateNames: Record<string, string> = {
  R: 'Running',
  S: 'Sleeping',
  D: 'Waiting on I/O',
  I: 'Idle',
  Z: 'Zombie',
  T: 'Stopped',
  t: 'Stopped by debugger',
  X: 'Dead',
  P: 'Parked',
  W: 'Paging',
  K: 'Wakekill',
}

/** A readable name for a one-letter process state, e.g. "S" -> "Sleeping". */
export function processStateName(state: string): string {
  return stateNames[state] ?? state
}
