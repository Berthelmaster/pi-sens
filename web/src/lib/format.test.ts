import { describe, expect, it } from 'vitest'
import {
  PLACEHOLDER,
  formatBitsPerSecond,
  formatBytes,
  formatDuration,
  formatHz,
  formatPercent,
  formatTemp,
} from './format'

const formatters = [
  formatBytes,
  formatBitsPerSecond,
  formatPercent,
  formatDuration,
  formatTemp,
  formatHz,
]

describe('all formatters', () => {
  it.each([null, undefined, NaN, Infinity, -Infinity])(
    'show the placeholder for %s',
    (v) => {
      for (const f of formatters) expect(f(v)).toBe(PLACEHOLDER)
    },
  )
})

describe('formatBytes', () => {
  it.each([
    [0, '0 B'],
    [512, '512 B'],
    [1023, '1023 B'],
    [1024, '1.0 KiB'],
    [1536, '1.5 KiB'],
    [5 * 1024 ** 2, '5.0 MiB'],
    [3.7 * 1024 ** 3, '3.7 GiB'],
    [2 * 1024 ** 4, '2.0 TiB'],
    [1024 ** 6, '1024.0 PiB'],
    [-2048, '-2.0 KiB'],
  ])('%d -> %s', (bytes, want) => {
    expect(formatBytes(bytes)).toBe(want)
  })

  it('rolls over to the next unit when rounding reaches it', () => {
    expect(formatBytes(1024 * 1024 - 1)).toBe('1.0 MiB')
    expect(formatBytes(1023.6)).toBe('1.0 KiB')
  })

  it('honours decimals', () => {
    expect(formatBytes(1536, { decimals: 2 })).toBe('1.50 KiB')
    expect(formatBytes(1536, { decimals: 0 })).toBe('2 KiB')
  })

  it('uses decimal units on request', () => {
    expect(formatBytes(999, { decimal: true })).toBe('999 B')
    expect(formatBytes(1500, { decimal: true })).toBe('1.5 kB')
    expect(formatBytes(64e9, { decimal: true })).toBe('64.0 GB')
  })
})

describe('formatBitsPerSecond', () => {
  it.each([
    [0, '0 b/s'],
    [999, '999 b/s'],
    [1000, '1.0 kb/s'],
    [12_500_000, '12.5 Mb/s'],
    [1e9, '1.0 Gb/s'],
  ])('%d -> %s', (bps, want) => {
    expect(formatBitsPerSecond(bps)).toBe(want)
  })

  it('honours decimals', () => {
    expect(formatBitsPerSecond(12_345_678, 2)).toBe('12.35 Mb/s')
  })
})

describe('formatPercent', () => {
  it.each([
    [0, '0.0%'],
    [42.345, '42.3%'],
    [100, '100.0%'],
  ])('%d -> %s', (pct, want) => {
    expect(formatPercent(pct)).toBe(want)
  })

  it('honours decimals', () => {
    expect(formatPercent(42.6, 0)).toBe('43%')
  })
})

describe('formatDuration', () => {
  it.each([
    [0, '0s'],
    [0.9, '0s'],
    [5, '5s'],
    [59.99, '59s'],
    [60, '1m'],
    [125, '2m 5s'],
    [3600, '1h'],
    [3725, '1h 2m'],
    [7200 + 30, '2h'],
    [86400, '1d'],
    [93784, '1d 2h'],
    [86400 * 400 + 3600 * 5, '400d 5h'],
    [-125, '-2m 5s'],
  ])('%d -> %s', (secs, want) => {
    expect(formatDuration(secs)).toBe(want)
  })
})

describe('formatTemp', () => {
  it.each([
    [52.34, '52.3 °C'],
    [-4, '-4.0 °C'],
    [0, '0.0 °C'],
  ])('%d -> %s', (c, want) => {
    expect(formatTemp(c)).toBe(want)
  })

  it('honours decimals', () => {
    expect(formatTemp(52.36, 0)).toBe('52 °C')
  })
})

describe('formatHz', () => {
  it.each([
    [50, '50 Hz'],
    [1500, '1.5 kHz'],
    [600e6, '600 MHz'],
    [2.4e9, '2.4 GHz'],
    [2.45e9, '2.45 GHz'],
    [999_999_999, '1 GHz'],
  ])('%d -> %s', (hz, want) => {
    expect(formatHz(hz)).toBe(want)
  })

  it('honours decimals', () => {
    expect(formatHz(1.56e9, 1)).toBe('1.6 GHz')
  })
})
