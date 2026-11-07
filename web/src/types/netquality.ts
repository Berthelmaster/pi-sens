import type { CardStatus } from '../components/Card.svelte'
import { formatDuration, num, PLACEHOLDER } from '../lib/format'
import type { SeriesHistory } from '../lib/live'
import { formatClock } from './alerts'
import type { CollectorError, Snapshot } from './snapshot'

/**
 * Overall internet health, worked out by the backend each probe round
 * (netqStatus in internal/collectors/netquality.go):
 *
 * - down: every internet target lost every ping (with no internet targets,
 *   the gateway did)
 * - degraded: a target lost a ping or averaged over 150 ms, or the DNS
 *   lookup failed
 * - good: none of that
 * - pending: the first round has not finished yet
 */
export type NetQualityStatus = 'good' | 'degraded' | 'down' | 'pending'

/**
 * The `netquality` collector's section of the snapshot (netqData in
 * internal/collectors/netquality.go). With the probes turned off
 * (PISENS_NETQ=off) only `available: false` is sent, and while the first
 * round runs only `available`, `status` and `interval_s`.
 */
export interface NetQualitySection {
  available: boolean
  status?: NetQualityStatus
  /** Seconds between two probe rounds. */
  interval_s?: number
  /** RFC 3339 time the latest round finished. */
  probed_at?: string
  /** The gateway first, then the internet targets in configured order. */
  targets?: NetQualityTarget[]
  /** The latest DNS lookup; missing when lookups are turned off. */
  dns?: NetQualityDns
}

export interface NetQualityTarget {
  /** "gateway" or the target as configured, e.g. "1.1.1.1". */
  name: string
  /** The series key segment: netq.<key>.latency_ms. */
  key: string
  kind: 'gateway' | 'internet'
  /** The IPv4 address that was probed. */
  address?: string
  /** icmp, or tcp when the kernel does not allow ICMP sockets. */
  method?: 'icmp' | 'tcp'
  /** Average round trip time; null when no ping was answered. */
  latency_ms: number | null
  /** Mean change between consecutive round trips; null with under two. */
  jitter_ms: number | null
  loss_pct: number
  /** RFC 3339 time of the last round with an answer. */
  last_ok: string | null
  /** Why pings were lost, when that is known. */
  error?: string
}

export interface NetQualityDns {
  /** The host name looked up, e.g. "example.com". */
  name: string
  /** The nameserver asked, e.g. "192.168.1.1:53". */
  resolver?: string
  ok: boolean
  lookup_ms: number | null
  /** Failed lookups since pisens started. */
  failures: number
  /** RFC 3339 time of the last successful lookup. */
  last_ok: string | null
  error?: string
}

/** The netquality section of a snapshot, or undefined when it has none. */
export function netqualitySection(
  snap: Snapshot | null,
): NetQualitySection | CollectorError | undefined {
  const s = snap?.collectors.netquality
  return typeof s === 'object' && s !== null
    ? (s as NetQualitySection | CollectorError)
    : undefined
}

const STATUSES: readonly NetQualityStatus[] = [
  'good',
  'degraded',
  'down',
  'pending',
]

/** The section's status, or undefined when it is missing or unknown. */
export function statusOf(status: unknown): NetQualityStatus | undefined {
  return STATUSES.find((s) => s === status)
}

/** The badge text for a status. */
export const STATUS_LABEL: Record<NetQualityStatus, string> = {
  good: 'Good',
  degraded: 'Degraded',
  down: 'Down',
  pending: 'Checking…',
}

/**
 * The Card colour for a status: green, amber or red. Pending stays neutral
 * as nothing is known yet either way.
 */
export function cardStatus(
  status: NetQualityStatus | undefined,
): CardStatus | undefined {
  switch (status) {
    case 'good':
      return 'ok'
    case 'degraded':
      return 'warning'
    case 'down':
      return 'critical'
    default:
      return undefined
  }
}

/** How a target is labelled: the gateway is the "Router". */
export function targetLabel(t: Pick<NetQualityTarget, 'name' | 'kind'>) {
  return t.kind === 'gateway' ? 'Router' : t.name
}

export function latencyKey(t: Pick<NetQualityTarget, 'key'>): string {
  return `netq.${t.key}.latency_ms`
}

export function lossKey(t: Pick<NetQualityTarget, 'key'>): string {
  return `netq.${t.key}.loss_pct`
}

/** Series key of the DNS failure counter. */
export const DNS_FAILURES_KEY = 'netq.dns.failures'

/**
 * Formats a time in milliseconds: one decimal below 100 ms, none above,
 * e.g. 0.412 -> "0.4 ms", 23.456 -> "23.5 ms", 151.2 -> "151 ms".
 */
export function formatMs(ms: unknown): string {
  const v = num(ms)
  if (v === undefined) return PLACEHOLDER
  return `${v.toFixed(Math.abs(v) < 99.95 ? 1 : 0)} ms`
}

/**
 * Formats packet loss without decimals unless it has some, e.g. 0 -> "0%",
 * 20 -> "20%", 33.33 -> "33.3%".
 */
export function formatLoss(pct: unknown): string {
  const v = num(pct)
  if (v === undefined) return PLACEHOLDER
  return Number.isInteger(v) ? `${v}%` : `${v.toFixed(1)}%`
}

/**
 * The loss series keys that decide whether the internet is down, the same
 * way the backend does: every internet target, or the gateway when there
 * are none.
 */
export function outageKeys(
  targets: Pick<NetQualityTarget, 'key' | 'kind'>[],
): string[] {
  const internet = targets.filter((t) => t.kind === 'internet')
  return (internet.length > 0 ? internet : targets).map(lossKey)
}

export interface Outage {
  /** Unix seconds of the first sample in the outage. */
  start: number
  /** Unix seconds of the first sample after it, or null while it lasts. */
  end: number | null
  /** Unix seconds of the newest sample, for the length of an ongoing one. */
  latest: number
  /**
   * The outage was already going on at the oldest sample in view, so it
   * started earlier than `start`.
   */
  clipped: boolean
}

/**
 * The most recent outage in the rolling buffer: the last run of samples in
 * which every one of the given loss histories was at 100%. Pass the
 * histories of `outageKeys`. A sample time missing from any history counts
 * as neither up nor down. Returns null when there was no outage in view.
 */
export function lastOutage(losses: SeriesHistory[]): Outage | null {
  if (losses.length === 0) return null
  const byTime = new Map<number, number[]>()
  for (const [times, values] of losses) {
    times.forEach((t, i) => {
      const row = byTime.get(t)
      if (row) row.push(values[i])
      else byTime.set(t, [values[i]])
    })
  }
  const samples = [...byTime]
    .filter(([, row]) => row.length === losses.length)
    .map(([t, row]) => ({ t, down: row.every((v) => v >= 100) }))
    .sort((a, b) => a.t - b.t)
  if (samples.length === 0) return null

  // Walk back from the newest sample to the end of the last down run, then
  // to its start.
  let i = samples.length - 1
  while (i >= 0 && !samples[i].down) i--
  if (i < 0) return null
  const endIndex = i + 1
  while (i > 0 && samples[i - 1].down) i--
  return {
    start: samples[i].t,
    end: endIndex < samples.length ? samples[endIndex].t : null,
    latest: samples[samples.length - 1].t,
    clipped: i === 0,
  }
}

/**
 * Describes an outage for the widget, with clock times as formatClock
 * shows them (now in Unix milliseconds):
 *
 * - "Last outage: 09:20:40, lasted 35s"
 * - "Last outage: ended 09:20:40, lasted over 2m" (began before the window)
 * - "Outage since 09:20:40, 35s so far"
 * - "Outage for over 5m" (ongoing, began before the window)
 */
export function describeOutage(o: Outage, now: number): string {
  const clock = (s: number) => formatClock(s * 1000, now)
  const length = formatDuration(Math.max(0, (o.end ?? o.latest) - o.start))
  if (o.end === null) {
    return o.clipped
      ? `Outage for over ${length}`
      : `Outage since ${clock(o.start)}, ${length} so far`
  }
  return o.clipped
    ? `Last outage: ended ${clock(o.end)}, lasted over ${length}`
    : `Last outage: ${clock(o.start)}, lasted ${length}`
}

/**
 * Unix seconds of the newest sample in which the DNS failure counter went
 * up, or null when it did not in view. A drop (pisens restarted) is not a
 * failure.
 */
export function lastDnsFailure([times, values]: SeriesHistory): number | null {
  for (let i = values.length - 1; i > 0; i--) {
    if (values[i] > values[i - 1]) return times[i]
  }
  return null
}
