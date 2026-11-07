import { render, screen } from '@testing-library/svelte'
import { tick } from 'svelte'
import type { Writable } from 'svelte/store'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { snapshot as mockedSnapshot } from '../lib/live'
import type { NetinfoSection, NetworkSection } from '../types/network'
import type { Snapshot } from '../types/snapshot'
import NetworkWidget from './NetworkWidget.svelte'

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

const counters = {
  rx_bytes: 1000,
  rx_packets: 10,
  rx_errors: 0,
  rx_dropped: 0,
  tx_bytes: 500,
  tx_packets: 5,
  tx_errors: 0,
  tx_dropped: 0,
}

// The network collector skips docker0 on purpose, so only netinfo has it.
const network: NetworkSection = {
  interfaces: [
    {
      name: 'eth0',
      ...counters,
      rates: { rx_bps: 125, tx_bps: 250, rx_pps: 1, tx_pps: 1 },
    },
    // Counted, but no rates yet on the first sample.
    { name: 'wlan0', ...counters },
  ],
  total: { ...counters },
}

const netinfo: NetinfoSection = {
  interfaces: [
    {
      name: 'docker0',
      operstate: 'up',
      mtu: 1500,
      wireless: false,
      ipv4: ['172.17.0.1/16'],
      ipv6: [],
    },
    {
      name: 'eth0',
      operstate: 'up',
      mtu: 1500,
      wireless: false,
      ipv4: ['192.168.1.10/24'],
      ipv6: [],
    },
    {
      name: 'wlan0',
      operstate: 'up',
      mtu: 1500,
      wireless: false,
      ipv4: [],
      ipv6: [],
    },
  ],
  default_gateway: { address: '192.168.1.1', interface: 'eth0' },
}

/** The list item of one interface, found by its name. */
function iface(name: string): HTMLElement {
  const li = screen.getByText(name).closest('li')
  expect(li).not.toBeNull()
  return li!
}

describe('NetworkWidget', () => {
  beforeEach(() => live.set(null))

  it('leaves the rate line out for interfaces without counters', async () => {
    render(NetworkWidget)
    live.set({
      timestamp: '2026-11-07T09:20:00Z',
      collectors: { network, netinfo },
      series: {},
    })
    await tick()

    const docker = iface('docker0')
    expect(docker.querySelector('.rates')).toBeNull()
    expect(docker.querySelector('.badge')!.textContent).toBe('Up')
    expect(docker.textContent).toContain('172.17.0.1/16')
    expect(docker.textContent).not.toContain('↓')

    const eth = iface('eth0')
    const rates = eth.querySelector('.rates')
    expect(rates).not.toBeNull()
    expect(rates!.textContent).toContain('↓')
    expect(rates!.textContent).toContain('↑')

    // A counted interface keeps its rate line even before the first rate.
    expect(iface('wlan0').querySelector('.rates')).not.toBeNull()
  })
})
