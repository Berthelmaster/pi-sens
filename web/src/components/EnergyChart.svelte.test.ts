import { fireEvent, render, screen } from '@testing-library/svelte'
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'
import type { EnergyDailyResponse } from '../api'
import EnergyChart from './EnergyChart.svelte'

// bind:clientWidth needs a ResizeObserver, which jsdom lacks, and jsdom
// lays nothing out, so every element is given a width to draw into.
vi.stubGlobal(
  'ResizeObserver',
  class {
    observe() {}
    unobserve() {}
    disconnect() {}
  },
)
const WIDTH = 344 // 44 px of axis labels and 300 px of bars
let clientWidth: PropertyDescriptor | undefined
beforeAll(() => {
  clientWidth = Object.getOwnPropertyDescriptor(
    HTMLElement.prototype,
    'clientWidth',
  )
  Object.defineProperty(HTMLElement.prototype, 'clientWidth', {
    configurable: true,
    get: () => WIDTH,
  })
})
afterAll(() => {
  if (clientWidth)
    Object.defineProperty(HTMLElement.prototype, 'clientWidth', clientWidth)
})

const data: EnergyDailyResponse = {
  price: 2.5,
  currency: 'DKK',
  days: [
    { date: '2026-11-04', start: 0, kwh: 0.12, cost: 0.3 },
    { date: '2026-11-05', start: 0, kwh: null },
    { date: '2026-11-06', start: 0, kwh: 0.24, cost: 0.6 },
    { date: '2026-11-07', start: 0, kwh: 0.06, cost: 0.15 },
  ],
}
const label = 'Energy per day, last 4 days'

describe('EnergyChart', () => {
  it('draws a bar for every day with data', () => {
    const { container } = render(EnergyChart, { data, loading: false, label })
    expect(screen.getByRole('img', { name: label })).toBeTruthy()
    const bars = container.querySelectorAll('path.bar')
    expect(bars).toHaveLength(3)
    // Today is still counting.
    expect(bars[2].classList.contains('partial')).toBe(true)
    expect(container.querySelector('.total')?.textContent).toContain(
      '4 days: 0.420 kWh, 1.05 DKK',
    )
  })

  it('shows the kWh and cost of the hovered day', async () => {
    const { container } = render(EnergyChart, { data, loading: false, label })
    const plot = container.querySelector('.plot') as HTMLElement
    // 75 px per day after the 44 px axis: the third day.
    await fireEvent.pointerMove(plot, { clientX: 44 + 2 * 75 + 10 })
    const tip = container.querySelector('.tooltip')
    expect(tip?.textContent).toContain('0.240 kWh')
    expect(tip?.textContent).toContain('0.60 DKK')

    await fireEvent.pointerMove(plot, { clientX: 44 + 75 + 10 })
    expect(container.querySelector('.tooltip')?.textContent).toContain(
      'No data',
    )

    await fireEvent.pointerLeave(plot)
    expect(container.querySelector('.tooltip')).toBeNull()
  })

  it('leaves the cost out without a price', async () => {
    const days = data.days.map((d) => ({ ...d, cost: undefined }))
    const { container } = render(EnergyChart, {
      data: { days },
      loading: false,
      label,
    })
    const plot = container.querySelector('.plot') as HTMLElement
    await fireEvent.pointerMove(plot, { clientX: 44 + 10 })
    const tip = container.querySelector('.tooltip')
    expect(tip?.textContent).toContain('0.120 kWh')
    expect(tip?.textContent).not.toContain('Cost')
  })

  it('lists the days in a table', async () => {
    render(EnergyChart, { data, loading: false, label })
    await fireEvent.click(screen.getByLabelText('Show as table'))
    const table = screen.getByRole('table', { name: label })
    const rows = table.querySelectorAll('tbody tr')
    expect(rows).toHaveLength(4)
    expect(rows[1].textContent).toContain('No data')
    expect(rows[3].textContent).toContain('so far')
    expect(rows[3].textContent).toContain('0.15 DKK')
  })

  it('shows an empty state when no day has data', () => {
    const empty = { days: data.days.map((d) => ({ ...d, kwh: null })) }
    const { container } = render(EnergyChart, {
      data: empty,
      loading: false,
      label,
    })
    expect(container.querySelector('svg')).toBeNull()
    expect(screen.getByText(/No energy use recorded yet/)).toBeTruthy()
  })

  it('shows loading and error states', () => {
    render(EnergyChart, { data: null, loading: true, label })
    expect(screen.getByText(/Loading/)).toBeTruthy()
    render(EnergyChart, { data: null, loading: false, error: 'boom', label })
    expect(screen.getByText(/Couldn't load the energy per day/)).toBeTruthy()
  })
})
