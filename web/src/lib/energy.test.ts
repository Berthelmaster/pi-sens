import { describe, expect, it } from 'vitest'
import {
  formatCost,
  formatKWh,
  hasEnergyData,
  labelEvery,
  niceTicks,
  parseDate,
  tickDecimals,
} from './energy'

describe('formatKWh', () => {
  it('keeps three significant digits for a Pi-sized day', () => {
    expect(formatKWh(0.1234)).toBe('0.123 kWh')
    expect(formatKWh(1.2345)).toBe('1.23 kWh')
    expect(formatKWh(12.345)).toBe('12.3 kWh')
    expect(formatKWh(0)).toBe('0.000 kWh')
  })
})

describe('formatCost', () => {
  it('shows two decimals and the currency when there is one', () => {
    expect(formatCost(0.3025, 'DKK')).toBe('0.30 DKK')
    expect(formatCost(1.5)).toBe('1.50')
    expect(formatCost(1.5, '')).toBe('1.50')
  })
})

describe('niceTicks', () => {
  it('covers the maximum with round steps from zero', () => {
    expect(niceTicks(0.143)).toEqual([0, 0.05, 0.1, 0.15])
    expect(niceTicks(0.3)).toEqual([0, 0.1, 0.2, 0.3])
    expect(niceTicks(7, 4)).toEqual([0, 2, 4, 6, 8])
    expect(niceTicks(0.143, 3)).toEqual([0, 0.05, 0.1, 0.15])
  })

  it('has a usable axis without data', () => {
    expect(niceTicks(0)).toEqual([0, 1])
    expect(niceTicks(NaN)).toEqual([0, 1])
  })
})

describe('tickDecimals', () => {
  it('shows as many decimals as the step needs', () => {
    expect(tickDecimals(0.05)).toBe(2)
    expect(tickDecimals(0.1)).toBe(1)
    expect(tickDecimals(1)).toBe(0)
    expect(tickDecimals(20)).toBe(0)
  })
})

describe('labelEvery', () => {
  it('spaces labels so they do not overlap', () => {
    expect(labelEvery(60, 48)).toBe(1)
    expect(labelEvery(20, 48)).toBe(3)
    expect(labelEvery(0, 48)).toBe(1)
  })
})

describe('parseDate', () => {
  it('reads the date as local midnight', () => {
    const d = parseDate('2026-11-07')
    expect([d.getFullYear(), d.getMonth(), d.getDate(), d.getHours()]).toEqual([
      2026, 10, 7, 0,
    ])
  })
})

describe('hasEnergyData', () => {
  it('is true once any day has a value', () => {
    expect(hasEnergyData([])).toBe(false)
    expect(
      hasEnergyData([
        { date: '2026-11-06', start: 0, kwh: null },
        { date: '2026-11-07', start: 0, kwh: 0 },
      ]),
    ).toBe(true)
  })
})
