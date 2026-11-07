// Helpers for the History page: the range picker and its URL form, units per
// series key, the default chart groups and turning /api/history answers into
// chart data.

import type uPlot from 'uplot'
import type { HistoryResponse } from '../api'
import type { ChartBand } from '../components/TimeSeriesChart.svelte'
import {
  formatBitsPerSecond,
  formatBytes,
  formatDuration,
  formatHz,
  formatPercent,
  formatTemp,
} from './format'

// ---------------------------------------------------------------------------
// Ranges

export interface RangePreset {
  id: string
  label: string
  seconds: number
}

const HOUR = 3600
const DAY = 24 * HOUR

export const RANGE_PRESETS: readonly RangePreset[] = [
  { id: '1h', label: '1h', seconds: HOUR },
  { id: '6h', label: '6h', seconds: 6 * HOUR },
  { id: '24h', label: '24h', seconds: DAY },
  { id: '7d', label: '7d', seconds: 7 * DAY },
  { id: '30d', label: '30d', seconds: 30 * DAY },
  { id: '90d', label: '90d', seconds: 90 * DAY },
]

export const DEFAULT_PRESET = '24h'

/** Ranges up to this long refresh on their own. */
export const AUTO_REFRESH_MAX = DAY
export const AUTO_REFRESH_MS = 30_000

/** The selected range: a preset ending now, or fixed times in Unix seconds. */
export type RangeSelection =
  { kind: 'preset'; id: string } | { kind: 'custom'; from: number; to: number }

export function presetById(id: string): RangePreset | undefined {
  return RANGE_PRESETS.find((p) => p.id === id)
}

/** The [from, to) Unix seconds a selection stands for at `now`. */
export function resolveRange(
  sel: RangeSelection,
  now: number,
): { from: number; to: number } {
  if (sel.kind === 'custom') return { from: sel.from, to: sel.to }
  const preset = presetById(sel.id) ?? presetById(DEFAULT_PRESET)!
  const to = Math.floor(now)
  return { from: to - preset.seconds, to }
}

/** Whether the page should refetch a selection every AUTO_REFRESH_MS. */
export function autoRefreshes(sel: RangeSelection): boolean {
  if (sel.kind !== 'preset') return false
  const preset = presetById(sel.id)
  return preset !== undefined && preset.seconds <= AUTO_REFRESH_MAX
}

/**
 * Most series on the custom chart: one per colour in the chart palette
 * (SERIES_COLORS in chart.ts, which this file does not import so the tests
 * need no canvas).
 */
export const CUSTOM_MAX_SERIES = 8

/** What the History page keeps in its URL. */
export interface HistoryUrlState {
  range: RangeSelection
  /** Series keys of the custom chart. */
  custom: string[]
}

function unixParam(v: string | null): number | undefined {
  if (v === null || !/^\d{1,12}$/.test(v)) return undefined
  return Number(v)
}

/**
 * Reads the page state from a query string: `range=7d`, or `from` and `to`
 * in Unix seconds for a custom range, and `series=a,b` for the custom chart.
 * Anything unusable falls back to the default range.
 */
export function parseHistoryUrl(search: string): HistoryUrlState {
  const params = new URLSearchParams(search)
  let range: RangeSelection = { kind: 'preset', id: DEFAULT_PRESET }
  const from = unixParam(params.get('from'))
  const to = unixParam(params.get('to'))
  const id = params.get('range')
  if (from !== undefined && to !== undefined && from < to) {
    range = { kind: 'custom', from, to }
  } else if (id && presetById(id)) {
    range = { kind: 'preset', id }
  }
  const custom: string[] = []
  for (const key of (params.get('series') ?? '').split(',')) {
    const k = key.trim()
    if (k && !custom.includes(k) && custom.length < CUSTOM_MAX_SERIES) {
      custom.push(k)
    }
  }
  return { range, custom }
}

/** The query string for a page state, with a leading "?" or empty. */
export function historyQuery(state: HistoryUrlState): string {
  const params = new URLSearchParams()
  if (state.range.kind === 'custom') {
    params.set('from', String(state.range.from))
    params.set('to', String(state.range.to))
  } else {
    params.set('range', state.range.id)
  }
  if (state.custom.length > 0) params.set('series', state.custom.join(','))
  // Keep the commas readable in shared links.
  const qs = params.toString().replaceAll('%2C', ',')
  return qs ? `?${qs}` : ''
}

function pad(n: number): string {
  return String(n).padStart(2, '0')
}

/** Unix seconds as a datetime-local input value in local time. */
export function toLocalInput(unix: number): string {
  const d = new Date(unix * 1000)
  return (
    `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}` +
    `T${pad(d.getHours())}:${pad(d.getMinutes())}`
  )
}

/** A datetime-local input value (local time) as Unix seconds. */
export function fromLocalInput(value: string): number | undefined {
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}/.test(value)) return undefined
  const ms = new Date(value).getTime()
  return Number.isFinite(ms) ? Math.floor(ms / 1000) : undefined
}

// ---------------------------------------------------------------------------
// Units

export type Unit =
  | 'percent'
  | 'celsius'
  | 'bytes'
  | 'bytesPerSecond'
  | 'bitsPerSecond'
  | 'megahertz'
  | 'volts'
  | 'watts'
  | 'rpm'
  | 'seconds'
  | 'dbm'
  | 'number'

const plain = (v: number) =>
  v.toLocaleString(undefined, { maximumFractionDigits: 2 })

export const unitFormat: Record<Unit, (v: number) => string> = {
  percent: (v) => formatPercent(v, Math.abs(v) < 10 && v % 1 !== 0 ? 1 : 0),
  celsius: (v) => formatTemp(v),
  bytes: (v) => formatBytes(v),
  bytesPerSecond: (v) => `${formatBytes(v, { decimal: true })}/s`,
  bitsPerSecond: (v) => formatBitsPerSecond(v),
  megahertz: (v) => formatHz(v * 1e6),
  volts: (v) => `${v.toFixed(3)} V`,
  watts: (v) => `${v.toFixed(2)} W`,
  rpm: (v) => `${Math.round(v)} rpm`,
  seconds: (v) => formatDuration(v),
  dbm: (v) => `${v.toFixed(0)} dBm`,
  number: plain,
}

/** The unit of a series key, from its suffix: `_pct`, `_c`, `_bps`, ... */
export function unitOfKey(key: string): Unit {
  const suffix = key.slice(key.lastIndexOf('.') + 1)
  if (suffix === 'rpm') return 'rpm'
  const unit = suffix.slice(suffix.lastIndexOf('_') + 1)
  switch (unit) {
    case 'pct':
      return 'percent'
    case 'c':
      return 'celsius'
    case 'bytes':
      return 'bytes'
    case 'bps':
      return 'bytesPerSecond'
    case 'mhz':
      return 'megahertz'
    case 'v':
      return 'volts'
    case 'w':
      return 'watts'
    case 's':
      return 'seconds'
    case 'dbm':
      return 'dbm'
    default:
      return 'number'
  }
}

/** The one unit every key shares, or "number" when they differ. */
export function commonUnit(keys: string[]): Unit {
  const units = new Set(keys.map(unitOfKey))
  return units.size === 1 ? [...units][0] : 'number'
}

// ---------------------------------------------------------------------------
// Default chart groups

export interface PanelSeries {
  key: string
  label: string
  /** Multiplier for the stored value, e.g. 8 for bytes to bits. */
  scale?: number
}

export interface Panel {
  /** Shown above the chart when its group has more than one. */
  title: string
  series: PanelSeries[]
  unit: Unit
  min?: number
  max?: number
}

export interface Group {
  id: string
  title: string
  panels: Panel[]
}

/** Up to this many disks go in the disk I/O chart. */
const MAX_DISKS = 5

function present(available: Set<string>, list: PanelSeries[]): PanelSeries[] {
  return list.filter((s) => available.has(s.key))
}

function panel(
  available: Set<string>,
  p: Omit<Panel, 'series'>,
  list: PanelSeries[],
): Panel[] {
  const series = present(available, list)
  return series.length ? [{ ...p, series }] : []
}

/**
 * The default chart groups for the stored series keys. Charts without any
 * of their series are left out, and groups without any chart too.
 */
export function defaultGroups(keys: Iterable<string>): Group[] {
  const available = new Set(keys)

  const disks = [...available]
    .map((k) => /^disk\.(.+)\.read_bps$/.exec(k)?.[1])
    .filter((d): d is string => !!d)
    .sort()
    .slice(0, MAX_DISKS)
  const diskSeries = disks.flatMap((d) => {
    const one = disks.length === 1
    return [
      { key: `disk.${d}.read_bps`, label: one ? 'Read' : `${d} read` },
      { key: `disk.${d}.write_bps`, label: one ? 'Write' : `${d} write` },
    ]
  })

  const fan = available.has('fan.rpm')
    ? panel(available, { title: 'Fan', unit: 'rpm', min: 0 }, [
        { key: 'fan.rpm', label: 'Fan' },
      ])
    : panel(available, { title: 'Fan', unit: 'percent', min: 0, max: 100 }, [
        { key: 'fan.pwm_pct', label: 'Fan duty' },
      ])

  const groups: Group[] = [
    {
      id: 'cpu',
      title: 'CPU usage',
      panels: panel(
        available,
        { title: 'CPU usage', unit: 'percent', min: 0, max: 100 },
        [
          { key: 'cpu.usage_pct', label: 'Usage' },
          { key: 'cpu.iowait_pct', label: 'I/O wait' },
        ],
      ),
    },
    {
      id: 'temperature',
      title: 'CPU temperature and fan',
      panels: [
        ...panel(available, { title: 'Temperature', unit: 'celsius' }, [
          { key: 'temp.cpu_c', label: 'CPU' },
        ]),
        ...fan,
      ],
    },
    {
      id: 'memory',
      title: 'Memory',
      panels: panel(
        available,
        { title: 'Memory', unit: 'percent', min: 0, max: 100 },
        [{ key: 'mem.used_pct', label: 'Used' }],
      ),
    },
    {
      id: 'network',
      title: 'Network',
      panels: panel(
        available,
        { title: 'Network', unit: 'bitsPerSecond', min: 0 },
        [
          { key: 'net.total.rx_bps', label: 'Received', scale: 8 },
          { key: 'net.total.tx_bps', label: 'Sent', scale: 8 },
        ],
      ),
    },
    {
      id: 'disk',
      title: 'Disk I/O',
      panels: panel(
        available,
        { title: 'Disk I/O', unit: 'bytesPerSecond', min: 0 },
        diskSeries,
      ),
    },
    {
      id: 'power',
      title: 'Pi power and voltage',
      panels: [
        ...panel(available, { title: 'Power', unit: 'watts', min: 0 }, [
          { key: 'pi.power_w', label: 'Power' },
        ]),
        // 5 V and ~0.9 V on one axis would flatten both, so they get a chart
        // each.
        ...panel(available, { title: '5V supply', unit: 'volts' }, [
          { key: 'pi.ext5v_v', label: '5V supply' },
        ]),
        ...panel(available, { title: 'Core voltage', unit: 'volts' }, [
          { key: 'pi.core_v', label: 'Core' },
        ]),
      ],
    },
  ]
  return groups.filter((g) => g.panels.length > 0)
}

// ---------------------------------------------------------------------------
// Chart data

export interface ChartData {
  data: uPlot.AlignedData
  /** Min/max per series, only when the points are aggregated buckets. */
  bands?: ChartBand[]
  /** Whether any series has a point. */
  hasData: boolean
}

/** Whether the points of a response stand for more than one sample each. */
export function isAggregated(res: HistoryResponse): boolean {
  return res.resolution !== 'raw' || res.step > 1
}

/**
 * Lines up the series of a history response on shared timestamps for a
 * chart. A bucket only one series lacks is left undefined so the line runs
 * past it; a gap in the data as a whole (the Pi was off) gets a null row so
 * every line breaks there.
 */
export function chartData(
  res: HistoryResponse,
  series: PanelSeries[],
): ChartData {
  const times = new Set<number>()
  for (const s of series) {
    for (const t of res.series[s.key]?.t ?? []) times.add(t)
  }
  const sorted = [...times].sort((a, b) => a - b)

  // A gap is a jump of well over the usual spacing between points.
  const deltas: number[] = []
  for (let i = 1; i < sorted.length; i++) deltas.push(sorted[i] - sorted[i - 1])
  deltas.sort((a, b) => a - b)
  const usual = Math.max(res.step, deltas[Math.floor(deltas.length / 2)] ?? 0)
  const gap = usual * 2.5

  const t: number[] = []
  const breaks = new Set<number>()
  sorted.forEach((ts, i) => {
    if (i > 0 && ts - sorted[i - 1] > gap) {
      breaks.add(t.length)
      t.push(sorted[i - 1] + usual)
    }
    t.push(ts)
  })
  const row = new Map(t.map((ts, i) => [ts, i]))

  const empty = () =>
    t.map((_, i) => (breaks.has(i) ? null : undefined)) as (
      number | null | undefined
    )[]

  const aggregated = isAggregated(res)
  const values: (number | null | undefined)[][] = []
  const bands: ChartBand[] = []
  for (const s of series) {
    const k = s.scale ?? 1
    const src = res.series[s.key]
    const avg = empty()
    const lo = empty()
    const hi = empty()
    src?.t.forEach((ts, j) => {
      const i = row.get(ts)!
      avg[i] = src.avg[j] * k
      lo[i] = src.min[j] * k
      hi[i] = src.max[j] * k
    })
    values.push(avg)
    bands.push({ min: lo, max: hi })
  }

  return {
    data: [t, ...values],
    bands: aggregated ? bands : undefined,
    hasData: sorted.length > 0,
  }
}

/** A bucket size in words: 1 -> "1 second", 360 -> "6 minutes". */
export function describeStep(seconds: number): string {
  const units: [string, number][] = [
    ['day', DAY],
    ['hour', HOUR],
    ['minute', 60],
    ['second', 1],
  ]
  for (const [name, size] of units) {
    if (seconds >= size && seconds % size === 0) {
      const n = seconds / size
      return `${n} ${name}${n === 1 ? '' : 's'}`
    }
  }
  return `${seconds} seconds`
}
