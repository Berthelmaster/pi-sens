import { render, screen } from '@testing-library/svelte'
import { tick } from 'svelte'
import type { Writable } from 'svelte/store'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { snapshot as mockedSnapshot } from '../lib/live'
import type { EnergySection } from '../types/energy'
import type { Snapshot } from '../types/snapshot'
import EnergyWidget from './EnergyWidget.svelte'

// uPlot reads matchMedia as soon as it loads, which jsdom does not have.
vi.hoisted(() => {
  window.matchMedia = () =>
    ({
      matches: false,
      addEventListener() {},
      removeEventListener() {},
    }) as unknown as MediaQueryList
})

// An empty history keeps the sparkline out, so uPlot never needs a canvas.
vi.mock('../lib/live', async () => {
  const { readable, writable } = await import('svelte/store')
  return {
    snapshot: writable(null),
    seriesHistory: () => readable([[], []]),
  }
})

const live = mockedSnapshot as Writable<Snapshot | null>

function snap(energy: unknown): Snapshot {
  return {
    timestamp: '2026-11-07T12:00:00Z',
    collectors: { energy },
    series: {},
  }
}

const base: EnergySection = {
  available: true,
  power_w: 3.214,
  today_kwh: 0.0412,
  yesterday_kwh: 0.0771,
  month_kwh: 1.5208,
  total_kwh: 3.1904,
  since: '2026-09-14T12:02:11Z',
}

/** The values of the row whose label starts with the given text. */
function row(label: string): string[] {
  const dt = screen.getByText(label, { selector: 'dt', exact: false })
  return [...dt.parentElement!.querySelectorAll('dd')].map(
    (dd) => dd.textContent ?? '',
  )
}

describe('EnergyWidget', () => {
  beforeEach(() => live.set(null))

  it('waits for the first snapshot', () => {
    render(EnergyWidget)
    expect(screen.getByText('Waiting for data…')).toBeTruthy()
  })

  it('shows power, energy per period and the costs', async () => {
    render(EnergyWidget)
    live.set(
      snap({ ...base, today_cost: 0.1009, month_cost: 3.726, currency: 'DKK' }),
    )
    await tick()

    expect(screen.getByText('3.21 W')).toBeTruthy()
    expect(row('Today')).toEqual(['41.2 Wh', '0.10 DKK'])
    expect(row('Yesterday')).toEqual(['77.1 Wh'])
    expect(row('This month')).toEqual(['1.52 kWh', '3.73 DKK'])
    expect(row('Total')).toEqual(['3.19 kWh'])
    expect(screen.getByText('2026-09-14').getAttribute('datetime')).toBe(
      base.since,
    )
  })

  it('leaves the costs out without a price', async () => {
    render(EnergyWidget)
    live.set(snap(base))
    await tick()

    expect(row('Today')).toEqual(['41.2 Wh'])
    expect(row('This month')).toEqual(['1.52 kWh'])
    expect(screen.queryByText(/DKK/)).toBeNull()
  })

  it('explains when the Pi cannot measure energy', async () => {
    render(EnergyWidget)
    live.set(snap({ available: false }))
    await tick()
    expect(screen.getByText('Energy data needs a Raspberry Pi 5')).toBeTruthy()

    live.set(snap({ error: 'boom' }))
    await tick()
    expect(screen.getByText('Energy data needs a Raspberry Pi 5')).toBeTruthy()
  })

  it('renders the snapshot it is given instead of the live one', () => {
    render(EnergyWidget, { snap: snap(base), history: [[], []] })
    expect(row('Today')).toEqual(['41.2 Wh'])
  })
})
