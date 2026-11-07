import { fireEvent, render, screen } from '@testing-library/svelte'
import { tick } from 'svelte'
import type { Writable } from 'svelte/store'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { alertPanel } from '../lib/alertPanel.svelte'
import { alerts as mockedAlerts, snapshot as mockedSnapshot } from '../lib/live'
import type { Alert, AlertSet } from '../types/alerts'
import type { Snapshot } from '../types/snapshot'
import AlertBanner from './AlertBanner.svelte'

// The banner only reads the alerts and snapshot stores, so plain writables
// stand in for the live connection and the tests set them directly.
vi.mock('../lib/live', async () => {
  const { writable } = await import('svelte/store')
  return { alerts: writable(null), snapshot: writable(null) }
})

// The mocked stores, set directly by the tests.
const live = {
  alerts: mockedAlerts as Writable<AlertSet | null>,
  snapshot: mockedSnapshot as Writable<Snapshot | null>,
}

const FIRED = '2026-11-07T09:20:00Z'
const RESOLVED = '2026-11-07T09:30:00Z'

function alert(
  rule: string,
  series: string,
  state: Alert['state'] = 'firing',
  severity: Alert['severity'] = 'warning',
): Alert {
  return {
    rule,
    series,
    severity,
    op: '>',
    threshold: 80,
    for_s: 30,
    state,
    value: 85,
    pending_at: FIRED,
    firing_at: FIRED,
    resolved_at: state === 'resolved' ? RESOLVED : undefined,
  }
}

let version = 0

/** Publishes a new alert set, as an `alerts` event on the stream would. */
async function publish(firing: Alert[], resolved: Alert[] = []) {
  live.alerts.set({ version: ++version, firing, resolved })
  await tick()
}

function banner() {
  return screen.queryByRole('region', { name: 'Alerts' })
}

describe('AlertBanner', () => {
  beforeEach(() => {
    live.alerts.set(null)
    live.snapshot.set(null)
    // Module level state, shared with the nav button; reset per test.
    alertPanel.expanded = false
    alertPanel.recentOpen = false
  })

  it('stays hidden until alerts are known and while nothing fires', async () => {
    render(AlertBanner)
    expect(banner()).toBeNull()

    await publish([])
    expect(banner()).toBeNull()
  })

  it('appears when an alert fires and goes when it resolves', async () => {
    render(AlertBanner)
    await publish([])

    const hot = alert('CPU hot', 'temp.cpu_c', 'firing', 'critical')
    await publish([hot])

    const section = banner()
    expect(section).not.toBeNull()
    expect(section!.dataset.severity).toBe('critical')
    expect(section!.textContent).toContain('CPU hot')
    expect(section!.textContent).toContain('temp.cpu_c 85 > 80')
    expect(screen.getByText('1 alert firing, 1 critical')).toBeTruthy()

    await publish([], [{ ...hot, state: 'resolved', resolved_at: RESOLVED }])
    expect(banner()).toBeNull()
  })

  it('shows the latest series value while an alert fires', async () => {
    render(AlertBanner)
    await publish([alert('CPU hot', 'temp.cpu_c')])

    live.snapshot.set({
      timestamp: FIRED,
      collectors: {},
      series: { 'temp.cpu_c': 91.25 },
    })
    await tick()

    expect(banner()!.textContent).toContain('temp.cpu_c 91.25 > 80')
  })

  it('collapses to the first alert with more than two firing', async () => {
    render(AlertBanner)
    await publish([
      alert('CPU hot', 'temp.cpu_c', 'firing', 'critical'),
      alert('Memory high', 'mem.used_pct'),
      alert('Disk full', 'disk.used_pct'),
    ])

    const firing = () => banner()!.querySelectorAll('#firing-alerts > li .rule')
    expect([...firing()].map((el) => el.textContent)).toEqual(['CPU hot'])

    const toggle = screen.getByRole('button', { name: 'Show all 3 firing' })
    expect(toggle.getAttribute('aria-expanded')).toBe('false')

    await fireEvent.click(toggle)
    expect([...firing()].map((el) => el.textContent)).toEqual([
      'CPU hot',
      'Memory high',
      'Disk full',
    ])
    expect(toggle.textContent?.trim()).toBe('Show fewer')
    expect(toggle.getAttribute('aria-expanded')).toBe('true')
  })

  it('does not collapse with two firing', async () => {
    render(AlertBanner)
    await publish([
      alert('CPU hot', 'temp.cpu_c'),
      alert('Memory high', 'mem.used_pct'),
    ])

    expect(banner()!.querySelectorAll('#firing-alerts > li')).toHaveLength(2)
    expect(screen.queryByRole('button', { name: /^Show all/ })).toBeNull()
  })

  it('shows resolved alerts from the Recent alerts toggle', async () => {
    render(AlertBanner)
    await publish(
      [alert('CPU hot', 'temp.cpu_c')],
      [alert('Disk full', 'disk.used_pct', 'resolved')],
    )
    expect(screen.queryByRole('heading', { name: 'Recent alerts' })).toBeNull()

    await fireEvent.click(screen.getByRole('button', { name: 'Recent alerts' }))

    const recent = document.getElementById('recent-alerts')
    expect(recent).not.toBeNull()
    expect(recent!.textContent).toContain('Disk full')
    expect(recent!.textContent).toContain('disk.used_pct > 80')
    expect(recent!.textContent).toContain('after 10m')

    await fireEvent.click(
      screen.getByRole('button', { name: 'Hide recent alerts' }),
    )
    expect(document.getElementById('recent-alerts')).toBeNull()
  })

  it('keeps the banner open for recent alerts with nothing firing', async () => {
    alertPanel.recentOpen = true
    render(AlertBanner)
    await publish([], [])

    expect(banner()).not.toBeNull()
    expect(screen.getByText('No alerts firing.')).toBeTruthy()
    expect(screen.getByText('Nothing has resolved yet.')).toBeTruthy()
  })
})
