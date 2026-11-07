import { fireEvent, render, screen } from '@testing-library/svelte'
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

  it('has no table toggle unless asked for', () => {
    render(TimeSeriesChart, { data, series })
    expect(screen.queryByLabelText('Show as table')).toBeNull()
  })

  it('shows the visible series as a table', async () => {
    const { container } = render(TimeSeriesChart, {
      data: [
        [1000, 1060, 1120],
        [10, 20, 30],
        [1, null, 3],
      ],
      series: [{ label: 'CPU' }, { label: 'GPU' }],
      format: (v: number) => `${v} %`,
      label: 'Usage, last hour',
      table: true,
    })
    expect(container.querySelector('table')).toBeNull()

    await fireEvent.click(screen.getByLabelText('Show as table'))
    const table = screen.getByRole('table', { name: /Usage, last hour/ })
    expect(container.querySelector('.plot')?.hasAttribute('hidden')).toBe(true)
    expect(
      [...table.querySelectorAll('thead th')].map((th) => th.textContent),
    ).toEqual(['Time', 'CPU', 'GPU'])
    const rows = [...table.querySelectorAll('tbody tr')].map((tr) =>
      [...tr.querySelectorAll('td')].map((td) => td.textContent),
    )
    expect(rows).toEqual([
      ['10 %', '1 %'],
      ['20 %', '–'],
      ['30 %', '3 %'],
    ])

    // A series hidden from the legend loses its column.
    await fireEvent.click(screen.getByRole('button', { name: 'GPU' }))
    expect(
      [...table.querySelectorAll('thead th')].map((th) => th.textContent),
    ).toEqual(['Time', 'CPU'])
  })

  it('thins long data to the row limit', async () => {
    const times = Array.from({ length: 100 }, (_, i) => 1000 + i * 60)
    render(TimeSeriesChart, {
      data: [times, times.map((_, i) => i)],
      series,
      label: 'CPU',
      table: true,
      tableRows: 5,
    })
    await fireEvent.click(screen.getByLabelText('Show as table'))
    const table = screen.getByRole('table')
    expect(table.querySelector('caption')?.textContent).toMatch(
      /5 of 100 samples/,
    )
    expect(
      [...table.querySelectorAll('tbody td')].map((td) => td.textContent),
    ).toEqual(['0', '25', '50', '74', '99'])
  })
})
