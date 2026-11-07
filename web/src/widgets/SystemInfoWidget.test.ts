import { describe, expect, it } from 'vitest'
import { formatDateTime, formatUptime } from './SystemInfoWidget.svelte'

describe('formatUptime', () => {
  it('shows every unit down to seconds', () => {
    expect(formatUptime(334893.67)).toBe('3d 21h 01m 33s')
    expect(formatUptime(3600)).toBe('1h 00m 00s')
    expect(formatUptime(65)).toBe('1m 05s')
    expect(formatUptime(0.4)).toBe('0s')
  })

  it('shows a placeholder for missing or bad values', () => {
    expect(formatUptime(undefined)).toBe('–')
    expect(formatUptime(NaN)).toBe('–')
    expect(formatUptime(-5)).toBe('–')
  })
})

describe('formatDateTime', () => {
  it('formats local time to the minute', () => {
    const ms = new Date(2026, 10, 3, 9, 5, 30).getTime()
    expect(formatDateTime(ms)).toBe('2026-11-03 09:05')
  })

  it('shows a placeholder for an invalid time', () => {
    expect(formatDateTime(NaN)).toBe('–')
  })
})
