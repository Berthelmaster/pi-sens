import { describe, expect, it } from 'vitest'
import {
  formatVolts,
  formatWatts,
  piHealthStatus,
  powerSection,
  supplyLow,
  throttleBadges,
  throttleSection,
  type ThrottleFlags,
} from './pihealth'
import type { Snapshot } from './snapshot'

const clear: ThrottleFlags = {
  under_voltage: false,
  arm_freq_capped: false,
  throttled: false,
  soft_temp_limit: false,
  under_voltage_occurred: false,
  arm_freq_capped_occurred: false,
  throttled_occurred: false,
  soft_temp_limit_occurred: false,
}

describe('throttleBadges', () => {
  it('is empty when no flag is set', () => {
    expect(throttleBadges(clear)).toEqual([])
    expect(throttleBadges(undefined)).toEqual([])
  })

  it('marks current flags active and past ones occurred', () => {
    // 0x50005: under-voltage and throttled now, and both since boot.
    const badges = throttleBadges({
      ...clear,
      under_voltage: true,
      throttled: true,
      under_voltage_occurred: true,
      throttled_occurred: true,
      soft_temp_limit_occurred: true,
    })
    expect(badges.map((b) => [b.key, b.state])).toEqual([
      ['under_voltage', 'active'],
      ['throttled', 'active'],
      ['soft_temp_limit', 'occurred'],
    ])
  })
})

describe('piHealthStatus', () => {
  it('is critical while any flag is set now', () => {
    expect(piHealthStatus({ ...clear, arm_freq_capped: true }, 5.1)).toBe(
      'critical',
    )
    expect(piHealthStatus({ ...clear, soft_temp_limit: true }, 4.5)).toBe(
      'critical',
    )
  })

  it('warns about past flags and a low supply', () => {
    expect(piHealthStatus({ ...clear, throttled_occurred: true }, 5.1)).toBe(
      'warning',
    )
    expect(piHealthStatus(clear, 4.79)).toBe('warning')
  })

  it('is ok when clear, with or without a supply reading', () => {
    expect(piHealthStatus(clear, 5.1)).toBe('ok')
    expect(piHealthStatus(clear, 4.8)).toBe('ok')
    expect(piHealthStatus(clear, undefined)).toBe('ok')
  })

  it('is unknown without flags', () => {
    expect(piHealthStatus(undefined, undefined)).toBe('unknown')
  })
})

describe('supplyLow', () => {
  it('warns below 4.8 V', () => {
    expect(supplyLow(4.79)).toBe(true)
    expect(supplyLow(4.8)).toBe(false)
    expect(supplyLow(undefined)).toBe(false)
    expect(supplyLow(NaN)).toBe(false)
  })
})

describe('sections', () => {
  it('picks the throttle and power sections', () => {
    const snap: Snapshot = {
      timestamp: '2026-11-07T10:00:00Z',
      collectors: {
        throttle: { available: false },
        power: { error: 'boom' },
      },
      series: {},
    }
    expect(throttleSection(snap)).toEqual({ available: false })
    expect(powerSection(snap)).toEqual({ error: 'boom' })
    expect(throttleSection(null)).toBeUndefined()
    expect(
      powerSection({ ...snap, collectors: { power: null } }),
    ).toBeUndefined()
  })
})

describe('formatters', () => {
  it('formats volts with millivolts below 1 V', () => {
    expect(formatVolts(5.0812)).toBe('5.08 V')
    expect(formatVolts(0.72)).toBe('0.720 V')
    expect(formatVolts(undefined)).toBe('–')
  })

  it('formats watts', () => {
    expect(formatWatts(3.14159)).toBe('3.14 W')
    expect(formatWatts(NaN)).toBe('–')
  })
})
