// Helpers for the "Energy per day" chart on the History page.

import type { EnergyDay } from '../api'

/** Whether any day has a stored value. */
export function hasEnergyData(days: EnergyDay[]): boolean {
  return days.some((d) => d.kwh !== null)
}

/**
 * Formats kWh with enough digits for a Pi, which uses a tenth of a kWh or
 * so a day: 0.123 kWh, 1.23 kWh, 12.3 kWh.
 */
export function formatKWh(kwh: number): string {
  const abs = Math.abs(kwh)
  const decimals = abs < 1 ? 3 : abs < 10 ? 2 : 1
  return `${kwh.toFixed(decimals)} kWh`
}

/** Formats a cost with two decimals and the currency, if there is one. */
export function formatCost(cost: number, currency?: string): string {
  const v = cost.toFixed(2)
  return currency ? `${v} ${currency}` : v
}

/**
 * Ticks for a value axis from 0 that covers `max`: 0 and about `count`
 * evenly spaced round values (1, 2 or 5 times a power of ten), the last at
 * or above max.
 */
export function niceTicks(max: number, count = 4): number[] {
  if (!(max > 0) || !Number.isFinite(max)) return [0, 1]
  const raw = max / count
  const pow = 10 ** Math.floor(Math.log10(raw))
  const step = ([1, 2, 5, 10].find((m) => m * pow >= raw) ?? 10) * pow
  const ticks: number[] = []
  for (let i = 0; ; i++) {
    // Rounded so 0.1 * 3 reads 0.3, not 0.30000000000000004.
    const v = Number((i * step).toPrecision(12))
    ticks.push(v)
    if (v >= max) return ticks
  }
}

/** Decimals a tick label needs for ticks `step` apart. */
export function tickDecimals(step: number): number {
  if (!(step > 0)) return 0
  return Math.max(0, -Math.floor(Math.log10(step) + 1e-9))
}

/** The calendar date "2026-11-07" as a local Date at midnight. */
export function parseDate(date: string): Date {
  const [y, m, d] = date.split('-').map(Number)
  return new Date(y, m - 1, d)
}

/**
 * How many bars apart the date labels go when each bar is `slot` pixels
 * wide and a label needs `labelWidth`, so they never overlap.
 */
export function labelEvery(slot: number, labelWidth: number): number {
  if (!(slot > 0)) return 1
  return Math.max(1, Math.ceil(labelWidth / slot))
}
