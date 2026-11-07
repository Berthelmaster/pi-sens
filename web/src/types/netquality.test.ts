import { describe, expect, it } from 'vitest'
import type { SeriesHistory } from '../lib/live'
import {
  cardStatus,
  describeOutage,
  formatLoss,
  formatMs,
  lastDnsFailure,
  lastOutage,
  netqualitySection,
  outageKeys,
  STATUS_LABEL,
  statusOf,
  targetLabel,
  type Outage,
} from './netquality'
import type { Snapshot } from './snapshot'

/** A loss history with one sample every 5 s from t0. */
function loss(values: number[], t0 = 1000): SeriesHistory {
  return [values.map((_, i) => t0 + i * 5), values]
}

describe('statusOf', () => {
  it('accepts the four backend statuses', () => {
    expect(statusOf('good')).toBe('good')
    expect(statusOf('degraded')).toBe('degraded')
    expect(statusOf('down')).toBe('down')
    expect(statusOf('pending')).toBe('pending')
  })

  it('rejects anything else', () => {
    expect(statusOf(undefined)).toBeUndefined()
    expect(statusOf('')).toBeUndefined()
    expect(statusOf('Good')).toBeUndefined()
    expect(statusOf(1)).toBeUndefined()
  })
})

describe('cardStatus', () => {
  it('colours good green, degraded amber and down red', () => {
    expect(cardStatus('good')).toBe('ok')
    expect(cardStatus('degraded')).toBe('warning')
    expect(cardStatus('down')).toBe('critical')
  })

  it('stays neutral while pending or unknown', () => {
    expect(cardStatus('pending')).toBeUndefined()
    expect(cardStatus(undefined)).toBeUndefined()
  })

  it('has a badge label for every status', () => {
    expect(STATUS_LABEL).toEqual({
      good: 'Good',
      degraded: 'Degraded',
      down: 'Down',
      pending: 'Checking…',
    })
  })
})

describe('targetLabel', () => {
  it('calls the gateway the router', () => {
    expect(targetLabel({ name: 'gateway', kind: 'gateway' })).toBe('Router')
    expect(targetLabel({ name: '1.1.1.1', kind: 'internet' })).toBe('1.1.1.1')
  })
})

describe('formatMs', () => {
  it('shows one decimal below 100 ms and none above', () => {
    expect(formatMs(0.412)).toBe('0.4 ms')
    expect(formatMs(23.456)).toBe('23.5 ms')
    expect(formatMs(99.94)).toBe('99.9 ms')
    expect(formatMs(99.96)).toBe('100 ms')
    expect(formatMs(151.2)).toBe('151 ms')
  })

  it('shows a placeholder for a missing value', () => {
    expect(formatMs(null)).toBe('–')
    expect(formatMs(undefined)).toBe('–')
  })
})

describe('formatLoss', () => {
  it('drops decimals for whole percentages', () => {
    expect(formatLoss(0)).toBe('0%')
    expect(formatLoss(20)).toBe('20%')
    expect(formatLoss(100)).toBe('100%')
    expect(formatLoss(33.33)).toBe('33.3%')
    expect(formatLoss(null)).toBe('–')
  })
})

describe('netqualitySection', () => {
  const snap = (netquality: unknown): Snapshot => ({
    timestamp: '2026-11-07T12:00:00Z',
    collectors: { netquality },
    series: {},
  })

  it('returns the section, an error or undefined', () => {
    expect(netqualitySection(snap({ available: false }))).toEqual({
      available: false,
    })
    expect(netqualitySection(snap({ error: 'x' }))).toEqual({ error: 'x' })
    expect(netqualitySection(snap(undefined))).toBeUndefined()
    expect(netqualitySection(snap(null))).toBeUndefined()
    expect(netqualitySection(null)).toBeUndefined()
  })
})

describe('outageKeys', () => {
  it('uses the internet targets when there are any', () => {
    expect(
      outageKeys([
        { key: 'gateway', kind: 'gateway' },
        { key: '1-1-1-1', kind: 'internet' },
        { key: '8-8-8-8', kind: 'internet' },
      ]),
    ).toEqual(['netq.1-1-1-1.loss_pct', 'netq.8-8-8-8.loss_pct'])
  })

  it('falls back to the gateway', () => {
    expect(outageKeys([{ key: 'gateway', kind: 'gateway' }])).toEqual([
      'netq.gateway.loss_pct',
    ])
    expect(outageKeys([])).toEqual([])
  })
})

describe('lastOutage', () => {
  it('finds nothing without histories or without total loss', () => {
    expect(lastOutage([])).toBeNull()
    expect(lastOutage([[[], []]])).toBeNull()
    expect(lastOutage([loss([0, 20, 80, 0])])).toBeNull()
  })

  it('finds an outage that ended', () => {
    expect(lastOutage([loss([0, 100, 100, 100, 0, 0])])).toEqual({
      start: 1005,
      end: 1020,
      latest: 1025,
      clipped: false,
    })
  })

  it('picks the most recent of several outages', () => {
    expect(lastOutage([loss([100, 0, 100, 100, 0])])).toEqual({
      start: 1010,
      end: 1020,
      latest: 1020,
      clipped: false,
    })
  })

  it('finds an ongoing outage', () => {
    expect(lastOutage([loss([0, 0, 100, 100])])).toEqual({
      start: 1010,
      end: null,
      latest: 1015,
      clipped: false,
    })
  })

  it('marks an outage that began before the window', () => {
    expect(lastOutage([loss([100, 100, 0])])?.clipped).toBe(true)
    expect(lastOutage([loss([100, 100])])).toEqual({
      start: 1000,
      end: null,
      latest: 1005,
      clipped: true,
    })
  })

  it('needs every target down at once', () => {
    const a = loss([0, 100, 100, 100, 0])
    const b = loss([0, 0, 100, 100, 100])
    expect(lastOutage([a, b])).toEqual({
      start: 1010,
      end: 1020,
      latest: 1020,
      clipped: false,
    })
    expect(lastOutage([loss([100, 0]), loss([0, 100])])).toBeNull()
  })

  it('skips times missing from a history', () => {
    // b has no sample at 1010, so that time neither starts nor ends the run.
    const a = loss([0, 100, 100, 100, 0])
    const b: SeriesHistory = [
      [1000, 1005, 1015, 1020],
      [0, 100, 100, 0],
    ]
    expect(lastOutage([a, b])).toEqual({
      start: 1005,
      end: 1020,
      latest: 1020,
      clipped: false,
    })
  })

  it('does not depend on the order of the samples', () => {
    const [t, v] = loss([0, 100, 100, 0])
    expect(lastOutage([[[...t].reverse(), [...v].reverse()]])).toEqual(
      lastOutage([[t, v]]),
    )
  })
})

describe('describeOutage', () => {
  // Local times, so the clock text does not depend on the time zone.
  const at = (h: number, m: number, s: number) =>
    new Date(2026, 10, 7, h, m, s).getTime() / 1000
  const now = new Date(2026, 10, 7, 9, 30, 0).getTime()

  it('describes an outage that ended', () => {
    const o: Outage = {
      start: at(9, 20, 5),
      end: at(9, 20, 45),
      latest: at(9, 29, 55),
      clipped: false,
    }
    expect(describeOutage(o, now)).toBe('Last outage: 09:20:05, lasted 40s')
    expect(describeOutage({ ...o, clipped: true }, now)).toBe(
      'Last outage: ended 09:20:45, lasted over 40s',
    )
  })

  it('describes an ongoing outage', () => {
    const o: Outage = {
      start: at(9, 28, 0),
      end: null,
      latest: at(9, 29, 35),
      clipped: false,
    }
    expect(describeOutage(o, now)).toBe('Outage since 09:28:00, 1m 35s so far')
    expect(describeOutage({ ...o, clipped: true }, now)).toBe(
      'Outage for over 1m 35s',
    )
  })

  it('shows the date for an outage on another day', () => {
    const o: Outage = {
      start: at(9, 20, 5) - 86400,
      end: at(9, 20, 45) - 86400,
      latest: at(9, 29, 55),
      clipped: false,
    }
    expect(describeOutage(o, now)).toBe(
      'Last outage: 2026-11-06 09:20:05, lasted 40s',
    )
  })
})

describe('lastDnsFailure', () => {
  it('returns the newest time the counter went up', () => {
    expect(lastDnsFailure(loss([2, 2, 3, 3, 4, 4]))).toBe(1020)
  })

  it('returns null when it did not go up in view', () => {
    expect(lastDnsFailure([[], []])).toBeNull()
    expect(lastDnsFailure(loss([5]))).toBeNull()
    expect(lastDnsFailure(loss([3, 3, 3]))).toBeNull()
  })

  it('ignores a restart resetting the counter', () => {
    expect(lastDnsFailure(loss([7, 7, 0, 0]))).toBeNull()
    expect(lastDnsFailure(loss([7, 8, 0, 0]))).toBe(1005)
  })
})
