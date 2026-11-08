import { render, screen } from '@testing-library/svelte'
import { tick } from 'svelte'
import type { Writable } from 'svelte/store'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { snapshot as mockedSnapshot } from '../lib/live'
import type { SeriesHistory } from '../lib/live'
import type { NetQualitySection, NetQualityTarget } from '../types/netquality'
import type { Snapshot } from '../types/snapshot'
import NetQualityWidget from './NetQualityWidget.svelte'

// uPlot reads matchMedia as soon as it loads, which jsdom does not have.
vi.hoisted(() => {
  window.matchMedia = () =>
    ({
      matches: false,
      addEventListener() {},
      removeEventListener() {},
    }) as unknown as MediaQueryList
})

// Empty histories keep the sparklines out, so uPlot never needs a canvas.
vi.mock('../lib/live', async () => {
  const { readable, writable } = await import('svelte/store')
  return {
    snapshot: writable(null),
    seriesHistory: () => readable([[], []]),
  }
})

const live = mockedSnapshot as Writable<Snapshot | null>

function snap(netquality: unknown): Snapshot {
  return {
    timestamp: '2026-11-07T12:00:00Z',
    collectors: { netquality },
    series: {},
  }
}

function target(over: Partial<NetQualityTarget>): NetQualityTarget {
  return {
    name: '1.1.1.1',
    key: '1-1-1-1',
    kind: 'internet',
    method: 'icmp',
    latency_ms: 12.34,
    jitter_ms: 0.81,
    loss_pct: 0,
    last_ok: '2026-11-07T12:00:00Z',
    ...over,
  }
}

const gateway = target({
  name: 'gateway',
  key: 'gateway',
  kind: 'gateway',
  address: '192.168.1.1',
  latency_ms: 0.612,
  jitter_ms: 0.104,
})

function section(over: Partial<NetQualitySection>): NetQualitySection {
  return {
    available: true,
    status: 'good',
    interval_s: 10,
    probed_at: '2026-11-07T12:00:00Z',
    targets: [gateway, target({})],
    dns: {
      name: 'example.com',
      resolver: '192.168.1.1:53',
      ok: true,
      lookup_ms: 18.42,
      failures: 0,
      last_ok: '2026-11-07T12:00:00Z',
    },
    ...over,
  }
}

/** The stats of the row headed by the given name, as "label value". */
function stats(name: string): string[] {
  const head = screen.getByText(name, { selector: '.name' })
  const row = head.closest('li, .dns') as HTMLElement
  return [...row.querySelectorAll('.stats > div')].map((d) =>
    (d.textContent ?? '').replace(/\s+/g, ' ').trim(),
  )
}

/** The Card's status dot label. */
function cardStatus(): string | null {
  return screen.queryByRole('img')?.getAttribute('aria-label') ?? null
}

describe('NetQualityWidget', () => {
  // Clock times get a date in front unless they are from today, so pin today
  // to the fixtures' day. Just before 12:00Z keeps every fixture time on the
  // same local day in every real time zone.
  beforeEach(() => {
    live.set(null)
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date('2026-11-07T11:59:59Z'))
  })
  afterEach(() => vi.useRealTimers())

  it('waits for the first snapshot', () => {
    render(NetQualityWidget)
    expect(screen.getByText('Waiting for data…')).toBeTruthy()
  })

  it('shows a good connection from the live snapshot', async () => {
    render(NetQualityWidget)
    live.set(snap(section({})))
    await tick()

    expect(screen.getByText('Good').dataset.status).toBe('good')
    expect(cardStatus()).toBe('OK')
    expect(stats('Router')).toEqual([
      'Latency 0.6 ms',
      'Jitter 0.1 ms',
      'Loss 0%',
    ])
    expect(screen.getByText('192.168.1.1')).toBeTruthy()
    expect(stats('1.1.1.1')).toEqual([
      'Latency 12.3 ms',
      'Jitter 0.8 ms',
      'Loss 0%',
    ])
    expect(stats('DNS')).toEqual(['Lookup 18.4 ms', 'Last failure None'])
    expect(screen.queryByText('TCP')).toBeNull()
    expect(screen.queryByText(/outage/i)).toBeNull()
  })

  it('shows a degraded connection with the TCP method', () => {
    render(NetQualityWidget, {
      snap: snap(
        section({
          status: 'degraded',
          targets: [
            { ...gateway, method: 'tcp' },
            target({ method: 'tcp', loss_pct: 20 }),
          ],
          dns: { ...section({}).dns!, failures: 4 },
        }),
      ),
      history: {},
    })

    expect(screen.getByText('Degraded').dataset.status).toBe('degraded')
    expect(cardStatus()).toBe('Warning')
    expect(screen.getAllByText('TCP')).toHaveLength(2)
    expect(stats('1.1.1.1')[2]).toBe('Loss 20%')
    expect(stats('DNS')[1]).toBe('Last failure 4 since start')
  })

  it('shows the last DNS failure in view', () => {
    const t = Date.parse('2026-11-07T11:59:40Z') / 1000
    const failures: SeriesHistory = [
      [t, t + 5, t + 10],
      [4, 5, 5],
    ]
    render(NetQualityWidget, {
      snap: snap(section({ dns: { ...section({}).dns!, failures: 5 } })),
      history: { 'netq.dns.failures': failures },
    })

    const clock = new Date((t + 5) * 1000).toTimeString().slice(0, 8)
    expect(stats('DNS')[1]).toBe(`Last failure ${clock}`)
  })

  it('shows an ongoing outage when down', () => {
    const t0 = Date.parse('2026-11-07T11:58:00Z') / 1000
    const times = [0, 1, 2, 3, 4].map((i) => t0 + i * 5)
    const down: SeriesHistory = [times, [0, 0, 100, 100, 100]]
    render(NetQualityWidget, {
      snap: snap(
        section({
          status: 'down',
          targets: [
            gateway,
            target({
              latency_ms: null,
              jitter_ms: null,
              loss_pct: 100,
              error: 'sendto: network is unreachable',
            }),
          ],
          dns: {
            ...section({}).dns!,
            ok: false,
            lookup_ms: null,
            failures: 3,
            error: 'no answer from 192.168.1.1:53 within 2s',
          },
        }),
      ),
      history: { 'netq.1-1-1-1.loss_pct': down },
    })

    expect(screen.getByText('Down').dataset.status).toBe('down')
    expect(cardStatus()).toBe('Critical')
    expect(stats('1.1.1.1')).toEqual(['Latency –', 'Jitter –', 'Loss 100%'])
    expect(screen.getByText('sendto: network is unreachable')).toBeTruthy()
    expect(stats('DNS')).toEqual(['Lookup Failed', 'Last failure Now'])
    expect(
      screen.getByText('no answer from 192.168.1.1:53 within 2s'),
    ).toBeTruthy()
    const clock = new Date(times[2] * 1000).toTimeString().slice(0, 8)
    expect(screen.getByText(/^Outage since/).textContent?.trim()).toBe(
      `Outage since ${clock}, 10s so far`,
    )
  })

  it('shows an outage that ended', () => {
    const t0 = Date.parse('2026-11-07T11:58:00Z') / 1000
    const times = [0, 1, 2, 3, 4, 5].map((i) => t0 + i * 5)
    render(NetQualityWidget, {
      snap: snap(section({})),
      history: {
        'netq.1-1-1-1.loss_pct': [times, [0, 100, 100, 100, 0, 0]],
      },
    })

    const clock = new Date(times[1] * 1000).toTimeString().slice(0, 8)
    expect(screen.getByText(/^Last outage/).textContent?.trim()).toBe(
      `Last outage: ${clock}, lasted 15s`,
    )
  })

  it('explains the pending state', () => {
    render(NetQualityWidget, {
      snap: snap({ available: true, status: 'pending', interval_s: 10 }),
      history: {},
    })
    expect(screen.getByText('Checking…')).toBeTruthy()
    expect(screen.getByText('Running the first check…')).toBeTruthy()
    expect(cardStatus()).toBeNull()
  })

  it('explains when the checks are turned off', () => {
    render(NetQualityWidget, { snap: snap({ available: false }), history: {} })
    expect(
      screen.getByText('Internet health checks are turned off.'),
    ).toBeTruthy()
    expect(cardStatus()).toBe('Unknown')
  })

  it('shows a collector error', () => {
    render(NetQualityWidget, {
      snap: snap({ error: 'netquality: boom' }),
      history: {},
    })
    expect(
      screen.getByText('Internet health unavailable: netquality: boom'),
    ).toBeTruthy()
    expect(cardStatus()).toBe('Unknown')
  })
})
