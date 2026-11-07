import { describe, expect, it } from 'vitest'
import {
  coreRows,
  formatLoad,
  formatMhz,
  governors,
  maxMhz,
  showIowait,
} from './cpu'

const usage = (core: number, usage_pct: number) => ({
  core,
  usage_pct,
  user_pct: 0,
  system_pct: 0,
  iowait_pct: 0,
  irq_pct: 0,
  steal_pct: 0,
  idle_pct: 0,
})

describe('coreRows', () => {
  it('joins usage and frequency by core number', () => {
    expect(
      coreRows(
        { cores: 2, per_core: [usage(1, 20), usage(0, 10)] },
        {
          cores: [
            { core: 0, cur_mhz: 1500 },
            { core: 1, cur_mhz: 2400 },
          ],
        },
      ),
    ).toEqual([
      { core: 0, usage: 10, mhz: 1500 },
      { core: 1, usage: 20, mhz: 2400 },
    ])
  })

  it('works without cpufreq', () => {
    expect(coreRows({ cores: 1, per_core: [usage(0, 5)] }, undefined)).toEqual([
      { core: 0, usage: 5 },
    ])
    expect(
      coreRows({ cores: 1, per_core: [usage(0, 5)] }, { cores: [] }),
    ).toEqual([{ core: 0, usage: 5 }])
  })

  it('falls back to the core count before usage is known', () => {
    expect(coreRows({ cores: 2 }, undefined)).toEqual([
      { core: 0 },
      { core: 1 },
    ])
  })

  it('shows frequencies when the cpu section is missing', () => {
    expect(
      coreRows(undefined, { cores: [{ core: 3, cur_mhz: 1000 }] }),
    ).toEqual([{ core: 3, mhz: 1000 }])
    expect(coreRows(undefined, undefined)).toEqual([])
  })
})

describe('maxMhz and governors', () => {
  const freq = {
    cores: [
      { core: 0, cur_mhz: 1500, max_mhz: 2400, governor: 'ondemand' },
      { core: 1, cur_mhz: 1500, max_mhz: 2000, governor: 'ondemand' },
    ],
  }

  it('takes the highest max frequency', () => {
    expect(maxMhz(freq)).toBe(2400)
    expect(maxMhz({ cores: [{ core: 0, cur_mhz: 1 }] })).toBeUndefined()
    expect(maxMhz(undefined)).toBeUndefined()
  })

  it('lists each governor once', () => {
    expect(governors(freq)).toBe('ondemand')
    expect(
      governors({
        cores: [
          ...freq.cores,
          { core: 2, cur_mhz: 1, governor: 'performance' },
        ],
      }),
    ).toBe('ondemand, performance')
    expect(governors({ cores: [] })).toBeUndefined()
  })
})

describe('showIowait', () => {
  it('shows iowait once it reaches 1% in the window', () => {
    expect(showIowait(0.2, [0, 0.1])).toBe(false)
    expect(showIowait(1.5, [])).toBe(true)
    expect(showIowait(0, [0, 4, 0])).toBe(true)
  })

  it('hides iowait without a current value', () => {
    expect(showIowait(undefined, [5])).toBe(false)
  })
})

describe('formatters', () => {
  it('formats load averages and MHz', () => {
    expect(formatLoad(0.5)).toBe('0.50')
    expect(formatLoad(undefined)).toBe('–')
    expect(formatMhz(2400)).toBe('2.4 GHz')
    expect(formatMhz(600)).toBe('600 MHz')
    expect(formatMhz(undefined)).toBe('–')
  })
})
