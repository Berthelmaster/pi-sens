import { describe, expect, it } from 'vitest'
import type { FilesystemMount } from '../types/storage'
import {
  formatIops,
  formatRate,
  moreLabel,
  storageStatus,
  sumHistories,
} from './storage'

function mount(used_pct?: number): FilesystemMount {
  return {
    name: 'root',
    device: '/dev/mmcblk0p2',
    mountpoint: '/',
    fstype: 'ext4',
    read_only: false,
    used_pct,
  }
}

describe('formatRate', () => {
  it('formats bytes per second', () => {
    expect(formatRate(0)).toBe('0 B/s')
    expect(formatRate(1536)).toBe('1.5 KiB/s')
    expect(formatRate(52_428_800)).toBe('50.0 MiB/s')
  })

  it('shows a bare placeholder for missing values', () => {
    expect(formatRate(undefined)).toBe('–')
    expect(formatRate(NaN)).toBe('–')
  })
})

describe('formatIops', () => {
  it('keeps one decimal below 10', () => {
    expect(formatIops(0)).toBe('0.0')
    expect(formatIops(2.54)).toBe('2.5')
    expect(formatIops(14.4)).toBe('14')
    expect(formatIops(undefined)).toBe('–')
  })
})

describe('storageStatus', () => {
  it('takes the fullest filesystem', () => {
    expect(storageStatus([mount(10), mount(50)])).toBe('ok')
    expect(storageStatus([mount(10), mount(80)])).toBe('warning')
    expect(storageStatus([mount(85), mount(90), mount(10)])).toBe('critical')
  })

  it('skips mounts without usage', () => {
    expect(storageStatus([mount(undefined), mount(20)])).toBe('ok')
    expect(storageStatus([mount(undefined)])).toBeUndefined()
    expect(storageStatus([])).toBeUndefined()
  })
})

describe('sumHistories', () => {
  it('adds values at matching times', () => {
    expect(
      sumHistories(
        [
          [1, 2, 3],
          [10, 20, 30],
        ],
        [
          [2, 3, 4],
          [1, 2, 3],
        ],
      ),
    ).toEqual([
      [2, 3],
      [21, 32],
    ])
  })

  it('handles empty histories', () => {
    expect(sumHistories([[], []], [[], []])).toEqual([[], []])
  })
})

describe('moreLabel', () => {
  it('counts hidden mounts and flags full ones', () => {
    expect(moreLabel([mount(10), mount(20)])).toBe('2 more')
    expect(moreLabel([mount(95), mount(82), mount(81), mount(5)])).toBe(
      '4 more (1 critical, 2 warning)',
    )
  })
})
