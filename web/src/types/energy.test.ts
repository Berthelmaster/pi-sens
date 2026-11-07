import { describe, expect, it } from 'vitest'
import { energySection, formatCost, formatEnergy, formatSince } from './energy'
import type { Snapshot } from './snapshot'

describe('formatEnergy', () => {
  it('shows Wh below 1 kWh', () => {
    expect(formatEnergy(0.0412)).toBe('41.2 Wh')
    expect(formatEnergy(0.5208)).toBe('520.8 Wh')
    expect(formatEnergy(0.00004)).toBe('0.0 Wh')
    expect(formatEnergy(0)).toBe('0.0 Wh')
  })

  it('shows kWh from 1 kWh up', () => {
    expect(formatEnergy(1)).toBe('1.00 kWh')
    expect(formatEnergy(3.1904)).toBe('3.19 kWh')
    expect(formatEnergy(123.456)).toBe('123.46 kWh')
  })

  it('switches to kWh when the Wh value would round up to 1000', () => {
    expect(formatEnergy(0.99994)).toBe('999.9 Wh')
    expect(formatEnergy(0.99996)).toBe('1.00 kWh')
  })

  it('shows a placeholder for a missing value', () => {
    expect(formatEnergy(undefined)).toBe('–')
    expect(formatEnergy(null)).toBe('–')
    expect(formatEnergy(NaN)).toBe('–')
    expect(formatEnergy('1')).toBe('–')
  })
})

describe('formatCost', () => {
  it('puts the currency after the amount', () => {
    expect(formatCost(1.276, 'DKK')).toBe('1.28 DKK')
    expect(formatCost(0.1009, 'EUR')).toBe('0.10 EUR')
    expect(formatCost(12, ' € ')).toBe('12.00 €')
  })

  it('shows only the amount without a currency', () => {
    expect(formatCost(1.276)).toBe('1.28')
    expect(formatCost(1.276, '')).toBe('1.28')
    expect(formatCost(1.276, '  ')).toBe('1.28')
  })

  it('does not round a small cost down to nothing', () => {
    expect(formatCost(0.0012, 'DKK')).toBe('<0.01 DKK')
    expect(formatCost(0.005, 'DKK')).toBe('0.01 DKK')
    expect(formatCost(0, 'DKK')).toBe('0.00 DKK')
  })

  it('shows a placeholder for a missing value', () => {
    expect(formatCost(undefined, 'DKK')).toBe('–')
  })
})

describe('formatSince', () => {
  it('formats the date in local time', () => {
    // Midday UTC is the same date in every time zone from UTC-11 to UTC+11.
    expect(formatSince('2026-09-14T12:02:11Z')).toBe('2026-09-14')
    expect(formatSince('2026-09-14T12:02:11.123456789Z')).toBe('2026-09-14')
  })

  it('is undefined for a missing or bad time', () => {
    expect(formatSince(undefined)).toBeUndefined()
    expect(formatSince('yesterday')).toBeUndefined()
    expect(formatSince(1757851331)).toBeUndefined()
  })
})

describe('energySection', () => {
  const snap = (collectors: Record<string, unknown>): Snapshot => ({
    timestamp: '2026-11-07T12:00:00Z',
    collectors,
    series: {},
  })

  it('returns the section or a collector error', () => {
    expect(energySection(snap({ energy: { available: false } }))).toEqual({
      available: false,
    })
    expect(energySection(snap({ energy: { error: 'boom' } }))).toEqual({
      error: 'boom',
    })
  })

  it('is undefined without a section', () => {
    expect(energySection(null)).toBeUndefined()
    expect(energySection(snap({}))).toBeUndefined()
    expect(energySection(snap({ energy: 'nope' }))).toBeUndefined()
  })
})
