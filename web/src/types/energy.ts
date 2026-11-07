import { num, PLACEHOLDER } from '../lib/format'
import { parseTimestamp, type CollectorError, type Snapshot } from './snapshot'

/**
 * The `energy` collector's section of the snapshot (energyData in
 * internal/collectors/energy.go). Without PMIC readings (a Pi 4 or older, or
 * not a Pi) only `available: false` is sent. The cost fields are only there
 * when the backend has an energy price set.
 */
export interface EnergySection {
  available: boolean
  /** The latest PMIC total power reading; missing when it is stale. */
  power_w?: number
  today_kwh?: number
  yesterday_kwh?: number
  month_kwh?: number
  /** Everything counted since `since`. */
  total_kwh?: number
  /** RFC 3339 time of the first reading ever. */
  since?: string
  today_cost?: number
  month_cost?: number
  /** Free text such as "DKK"; may be empty. */
  currency?: string
}

/** The energy section of a snapshot, or undefined when it has none. */
export function energySection(
  snap: Snapshot | null,
): EnergySection | CollectorError | undefined {
  const s = snap?.collectors.energy
  return typeof s === 'object' && s !== null
    ? (s as EnergySection | CollectorError)
    : undefined
}

/**
 * Formats an amount of energy given in kWh. Below 1 kWh it is shown in Wh,
 * which suits a Pi's few watts: 0.0412 -> "41.2 Wh", 3.1904 -> "3.19 kWh".
 */
export function formatEnergy(kwh: unknown): string {
  const v = num(kwh)
  if (v === undefined) return PLACEHOLDER
  const wh = v * 1000
  // Rounding can push 999.96 Wh up to "1000.0 Wh"; show "1.00 kWh" instead.
  if (Math.abs(Number(wh.toFixed(1))) < 1000) return `${wh.toFixed(1)} Wh`
  return `${v.toFixed(2)} kWh`
}

/**
 * Formats a cost with two decimals and the currency after it, e.g.
 * (1.276, "DKK") -> "1.28 DKK". A cost above zero that would round to 0.00
 * shows as "<0.01". Without a currency only the number is shown, and a
 * missing cost is just the placeholder.
 */
export function formatCost(cost: unknown, currency?: string): string {
  const v = num(cost)
  if (v === undefined) return PLACEHOLDER
  const amount = v > 0 && v < 0.005 ? '<0.01' : v.toFixed(2)
  const unit = currency?.trim()
  return unit ? `${amount} ${unit}` : amount
}

/**
 * Formats the `since` time as a local "YYYY-MM-DD", or undefined when it is
 * missing or not a valid time.
 */
export function formatSince(since: unknown): string | undefined {
  if (typeof since !== 'string') return undefined
  const ms = parseTimestamp(since)
  if (!Number.isFinite(ms)) return undefined
  const d = new Date(ms)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`
}
