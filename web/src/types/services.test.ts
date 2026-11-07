import { describe, expect, it } from 'vitest'
import {
  servicesSection,
  servicesStatus,
  shortUnitName,
  unitList,
  watchBadge,
} from './services'
import type { ServiceUnit } from './services'
import type { Snapshot } from './snapshot'

function unit(name: string, active: string, load = 'loaded'): ServiceUnit {
  return {
    unit: name,
    load,
    active,
    sub: active === 'active' ? 'running' : 'dead',
  }
}

describe('servicesSection', () => {
  const snap = (services: unknown): Snapshot => ({
    timestamp: '2026-11-07T12:00:00Z',
    collectors: { services },
    series: {},
  })

  it('returns the section, an error or undefined', () => {
    expect(servicesSection(snap({ available: false }))).toEqual({
      available: false,
    })
    expect(servicesSection(snap({ error: 'boom' }))).toEqual({ error: 'boom' })
    expect(servicesSection(snap(undefined))).toBeUndefined()
    expect(servicesSection(null)).toBeUndefined()
  })
})

describe('watchBadge', () => {
  it('maps the active state to a badge', () => {
    expect(watchBadge(unit('a.service', 'active')).state).toBe('active')
    expect(watchBadge(unit('a.service', 'failed')).state).toBe('failed')
    expect(watchBadge(unit('a.service', 'inactive')).state).toBe('inactive')
    expect(watchBadge(unit('a.service', 'activating'))).toEqual({
      state: 'changing',
      label: 'activating',
    })
  })

  it('shows a unit systemd does not know as not found', () => {
    expect(watchBadge(unit('a.service', 'inactive', 'not-found'))).toEqual({
      state: 'inactive',
      label: 'not found',
    })
  })
})

describe('servicesStatus', () => {
  it('is critical when any unit has failed', () => {
    expect(servicesStatus({ failed: 1, failed_units: [] })).toBe('critical')
    expect(
      servicesStatus({ failed_units: [unit('x.service', 'failed')] }),
    ).toBe('critical')
    expect(
      servicesStatus({ failed: 0, watched: [unit('x.service', 'failed')] }),
    ).toBe('critical')
  })

  it('warns when a watched unit is inactive or missing', () => {
    expect(
      servicesStatus({ failed: 0, watched: [unit('x.service', 'inactive')] }),
    ).toBe('warning')
    expect(
      servicesStatus({
        failed: 0,
        watched: [unit('x.service', 'inactive', 'not-found')],
      }),
    ).toBe('warning')
  })

  it('is ok when nothing failed and watched units are up', () => {
    expect(
      servicesStatus({
        failed: 0,
        failed_units: [],
        watched: [unit('a.service', 'active'), unit('b.service', 'reloading')],
      }),
    ).toBe('ok')
    expect(servicesStatus({})).toBe('ok')
  })
})

describe('helpers', () => {
  it('drops the .service suffix only', () => {
    expect(shortUnitName('ssh.service')).toBe('ssh')
    expect(shortUnitName('foo.socket')).toBe('foo.socket')
  })

  it('treats a missing list as empty', () => {
    expect(unitList(undefined)).toEqual([])
    expect(unitList(null)).toEqual([])
  })
})
