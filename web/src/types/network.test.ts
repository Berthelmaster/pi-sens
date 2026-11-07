import { describe, expect, it } from 'vitest'
import {
  formatLinkSpeed,
  interfaceViews,
  isLinkLocal6,
  netSeriesNames,
  niceCeil,
  signalBars,
  type NetinfoSection,
  type NetworkSection,
} from './network'

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

describe('netSeriesNames', () => {
  it('matches the backend key names', () => {
    const keys = netSeriesNames(['wlan0', 'eth0.100', 'Total', 'eth0_100'])
    expect(keys.get('Total')).toBe('total_2')
    expect(keys.get('eth0.100')).toBe('eth0_100')
    expect(keys.get('eth0_100')).toBe('eth0_100_2')
    expect(keys.get('wlan0')).toBe('wlan0')
  })
})

describe('interfaceViews', () => {
  const network: NetworkSection = {
    interfaces: [
      {
        name: 'eth0',
        ...counters,
        rates: { rx_bps: 125, tx_bps: 250, rx_pps: 1, tx_pps: 1 },
      },
      { name: 'wlan0', ...counters },
    ],
    total: { ...counters },
  }
  const netinfo: NetinfoSection = {
    interfaces: [
      {
        name: 'eth0',
        operstate: 'down',
        mac: 'dc:a6:32:00:00:01',
        mtu: 1500,
        wireless: false,
        ipv4: [],
        ipv6: [],
      },
      {
        name: 'docker0',
        operstate: 'up',
        mtu: 1500,
        wireless: false,
        ipv4: ['172.17.0.1/16'],
        ipv6: [],
      },
      {
        name: 'wlan0',
        operstate: 'up',
        mtu: 1500,
        wireless: true,
        speed_mbps: -1,
        ipv4: ['192.168.1.20/24'],
        ipv6: ['fe80::1/64'],
        wifi: { signal_dbm: -60, link_quality: 50, quality_pct: 71.43 },
      },
    ],
    default_gateway: { address: '192.168.1.1', interface: 'wlan0' },
  }

  it('joins both collectors by name', () => {
    const views = interfaceViews(network, netinfo)
    expect(views.map((v) => v.name)).toEqual(['docker0', 'eth0', 'wlan0'])

    const [docker, eth, wlan] = views
    expect(docker.seriesKey).toBeUndefined()
    expect(docker.rxBytes).toBeUndefined()

    expect(eth.active).toBe(false)
    expect(eth.rxBits).toBe(1000)
    expect(eth.txBits).toBe(2000)
    expect(eth.seriesKey).toBe('net.eth0')

    expect(wlan.active).toBe(true)
    expect(wlan.gateway).toBe(true)
    expect(wlan.speedMbps).toBeUndefined()
    expect(wlan.rxBits).toBeUndefined()
    expect(wlan.wifi?.signal_dbm).toBe(-60)
  })

  it('works with only one collector', () => {
    expect(interfaceViews(network, undefined).map((v) => v.state)).toEqual([
      'unknown',
      'unknown',
    ])
    expect(interfaceViews(undefined, netinfo)).toHaveLength(3)
    expect(interfaceViews(undefined, undefined)).toEqual([])
  })
})

describe('formatLinkSpeed', () => {
  it('picks Mb/s or Gb/s', () => {
    expect(formatLinkSpeed(100)).toBe('100 Mb/s')
    expect(formatLinkSpeed(1000)).toBe('1 Gb/s')
    expect(formatLinkSpeed(2500)).toBe('2.5 Gb/s')
    expect(formatLinkSpeed(undefined)).toBe('–')
    expect(formatLinkSpeed(-1)).toBe('–')
  })
})

describe('signalBars', () => {
  it('maps dBm to bars', () => {
    expect(signalBars(-40)).toBe(4)
    expect(signalBars(-60)).toBe(3)
    expect(signalBars(-70)).toBe(2)
    expect(signalBars(-80)).toBe(1)
    expect(signalBars(-90)).toBe(0)
    expect(signalBars(undefined)).toBe(0)
  })
})

describe('niceCeil', () => {
  it('rounds up to 1, 2 or 5 times a power of ten', () => {
    expect(niceCeil(8000)).toBe(10000)
    expect(niceCeil(10000)).toBe(10000)
    expect(niceCeil(12000)).toBe(20000)
    expect(niceCeil(0.3)).toBe(0.5)
    expect(niceCeil(4.2e6)).toBe(5e6)
    expect(niceCeil(0)).toBe(1)
  })
})

describe('isLinkLocal6', () => {
  it('spots fe80::/10', () => {
    expect(isLinkLocal6('fe80::1/64')).toBe(true)
    expect(isLinkLocal6('2001:db8::1/64')).toBe(false)
  })
})
