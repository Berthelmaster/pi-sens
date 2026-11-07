/**
 * The envelope the backend sends from /api/metrics and as each `snapshot`
 * event on /api/stream (metrics.Snapshot in Go).
 *
 * Collector sections are typed by the widget that reads them, in its own
 * file under types/, e.g. types/cpu.ts. Narrow a section with
 * isCollectorError before using it.
 */
export interface Snapshot {
  /** RFC 3339 time of the sample, with up to nanosecond precision. */
  timestamp: string
  /** One section per collector, keyed by collector name. */
  collectors: Record<string, unknown>
  /**
   * Numeric values for charts, keyed `area.thing_unit`, e.g. `cpu.usage_pct`.
   * Only collectors that succeeded contribute.
   */
  series: Record<string, number>
}

/** The section a collector gets in place of its data when it fails. */
export interface CollectorError {
  error: string
}

export function isCollectorError(section: unknown): section is CollectorError {
  return (
    typeof section === 'object' &&
    section !== null &&
    typeof (section as { error?: unknown }).error === 'string'
  )
}

/**
 * Parses a snapshot timestamp into Unix milliseconds, or NaN when it is not a
 * valid time. Go writes up to nine fractional digits, which Date.parse is
 * only guaranteed to handle as three, so the rest is cut off first.
 */
export function parseTimestamp(ts: string): number {
  return Date.parse(ts.replace(/(\.\d{3})\d+/, '$1'))
}
