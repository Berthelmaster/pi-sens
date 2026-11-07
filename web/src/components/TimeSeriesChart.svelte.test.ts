import { render } from '@testing-library/svelte'
import { tick } from 'svelte'
import type uPlot from 'uplot'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import TimeSeriesChart from './TimeSeriesChart.svelte'

// jsdom has no canvas, so uPlot is replaced by a stand-in that keeps the
// options each chart was built with.
const { built } = vi.hoisted(() => ({ built: [] as uPlot.Options[] }))

vi.mock('uplot', () => ({
  default: class {
    series: uPlot.Series[]
    constructor(opts: uPlot.Options) {
      built.push(opts)
      this.series = opts.series
    }
    setData() {}
    setSize() {}
    setSeries() {}
    redraw() {}
    destroy() {}
  },
}))

// The real theme reads window.matchMedia, which jsdom lacks.
vi.mock('../lib/theme.svelte', () => ({ theme: { current: 'dark' } }))

// bind:clientWidth needs a ResizeObserver, which jsdom lacks too.
vi.stubGlobal(
  'ResizeObserver',
  class {
    observe() {}
    unobserve() {}
    disconnect() {}
  },
)

const data: uPlot.AlignedData = [
  [1000, 1060, 1120],
  [10, 20, 30],
]
const series = [{ label: 'CPU' }]

/** Calls the chart's setCursor hook as uPlot would on a cursor move. */
async function moveCursor(opts: uPlot.Options, left: number) {
  const u = {
    cursor: { idx: 1, left, top: 50 },
    over: { clientWidth: 400, offsetLeft: 40, offsetTop: 0 },
  } as unknown as uPlot
  for (const hook of opts.hooks?.setCursor ?? []) hook?.(u)
  await tick()
}

describe('TimeSeriesChart', () => {
  beforeEach(() => {
    built.length = 0
  })

  it('has its own cursor without a syncKey', () => {
    render(TimeSeriesChart, { data, series })
    expect(built).toHaveLength(1)
    expect(built[0].cursor?.sync).toBeUndefined()
  })

  it('links the cursor by time with a syncKey', () => {
    render(TimeSeriesChart, { data, series, syncKey: 'history' })
    render(TimeSeriesChart, { data, series, syncKey: 'history' })
    expect(built).toHaveLength(2)
    for (const opts of built) {
      expect(opts.cursor?.sync).toEqual({
        key: 'history',
        scales: ['x', null],
      })
    }
  })

  it('shows the tooltip only while the cursor is inside the plot', async () => {
    const { container } = render(TimeSeriesChart, {
      data,
      series,
      syncKey: 'history',
    })
    const tooltip = () => container.querySelector('.tooltip')

    await moveCursor(built[0], 200)
    expect(tooltip()).not.toBeNull()

    // A linked chart with a later range puts the cursor past this one's end.
    await moveCursor(built[0], 450)
    expect(tooltip()).toBeNull()

    await moveCursor(built[0], -10)
    expect(tooltip()).toBeNull()
  })
})
