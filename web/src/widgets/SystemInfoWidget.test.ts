import { describe, expect, it } from 'vitest'
import {
  boardDetails,
  formatDateTime,
  formatUptime,
} from './SystemInfoWidget.svelte'

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

describe('boardDetails', () => {
  it('joins the decoded RAM, SoC and manufacturer', () => {
    expect(
      boardDetails({
        fitted_ram_bytes: 8 * 2 ** 30,
        processor: 'BCM2712',
        manufacturer: 'Sony UK',
      }),
    ).toBe('8 GiB · BCM2712 · Sony UK')
    expect(boardDetails({ fitted_ram_bytes: 512 * 2 ** 20 })).toBe('512 MiB')
  })

  it('is undefined when nothing was decoded', () => {
    expect(
      boardDetails({ model: 'Raspberry Pi Model B Rev 2' }),
    ).toBeUndefined()
    expect(boardDetails({ processor: ' ' })).toBeUndefined()
  })
})
