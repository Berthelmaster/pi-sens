import { describe, expect, it } from 'vitest'
import type { HistoryResponse } from '../api'
import {
  autoRefreshes,
  chartData,
  commonUnit,
  CUSTOM_MAX_SERIES,
  defaultGroups,
  describeStep,
  fromLocalInput,
  historyQuery,
  parseHistoryUrl,
  resolveRange,
  toLocalInput,
  unitOfKey,
} from './history'

describe('parseHistoryUrl', () => {
  it('defaults to the last 24 hours and no custom series', () => {
    expect(parseHistoryUrl('')).toEqual({
      range: { kind: 'preset', id: '24h' },
      custom: [],
    })
  })

  it('reads a preset', () => {
    expect(parseHistoryUrl('?range=7d').range).toEqual({
      kind: 'preset',
      id: '7d',
    })
  })

  it('ignores an unknown preset', () => {
    expect(parseHistoryUrl('?range=2y').range).toEqual({
      kind: 'preset',
      id: '24h',
    })
  })

  it('reads a custom range, which wins over a preset', () => {
    expect(parseHistoryUrl('?range=1h&from=100&to=200').range).toEqual({
      kind: 'custom',
      from: 100,
      to: 200,
    })
  })

  it('ignores a backwards or broken custom range', () => {
    expect(parseHistoryUrl('?from=200&to=100').range.kind).toBe('preset')
    expect(parseHistoryUrl('?from=-5&to=100').range.kind).toBe('preset')
    expect(parseHistoryUrl('?from=abc&to=100').range.kind).toBe('preset')
  })

  it('reads the custom series without blanks or repeats, up to the limit', () => {
    expect(parseHistoryUrl('?series=a, b,,a').custom).toEqual(['a', 'b'])
    const many = Array.from({ length: 20 }, (_, i) => `s${i}`).join(',')
    expect(parseHistoryUrl(`?series=${many}`).custom).toHaveLength(
      CUSTOM_MAX_SERIES,
    )
  })

  it('round-trips through historyQuery', () => {
    for (const qs of [
      '?range=6h',
      '?from=1762473600&to=1762560000',
      '?range=90d&series=cpu.usage_pct,temp.cpu_c',
    ]) {
      expect(historyQuery(parseHistoryUrl(qs))).toBe(qs)
    }
  })
})

describe('resolveRange', () => {
  it('ends a preset now', () => {
    expect(resolveRange({ kind: 'preset', id: '1h' }, 10_000.7)).toEqual({
      from: 6400,
      to: 10_000,
    })
  })

  it('keeps a custom range as it is', () => {
    expect(resolveRange({ kind: 'custom', from: 1, to: 2 }, 10_000)).toEqual({
      from: 1,
      to: 2,
    })
  })
})

describe('autoRefreshes', () => {
  it('refreshes presets up to 24 hours', () => {
    expect(autoRefreshes({ kind: 'preset', id: '1h' })).toBe(true)
    expect(autoRefreshes({ kind: 'preset', id: '24h' })).toBe(true)
    expect(autoRefreshes({ kind: 'preset', id: '7d' })).toBe(false)
    expect(autoRefreshes({ kind: 'custom', from: 0, to: 60 })).toBe(false)
  })
})

describe('local input times', () => {
  it('round-trips to the minute', () => {
    const unix = 1762473660
    expect(fromLocalInput(toLocalInput(unix))).toBe(unix)
  })

  it('rejects an empty value', () => {
    expect(fromLocalInput('')).toBeUndefined()
  })
})

describe('units', () => {
  it('reads the unit from the key', () => {
    expect(unitOfKey('cpu.usage_pct')).toBe('percent')
    expect(unitOfKey('temp.cpu_c')).toBe('celsius')
    expect(unitOfKey('net.eth0.rx_bps')).toBe('bytesPerSecond')
    expect(unitOfKey('mem.used_bytes')).toBe('bytes')
    expect(unitOfKey('cpu.core0.freq_mhz')).toBe('megahertz')
    expect(unitOfKey('pi.core_v')).toBe('volts')
    expect(unitOfKey('pi.power_w')).toBe('watts')
    expect(unitOfKey('fan.rpm')).toBe('rpm')
    expect(unitOfKey('system.uptime_s')).toBe('seconds')
    expect(unitOfKey('system.procs')).toBe('number')
  })

  it('falls back to a plain number for mixed units', () => {
    expect(commonUnit(['cpu.usage_pct', 'mem.used_pct'])).toBe('percent')
    expect(commonUnit(['cpu.usage_pct', 'temp.cpu_c'])).toBe('number')
  })
})

describe('defaultGroups', () => {
  it('leaves out groups and charts without series', () => {
    const groups = defaultGroups(['cpu.usage_pct', 'temp.cpu_c'])
    expect(groups.map((g) => g.id)).toEqual(['cpu', 'temperature'])
    expect(groups[0].panels[0].series.map((s) => s.key)).toEqual([
      'cpu.usage_pct',
    ])
    expect(groups[1].panels.map((p) => p.title)).toEqual(['Temperature'])
  })

  it('prefers fan rpm over duty cycle', () => {
    const [temp] = defaultGroups(['temp.cpu_c', 'fan.rpm', 'fan.pwm_pct'])
    expect(temp.panels[1].series.map((s) => s.key)).toEqual(['fan.rpm'])
    const [duty] = defaultGroups(['fan.pwm_pct'])
    expect(duty.panels[0].series.map((s) => s.key)).toEqual(['fan.pwm_pct'])
  })

  it('names disks only when there is more than one', () => {
    const [one] = defaultGroups([
      'disk.mmcblk0.read_bps',
      'disk.mmcblk0.write_bps',
    ])
    expect(one.panels[0].series.map((s) => s.label)).toEqual(['Read', 'Write'])
    const [two] = defaultGroups([
      'disk.sda.read_bps',
      'disk.sda.write_bps',
      'disk.mmcblk0.read_bps',
      'disk.mmcblk0.write_bps',
    ])
    expect(two.panels[0].series.map((s) => s.label)).toEqual([
      'mmcblk0 read',
      'mmcblk0 write',
      'sda read',
      'sda write',
    ])
  })

  it('shows network traffic in bits', () => {
    const [net] = defaultGroups(['net.total.rx_bps', 'net.total.tx_bps'])
    expect(net.panels[0].unit).toBe('bitsPerSecond')
    expect(net.panels[0].series.every((s) => s.scale === 8)).toBe(true)
  })
})

describe('chartData', () => {
  const res = (
    series: HistoryResponse['series'],
    resolution: HistoryResponse['resolution'] = '1m',
    step = 60,
  ): HistoryResponse => ({ resolution, step, series })

  it('lines series up on shared timestamps and scales them', () => {
    const out = chartData(
      res({
        a: { t: [0, 60, 120], avg: [1, 2, 3], min: [0, 1, 2], max: [2, 3, 4] },
        b: { t: [60, 120], avg: [5, 6], min: [5, 6], max: [5, 6] },
      }),
      [
        { key: 'a', label: 'A', scale: 8 },
        { key: 'b', label: 'B' },
      ],
    )
    expect(out.hasData).toBe(true)
    expect(out.data).toEqual([
      [0, 60, 120],
      [8, 16, 24],
      [undefined, 5, 6],
    ])
    expect(out.bands?.[0]).toEqual({ min: [0, 8, 16], max: [16, 24, 32] })
  })

  it('breaks the lines over a gap in the data', () => {
    const out = chartData(
      res({
        a: { t: [0, 60, 600, 660], avg: [1, 2, 3, 4], min: [], max: [] },
      }),
      [{ key: 'a', label: 'A' }],
    )
    expect(out.data[0]).toEqual([0, 60, 120, 600, 660])
    expect(out.data[1][2]).toBeNull()
  })

  it('has no bands for single samples', () => {
    const out = chartData(
      res(
        { a: { t: [0, 1], avg: [1, 2], min: [1, 2], max: [1, 2] } },
        'raw',
        1,
      ),
      [{ key: 'a', label: 'A' }],
    )
    expect(out.bands).toBeUndefined()
  })

  it('reports no data for empty or missing series', () => {
    const out = chartData(res({ a: { t: [], avg: [], min: [], max: [] } }), [
      { key: 'a', label: 'A' },
      { key: 'missing', label: 'M' },
    ])
    expect(out.hasData).toBe(false)
  })
})

describe('describeStep', () => {
  it('uses the largest whole unit', () => {
    expect(describeStep(1)).toBe('1 second')
    expect(describeStep(44)).toBe('44 seconds')
    expect(describeStep(360)).toBe('6 minutes')
    expect(describeStep(3600)).toBe('1 hour')
    expect(describeStep(5400)).toBe('90 minutes')
  })
})
