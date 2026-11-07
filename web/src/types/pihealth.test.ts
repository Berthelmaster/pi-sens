import { describe, expect, it } from 'vitest'
import {
  formatVolts,
  formatWatts,
  piHealthStatus,
  powerSection,
  psuSummary,
  rtcBattery,
  rtcBatteryLow,
  rtcSection,
  supplyLow,
  throttleBadges,
  throttleSection,
  undervoltageSummary,
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

  it('warns about a low RTC battery', () => {
    expect(piHealthStatus(clear, 5.1, 2.69)).toBe('warning')
    expect(piHealthStatus(clear, 5.1, 2.7)).toBe('ok')
    expect(piHealthStatus({ ...clear, throttled: true }, 5.1, 2.5)).toBe(
      'critical',
    )
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

describe('rtcBatteryLow', () => {
  it('warns below 2.7 V', () => {
    expect(rtcBatteryLow(2.69)).toBe(true)
    expect(rtcBatteryLow(2.7)).toBe(false)
    expect(rtcBatteryLow(undefined)).toBe(false)
    expect(rtcBatteryLow(NaN)).toBe(false)
  })
})

describe('rtcBattery', () => {
  it('reads a battery that is trickle charging', () => {
    expect(
      rtcBattery({
        present: true,
        rtc: 'rtc0',
        battery: true,
        battery_v: 3.021,
        charging: true,
        charging_voltage_v: 3,
      }),
    ).toEqual({ volts: 3.021, low: false, charging: true, chargingVolts: 3 })
  })

  it('flags a low battery that is not charging', () => {
    expect(
      rtcBattery({ present: true, battery: true, battery_v: 2.6 }),
    ).toEqual({ volts: 2.6, low: true, charging: false })
  })

  it('is undefined without an RTC, a battery or a reading', () => {
    expect(rtcBattery(undefined)).toBeUndefined()
    expect(rtcBattery({ error: 'boom' })).toBeUndefined()
    expect(rtcBattery({ present: false, battery: false })).toBeUndefined()
    expect(
      rtcBattery({ present: true, rtc: 'rtc0', battery: false }),
    ).toBeUndefined()
    expect(rtcBattery({ present: true, battery: true })).toBeUndefined()
    expect(
      rtcBattery({ present: true, battery: true, battery_v: 0 }),
    ).toBeUndefined()
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
    expect(rtcSection(snap)).toBeUndefined()
    expect(
      rtcSection({
        ...snap,
        collectors: { rtc: { present: false, battery: false } },
      }),
    ).toEqual({ present: false, battery: false })
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

describe('psuSummary', () => {
  it('marks a 5 A supply as fine', () => {
    expect(psuSummary({ psu_max_current_ma: 5000, psu_ok: true })).toEqual({
      rating: '5 A',
      ok: true,
    })
  })

  it('says a weaker supply limits USB current', () => {
    expect(
      psuSummary({
        psu_max_current_ma: 3000,
        psu_ok: false,
        usb_max_current_enable: false,
      }),
    ).toEqual({ rating: '3 A', ok: false, note: 'USB current limited' })
    expect(psuSummary({ psu_max_current_ma: 2500 })).toEqual({
      rating: '2.5 A',
      ok: false,
      note: 'USB current limited',
    })
  })

  it('notes when config.txt lifts the USB limit', () => {
    expect(
      psuSummary({ psu_max_current_ma: 3000, usb_max_current_enable: true }),
    ).toEqual({
      rating: '3 A',
      ok: false,
      note: 'USB limit lifted in config.txt',
    })
  })

  it('is undefined without a rating', () => {
    expect(psuSummary({ available: true })).toBeUndefined()
    expect(psuSummary(undefined)).toBeUndefined()
  })
})

describe('undervoltageSummary', () => {
  // Local times, so the expectations hold in any time zone.
  const now = new Date(2026, 10, 7, 15, 30)

  it('counts events and shows when the last one began', () => {
    const last = new Date(2026, 10, 7, 14, 2).toISOString()
    expect(
      undervoltageSummary(
        { undervoltage_events: 3, last_undervoltage: last },
        now,
      ),
    ).toBe('3 times, last 14:02')
    expect(
      undervoltageSummary(
        { undervoltage_events: 1, last_undervoltage: last },
        now,
      ),
    ).toBe('once, last 14:02')
  })

  it('adds the date for an earlier day', () => {
    const last = new Date(2026, 10, 5, 9, 7).toISOString()
    expect(
      undervoltageSummary(
        { undervoltage_events: 2, last_undervoltage: last },
        now,
      ),
    ).toBe('2 times, last 5 Nov 09:07')
  })

  it('handles no events and missing data', () => {
    expect(undervoltageSummary({ undervoltage_events: 0 }, now)).toBe(
      'none since pisens started',
    )
    expect(
      undervoltageSummary(
        { undervoltage_events: 2, last_undervoltage: 'nonsense' },
        now,
      ),
    ).toBe('2 times')
    expect(undervoltageSummary({ available: true }, now)).toBeUndefined()
  })
})
