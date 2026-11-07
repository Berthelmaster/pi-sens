import { describe, expect, it } from 'vitest'
import {
  describeCondition,
  describeRule,
  firingSince,
  formatClock,
  hasCritical,
  resolvedSummary,
  worstSeverity,
  type Alert,
} from './alerts'

function alert(over: Partial<Alert> = {}): Alert {
  return {
    rule: 'cpu-temp-critical',
    series: 'temp.cpu_c',
    severity: 'critical',
    op: '>',
    threshold: 82,
    for_s: 30,
    state: 'firing',
    value: 83.4,
    pending_at: '2026-11-07T09:20:12Z',
    firing_at: '2026-11-07T09:20:42Z',
    ...over,
  }
}

const local = (y: number, mo: number, d: number, h: number, mi: number) =>
  new Date(y, mo - 1, d, h, mi).getTime()

describe('severity helpers', () => {
  it('finds the worst severity', () => {
    const warn = alert({ severity: 'warning' })
    expect(worstSeverity([])).toBeNull()
    expect(worstSeverity([warn])).toBe('warning')
    expect(worstSeverity([warn, alert()])).toBe('critical')
    expect(hasCritical([warn])).toBe(false)
    expect(hasCritical([warn, alert()])).toBe(true)
  })
})

describe('describeCondition', () => {
  it('shows the series, value and threshold', () => {
    expect(describeCondition(alert())).toBe('temp.cpu_c 83.4 > 82')
    expect(
      describeCondition(
        alert({ series: 'pi.core_v', op: '<', value: 0.78123, threshold: 0.8 }),
      ),
    ).toBe('pi.core_v 0.78 < 0.8')
  })

  it('takes a newer value', () => {
    expect(describeCondition(alert(), 85.06)).toBe('temp.cpu_c 85.06 > 82')
  })

  it('leaves the value out for the rule alone', () => {
    expect(describeRule(alert({ value: 80.1 }))).toBe('temp.cpu_c > 82')
  })
})

describe('formatClock', () => {
  it('shows only the time on the same day', () => {
    const now = local(2026, 11, 7, 12, 0)
    expect(formatClock(local(2026, 11, 7, 9, 5), now)).toBe('09:05:00')
  })

  it('adds the date on another day', () => {
    const now = local(2026, 11, 7, 0, 10)
    expect(formatClock(local(2026, 11, 6, 23, 58), now)).toBe(
      '2026-11-06 23:58:00',
    )
  })
})

describe('firingSince', () => {
  it('shows the start and how long ago', () => {
    const start = local(2026, 11, 7, 9, 20)
    const a = alert({ firing_at: new Date(start).toISOString() })
    expect(firingSince(a, start + 303_000)).toBe('since 09:20:00 (5m 3s)')
  })

  it('treats a start in the future as just now', () => {
    const start = local(2026, 11, 7, 9, 20)
    const a = alert({ firing_at: new Date(start).toISOString() })
    expect(firingSince(a, start - 2000)).toBe('since 09:20:00 (0s)')
  })

  it('is empty without a valid start', () => {
    expect(firingSince(alert({ firing_at: undefined }), 0)).toBe('')
    expect(firingSince(alert({ firing_at: 'soon' }), 0)).toBe('')
  })

  it('handles nanosecond timestamps', () => {
    const a = alert({ firing_at: '2026-11-07T09:20:42.123456789Z' })
    const t = Date.parse('2026-11-07T09:20:42.123Z')
    expect(firingSince(a, t + 60_000)).toMatch(/\(1m\)$/)
  })
})

describe('resolvedSummary', () => {
  it('shows when it ended and how long it fired', () => {
    const start = local(2026, 11, 7, 9, 20)
    const end = start + 620_000
    const a = alert({
      state: 'resolved',
      firing_at: new Date(start).toISOString(),
      resolved_at: new Date(end).toISOString(),
    })
    expect(resolvedSummary(a, end)).toBe('resolved 09:30:20 after 10m 20s')
  })

  it('is empty while not resolved', () => {
    expect(resolvedSummary(alert(), 0)).toBe('')
  })
})
