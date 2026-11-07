// Display formatters for metric values. They are locale independent so the
// dashboard looks the same everywhere, and every one of them returns PLACEHOLDER
// for a missing or non-finite value, so widgets can pass series values straight
// through.

/** Shown in place of a value that is missing or not a number. */
export const PLACEHOLDER = '–'

type Value = number | null | undefined

function isNumber(v: Value): v is number {
  return typeof v === 'number' && Number.isFinite(v)
}

/**
 * Narrows an untyped snapshot field to a finite number, or undefined for
 * anything else (missing, null, NaN, Infinity, strings...).
 */
export function num(v: unknown): number | undefined {
  return typeof v === 'number' && Number.isFinite(v) ? v : undefined
}

/**
 * Scales value down by step until it is below step and formats it with the
 * matching unit. Values in the base unit are shown without decimals.
 */
function scaled(
  value: number,
  step: number,
  units: readonly string[],
  decimals: number,
  trimZeros = false,
): string {
  let v = Math.abs(value)
  let i = 0
  while (i < units.length - 1 && v >= step) {
    v /= step
    i++
  }
  const digits = () => (i === 0 ? 0 : decimals)
  // Rounding can push a value up to the next unit: 1023.96 KiB would show as
  // "1024.0 KiB", so show "1.0 MiB" instead.
  if (i < units.length - 1 && Number(v.toFixed(digits())) >= step) {
    v /= step
    i++
  }
  const sign = value < 0 ? '-' : ''
  let num = v.toFixed(digits())
  if (trimZeros && num.includes('.')) num = num.replace(/\.?0+$/, '')
  return `${sign}${num} ${units[i]}`
}

const binaryUnits = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'] as const
const decimalUnits = ['B', 'kB', 'MB', 'GB', 'TB', 'PB'] as const

export interface BytesOptions {
  /** Digits after the decimal point once a value is scaled. Default 1. */
  decimals?: number
  /**
   * Use decimal units (kB, MB, GB, steps of 1000) instead of binary ones (KiB,
   * MiB, GiB, steps of 1024). Disk makers label sizes in decimal units.
   */
  decimal?: boolean
}

/** Formats a byte count, e.g. 1536 -> "1.5 KiB". */
export function formatBytes(bytes: Value, opts: BytesOptions = {}): string {
  if (!isNumber(bytes)) return PLACEHOLDER
  const { decimals = 1, decimal = false } = opts
  return decimal
    ? scaled(bytes, 1000, decimalUnits, decimals)
    : scaled(bytes, 1024, binaryUnits, decimals)
}

const bitRateUnits = ['b/s', 'kb/s', 'Mb/s', 'Gb/s', 'Tb/s'] as const

/** Formats a bit rate with decimal units, e.g. 12_500_000 -> "12.5 Mb/s". */
export function formatBitsPerSecond(bps: Value, decimals = 1): string {
  if (!isNumber(bps)) return PLACEHOLDER
  return scaled(bps, 1000, bitRateUnits, decimals)
}

/** Formats a value that is already a percentage, e.g. 42.345 -> "42.3%". */
export function formatPercent(pct: Value, decimals = 1): string {
  if (!isNumber(pct)) return PLACEHOLDER
  return `${pct.toFixed(decimals)}%`
}

const durationUnits = [
  ['d', 86400],
  ['h', 3600],
  ['m', 60],
  ['s', 1],
] as const

/**
 * Formats a number of seconds as its two largest non-zero units, e.g.
 * 93784 -> "1d 2h", 125 -> "2m 5s". Fractions of a second are dropped.
 */
export function formatDuration(seconds: Value): string {
  if (!isNumber(seconds)) return PLACEHOLDER
  let rest = Math.floor(Math.abs(seconds))
  const parts: string[] = []
  for (const [unit, size] of durationUnits) {
    const n = Math.floor(rest / size)
    rest -= n * size
    if (n > 0 || parts.length > 0) parts.push(`${n}${unit}`)
    if (parts.length === 2) break
  }
  if (parts.length === 0) return '0s'
  // Drop a trailing zero unit: "2h 0m" reads better as "2h".
  if (parts.length === 2 && parts[1].startsWith('0')) parts.pop()
  return (seconds < 0 ? '-' : '') + parts.join(' ')
}

/** Formats a temperature in degrees Celsius, e.g. 52.34 -> "52.3 °C". */
export function formatTemp(celsius: Value, decimals = 1): string {
  if (!isNumber(celsius)) return PLACEHOLDER
  return `${celsius.toFixed(decimals)} °C`
}

const hzUnits = ['Hz', 'kHz', 'MHz', 'GHz', 'THz'] as const

/**
 * Formats a frequency given in hertz with up to decimals digits, dropping
 * trailing zeros: 2_400_000_000 -> "2.4 GHz", 600_000_000 -> "600 MHz".
 * Series in MHz (cpu.freq_mhz) need multiplying by 1e6 first.
 */
export function formatHz(hz: Value, decimals = 2): string {
  if (!isNumber(hz)) return PLACEHOLDER
  return scaled(hz, 1000, hzUnits, decimals, true)
}
