async function getJSON<T>(url: string, signal?: AbortSignal): Promise<T> {
  const res = await fetch(url, { signal })
  if (!res.ok) {
    // The history endpoints explain a 400 in {"error": "..."}.
    let detail = `${res.status} ${res.statusText}`
    try {
      const body = (await res.json()) as { error?: unknown }
      if (typeof body.error === 'string') detail = body.error
    } catch {
      // Not JSON; the status will do.
    }
    throw new Error(`GET ${url.split('?')[0]}: ${detail}`)
  }
  return (await res.json()) as T
}

/** Every series key in the history database, sorted. */
export async function getSeriesKeys(signal?: AbortSignal): Promise<string[]> {
  const body = await getJSON<{ series: string[] }>('/api/series', signal)
  return body.series ?? []
}

/** The points of one series from /api/history, as parallel arrays. */
export interface HistorySeries {
  /** Start of each bucket, Unix seconds. */
  t: number[]
  avg: number[]
  min: number[]
  max: number[]
}

export interface HistoryResponse {
  /** The table the values came from. */
  resolution: 'raw' | '1m' | '15m'
  /** Bucket size in seconds. */
  step: number
  series: Record<string, HistorySeries>
}

/** Most series /api/history takes in one request. */
export const HISTORY_MAX_SERIES = 10

/**
 * The history of `keys` in [from, to) (Unix seconds). More keys than one
 * request takes are split over several requests; they all get the same
 * buckets since those only depend on the range.
 */
export async function getHistory(
  keys: string[],
  from: number,
  to: number,
  signal?: AbortSignal,
): Promise<HistoryResponse> {
  const chunks: string[][] = []
  for (let i = 0; i < keys.length; i += HISTORY_MAX_SERIES) {
    chunks.push(keys.slice(i, i + HISTORY_MAX_SERIES))
  }
  const parts = await Promise.all(
    chunks.map((chunk) => {
      const q = new URLSearchParams({
        series: chunk.join(','),
        from: String(Math.floor(from)),
        to: String(Math.floor(to)),
      })
      return getJSON<HistoryResponse>(`/api/history?${q}`, signal)
    }),
  )
  const out: HistoryResponse = {
    resolution: parts[0]?.resolution ?? 'raw',
    step: parts[0]?.step ?? 1,
    series: {},
  }
  for (const p of parts) Object.assign(out.series, p.series)
  return out
}

/** One local calendar day from /api/energy/daily. */
export interface EnergyDay {
  /** The Pi's local date, "2026-11-07". */
  date: string
  /** The day's local midnight, Unix seconds. */
  start: number
  /** kWh used that day, null without stored samples. Today's counts so far. */
  kwh: number | null
  /** kwh at the configured price; only there with a price and a kwh. */
  cost?: number
}

export interface EnergyDailyResponse {
  /** Every day in the range, oldest first, ending with today. */
  days: EnergyDay[]
  /** Price per kWh and its currency, only there when a price is set. */
  price?: number
  currency?: string
}

/** The energy used on each of the last `days` days, today included. */
export async function getEnergyDaily(
  days = 30,
  signal?: AbortSignal,
): Promise<EnergyDailyResponse> {
  const body = await getJSON<EnergyDailyResponse>(
    `/api/energy/daily?days=${days}`,
    signal,
  )
  return { ...body, days: body.days ?? [] }
}
