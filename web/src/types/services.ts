import type { CollectorError, Snapshot } from './snapshot'

/**
 * The `services` collector's section of the snapshot (servicesData in
 * internal/collectors/services.go). Without systemctl or a running systemd
 * only `available: false` is sent.
 *
 * Units with load state "not-found" are left out of the counts and the
 * failed list.
 */
export interface ServicesSection {
  available: boolean
  active?: number
  failed?: number
  total?: number
  /** Sorted by unit name. */
  failed_units?: ServiceUnit[]
  /**
   * In the order of the watch list. A unit systemd does not know is
   * reported with load "not-found", active "inactive" and sub "dead".
   */
  watched?: ServiceUnit[]
}

export interface ServiceUnit {
  /** Full unit name, e.g. "ssh.service". */
  unit: string
  /** e.g. "loaded", "not-found", "masked". */
  load: string
  /** e.g. "active", "inactive", "failed", "activating". */
  active: string
  /** e.g. "running", "exited", "dead". */
  sub: string
  description?: string
}

/** The services section of a snapshot, or undefined when it has none. */
export function servicesSection(
  snap: Snapshot | null,
): ServicesSection | CollectorError | undefined {
  const section = snap?.collectors.services
  if (typeof section !== 'object' || section === null) return undefined
  return section as ServicesSection | CollectorError
}

/** A unit list from the section, or an empty one when it is missing. */
export function unitList(list: unknown): ServiceUnit[] {
  return Array.isArray(list) ? (list as ServiceUnit[]) : []
}

/** "ssh.service" -> "ssh". Other unit types keep their suffix. */
export function shortUnitName(unit: string): string {
  return unit.endsWith('.service') ? unit.slice(0, -'.service'.length) : unit
}

export type WatchState = 'active' | 'inactive' | 'failed' | 'changing'

export interface WatchBadge {
  state: WatchState
  label: string
}

/**
 * The badge for a watched unit: active, failed, inactive (also for a unit
 * systemd does not know) or changing while it starts, stops or reloads.
 */
export function watchBadge(
  u: Pick<ServiceUnit, 'load' | 'active'>,
): WatchBadge {
  if (u.load === 'not-found') return { state: 'inactive', label: 'not found' }
  switch (u.active) {
    case 'active':
      return { state: 'active', label: 'active' }
    case 'failed':
      return { state: 'failed', label: 'failed' }
    case 'inactive':
      return { state: 'inactive', label: 'inactive' }
    default:
      return { state: 'changing', label: u.active || 'unknown' }
  }
}

/**
 * The services card status: critical when any unit has failed, warning when
 * a watched unit is inactive or unknown to systemd, ok otherwise. A watched
 * unit that is starting, stopping or reloading does not count against it.
 */
export function servicesStatus(
  section: Pick<ServicesSection, 'failed' | 'failed_units' | 'watched'>,
): 'ok' | 'warning' | 'critical' {
  const failedUnits = unitList(section.failed_units)
  const watched = unitList(section.watched)
  const failed =
    (typeof section.failed === 'number' && section.failed > 0) ||
    failedUnits.length > 0 ||
    watched.some((u) => watchBadge(u).state === 'failed')
  if (failed) return 'critical'
  if (watched.some((u) => watchBadge(u).state === 'inactive')) return 'warning'
  return 'ok'
}
