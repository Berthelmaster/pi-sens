import { describe, expect, it } from 'vitest'
import { formatRatio, memoryBreakdown, zramSwapSummary } from './memory'
import type { ZramDevice } from './memory'

const GiB = 1024 ** 3

describe('memoryBreakdown', () => {
  it('splits the bar into used, buffers/cache and free', () => {
    const [used, cache, free] = memoryBreakdown({
      total_bytes: 8 * GiB,
      used_bytes: 2 * GiB,
      free_bytes: 4 * GiB,
      buffers_bytes: 0.5 * GiB,
      cached_bytes: 1.5 * GiB,
    })
    expect(used).toMatchObject({ key: 'used', bytes: 2 * GiB, pct: 25 })
    expect(cache).toMatchObject({ key: 'cache', bytes: 2 * GiB, pct: 25 })
    expect(free).toMatchObject({ key: 'free', bytes: 4 * GiB, pct: 50 })
  })

  it('shrinks the cache segment so the bar never overflows', () => {
    // Shared memory sits in the cache and counts as used too.
    const segs = memoryBreakdown({
      total_bytes: 8 * GiB,
      used_bytes: 4 * GiB,
      free_bytes: 2 * GiB,
      buffers_bytes: 0,
      cached_bytes: 3 * GiB,
    })
    expect(segs.map((s) => s.pct)).toEqual([50, 25, 25])
    expect(segs[1].bytes).toBe(3 * GiB)
  })

  it('shows an empty bar without a total', () => {
    expect(memoryBreakdown({}).map((s) => s.pct)).toEqual([0, 0, 0])
  })
})

function zram(d: Partial<ZramDevice>): ZramDevice {
  return {
    device: 'zram0',
    swap: true,
    disksize_bytes: 2 * GiB,
    orig_data_bytes: 0,
    compr_data_bytes: 0,
    mem_used_bytes: 0,
    ...d,
  }
}

describe('zramSwapSummary', () => {
  it('is undefined when swap is not on zram', () => {
    expect(zramSwapSummary(undefined)).toBeUndefined()
    expect(zramSwapSummary([])).toBeUndefined()
    expect(zramSwapSummary([zram({ swap: false })])).toBeUndefined()
  })

  it('totals the swap devices and weighs the ratio by size', () => {
    const s = zramSwapSummary([
      zram({
        algorithm: 'zstd',
        orig_data_bytes: 300,
        compr_data_bytes: 100,
        mem_used_bytes: 110,
      }),
      zram({
        device: 'zram1',
        algorithm: 'zstd',
        orig_data_bytes: 100,
        compr_data_bytes: 100,
        mem_used_bytes: 105,
      }),
      zram({ device: 'zram2', swap: false, orig_data_bytes: 999 }),
    ])
    expect(s).toEqual({
      algorithms: ['zstd'],
      origBytes: 400,
      comprBytes: 200,
      memUsedBytes: 215,
      ratio: 2,
    })
  })

  it('has no ratio while the devices are empty', () => {
    expect(zramSwapSummary([zram({})])?.ratio).toBeUndefined()
  })
})

describe('formatRatio', () => {
  it('formats with one decimal', () => {
    expect(formatRatio(2.843)).toBe('2.8×')
    expect(formatRatio(undefined)).toBe('–')
  })
})
