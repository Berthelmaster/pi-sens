import { formatDuration } from '../lib/format'
import { parseTimestamp } from './snapshot'

export type AlertSeverity = 'warning' | 'critical'

/**
 * One rule for one series (alerts.Alert in internal/alerts/evaluator.go).
 * Times are RFC 3339.
 */
export interface Alert {
  rule: string
  series: string
  severity: AlertSeverity
  op: '>' | '<'
  threshold: number
  /** How long the comparison has to hold before the alert fires. */
  for_s: number
  state: 'pending' | 'firing' | 'resolved'
  /** The series value at the alert's last state change. */
  value: number
  pending_at: string
  firing_at?: string
  resolved_at?: string
}

/**
 * What /api/alerts returns and each `alerts` event on /api/stream carries
 * (alerts.Set in Go). firing lists critical alerts first, resolved the most
 * recent first.
 */
export interface AlertSet {
  /** Goes up by one every time the set changes. */
  version: number
  firing: Alert[]
  resolved: Alert[]
}

/** Whether any of the alerts is critical. */
export function hasCritical(alerts: readonly Alert[]): boolean {
  return alerts.some((a) => a.severity === 'critical')
}

/** The most severe of the alerts, or null when there are none. */
export function worstSeverity(alerts: readonly Alert[]): AlertSeverity | null {
  if (alerts.length === 0) return null
  return hasCritical(alerts) ? 'critical' : 'warning'
}

/** A number without trailing zeros and at most two decimals, e.g. 83.4. */
function trimmed(v: number): string {
  if (!Number.isFinite(v)) return String(v)
  return String(Number(v.toFixed(2)))
}

/**
 * The breached comparison, e.g. "temp.cpu_c 83.4 > 82". The alert's own
 * value is from when it fired; pass the latest one to show that instead.
 */
export function describeCondition(a: Alert, value = a.value): string {
  return `${a.series} ${trimmed(value)} ${a.op} ${trimmed(a.threshold)}`
}

/** The rule's comparison without a value, e.g. "temp.cpu_c > 82". */
export function describeRule(a: Alert): string {
  return `${a.series} ${a.op} ${trimmed(a.threshold)}`
}

/** Unix milliseconds of an alert time, or null when missing or invalid. */
export function alertTime(ts: string | undefined): number | null {
  if (!ts) return null
  const t = parseTimestamp(ts)
  return Number.isFinite(t) ? t : null
}

function pad(n: number): string {
  return String(n).padStart(2, '0')
}

/**
 * Local clock time of t (Unix milliseconds), e.g. "09:20:42", with the date
 * in front when it is not on the same day as now, e.g. "2026-11-06 23:58:10".
 */
export function formatClock(t: number, now: number): string {
  const d = new Date(t)
  const n = new Date(now)
  const time = `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
  const sameDay =
    d.getFullYear() === n.getFullYear() &&
    d.getMonth() === n.getMonth() &&
    d.getDate() === n.getDate()
  if (sameDay) return time
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${time}`
}

/**
 * When a firing alert started and how long ago, e.g. "since 09:20:42 (5m 3s)".
 * The backend's clock may be a little ahead of the browser's, so a start in
 * the future counts as just now.
 */
export function firingSince(a: Alert, now: number): string {
  const t = alertTime(a.firing_at)
  if (t === null) return ''
  const ago = formatDuration(Math.max(0, (now - t) / 1000))
  return `since ${formatClock(t, now)} (${ago})`
}

/**
 * When a resolved alert ended and how long it fired, e.g.
 * "resolved 09:31:02 after 10m 20s".
 */
export function resolvedSummary(a: Alert, now: number): string {
  const end = alertTime(a.resolved_at)
  if (end === null) return ''
  const start = alertTime(a.firing_at)
  const parts = [`resolved ${formatClock(end, now)}`]
  if (start !== null) {
    parts.push(`after ${formatDuration(Math.max(0, (end - start) / 1000))}`)
  }
  return parts.join(' ')
}

/**
 * A key for an alert in a list. Rule and series identify a firing alert; a
 * resolved one also needs the time it resolved.
 */
export function alertKey(a: Alert): string {
  return `${a.rule}\u0000${a.series}\u0000${a.resolved_at ?? ''}`
}
