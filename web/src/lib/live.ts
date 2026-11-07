// Live metrics from the backend.
//
// One EventSource on /api/stream feeds three stores: the latest snapshot, the
// connection state for the header indicator, and a rolling buffer of recent
// series values for sparklines. The connection opens when the first component
// subscribes to any of them and closes when the last one unsubscribes, so
// nothing keeps running once the dashboard is gone.
//
// The browser's own EventSource retry is not used: on any error the stream is
// closed and reopened with exponential backoff, which also covers the cases
// where the browser gives up (a 503 or a proxy error page while the backend
// restarts).

import { derived, readable, writable, type Readable } from 'svelte/store'
import { parseTimestamp, type Snapshot } from '../types/snapshot'

export type ConnectionState = 'connecting' | 'live' | 'reconnecting' | 'offline'

export interface Connection {
  state: ConnectionState
  /** Date.now() when the last snapshot arrived, or null before the first. */
  lastUpdate: number | null
  /** Date.now() of the next reconnect attempt while waiting for one. */
  retryAt: number | null
}

/** Unix seconds (uPlot's x values) and the series values at those times. */
export type SeriesHistory = [timestamps: number[], values: number[]]

/** How much series history is kept for sparklines. */
export const HISTORY_WINDOW_MS = 5 * 60 * 1000
/** First reconnect delay; it doubles on every failed attempt. */
export const BACKOFF_BASE_MS = 1000
/** Upper bound for the reconnect delay. */
export const BACKOFF_MAX_MS = 30 * 1000
/**
 * Failed attempts in a row before the state goes from reconnecting to
 * offline. With the delays above that is about 15 seconds without a backend.
 */
export const OFFLINE_AFTER_FAILURES = 4
/** How long a hidden tab keeps the stream open before pausing it. */
export const HIDDEN_PAUSE_MS = 30 * 1000

interface Sample {
  /** Unix milliseconds of the snapshot. */
  t: number
  series: Record<string, number>
}

/**
 * Keeps the series of recent snapshots, dropping anything older than windowMs
 * before the newest sample. Times are the backend's snapshot timestamps, so a
 * client clock that is off does not matter.
 */
export class SeriesBuffer {
  private samples: Sample[] = []

  constructor(readonly windowMs: number = HISTORY_WINDOW_MS) {}

  get size(): number {
    return this.samples.length
  }

  /** Unix milliseconds of the newest sample, or null when empty. */
  get newest(): number | null {
    return this.samples.at(-1)?.t ?? null
  }

  /**
   * Adds a sample taken at t (Unix milliseconds). A sample at the same time as
   * the newest one replaces it. One older than the newest means the backend
   * clock went back (the Pi has no battery backed clock, so this happens when
   * NTP catches up after boot); the old samples would then never age out, so
   * the buffer starts over.
   */
  push(t: number, series: Record<string, number>): void {
    const newest = this.newest
    if (newest !== null && t < newest) {
      this.samples = []
    } else if (newest !== null && t === newest) {
      this.samples.pop()
    }
    this.samples.push({ t, series })

    const cutoff = t - this.windowMs
    let stale = 0
    while (stale < this.samples.length && this.samples[stale].t < cutoff) {
      stale++
    }
    if (stale > 0) this.samples.splice(0, stale)
  }

  clear(): void {
    this.samples = []
  }

  /** Times and values of every sample that has key, oldest first. */
  history(key: string): SeriesHistory {
    const times: number[] = []
    const values: number[] = []
    for (const s of this.samples) {
      const v = s.series[key]
      if (typeof v === 'number') {
        times.push(s.t / 1000)
        values.push(v)
      }
    }
    return [times, values]
  }
}

/** The parts of EventSource the store uses, so tests can fake it. */
export interface EventSourceLike {
  onopen: ((ev: Event) => unknown) | null
  onerror: ((ev: Event) => unknown) | null
  addEventListener(type: 'snapshot', listener: (ev: MessageEvent) => void): void
  close(): void
}

type Listener = () => void

interface EventTargetLike {
  addEventListener(type: string, listener: Listener): void
  removeEventListener(type: string, listener: Listener): void
}

export interface LiveOptions {
  streamUrl?: string
  metricsUrl?: string
  windowMs?: number
  openStream?: (url: string) => EventSourceLike
  fetch?: (url: string, init?: RequestInit) => Promise<Response>
  now?: () => number
  /** Spreads out reconnects; returns a number in [0, 1). */
  random?: () => number
  /** For visibilitychange; leave undefined to never pause. */
  document?: EventTargetLike & { readonly hidden: boolean }
  /** For the online event, which retries right away. */
  window?: EventTargetLike
}

export interface Live {
  /** The latest snapshot, or null until the first one arrives. */
  snapshot: Readable<Snapshot | null>
  connection: Readable<Connection>
  /** Recent history of one series key, e.g. seriesHistory('cpu.usage_pct'). */
  seriesHistory(key: string): Readable<SeriesHistory>
}

/** Reconnect delay after the given number of failed attempts in a row. */
export function backoffDelay(failures: number, random = 0.5): number {
  const exp = BACKOFF_BASE_MS * 2 ** Math.max(0, failures - 1)
  // ±20% jitter so several open tabs do not reconnect in lockstep.
  return Math.min(BACKOFF_MAX_MS, exp * (0.8 + 0.4 * random))
}

export function createLive(opts: LiveOptions = {}): Live {
  const {
    streamUrl = '/api/stream',
    metricsUrl = '/api/metrics',
    windowMs = HISTORY_WINDOW_MS,
    openStream = (url) => new EventSource(url),
    fetch: fetchFn = (url, init) => fetch(url, init),
    now = Date.now,
    random = Math.random,
    document: doc = typeof document === 'undefined' ? undefined : document,
    window: win = typeof window === 'undefined' ? undefined : window,
  } = opts

  const initialConnection: Connection = {
    state: 'connecting',
    lastUpdate: null,
    retryAt: null,
  }
  const latest = writable<Snapshot | null>(null)
  const connection = writable(initialConnection)
  const buffer = new SeriesBuffer(windowMs)
  const bufferStore = writable(buffer)

  let latestTime: number | null = null
  let haveSnapshot = false

  let source: EventSourceLike | null = null
  let failures = 0
  let retryTimer: ReturnType<typeof setTimeout> | undefined
  let pauseTimer: ReturnType<typeof setTimeout> | undefined
  let paused = false
  let initialFetch: AbortController | null = null

  function setState(state: ConnectionState, retryAt: number | null = null) {
    connection.update((c) => ({ ...c, state, retryAt }))
  }

  function accept(snap: Snapshot) {
    const t = parseTimestamp(snap.timestamp)
    // Streams resend the latest snapshot on connect; skip ones already seen.
    if (Number.isFinite(t) && latestTime !== null && t === latestTime) return

    haveSnapshot = true
    latest.set(snap)
    connection.update((c) => ({ ...c, lastUpdate: now() }))
    if (Number.isFinite(t)) {
      latestTime = t
      buffer.push(t, snap.series ?? {})
      bufferStore.set(buffer)
    }
  }

  function onSnapshot(ev: MessageEvent) {
    let snap: Snapshot
    try {
      snap = JSON.parse(ev.data as string) as Snapshot
    } catch (err) {
      console.warn('pisens: bad snapshot event', err)
      return
    }
    accept(snap)
  }

  function connect() {
    disconnect()
    const es = openStream(streamUrl)
    source = es
    es.onopen = () => {
      if (source !== es) return
      failures = 0
      setState('live')
    }
    es.addEventListener('snapshot', (ev) => {
      if (source === es) onSnapshot(ev)
    })
    es.onerror = () => {
      if (source !== es) return
      es.close()
      source = null
      failures++
      scheduleRetry()
    }
  }

  function scheduleRetry() {
    const delay = backoffDelay(failures, random())
    const state =
      failures >= OFFLINE_AFTER_FAILURES ? 'offline' : 'reconnecting'
    setState(state, now() + delay)
    retryTimer = setTimeout(connect, delay)
  }

  function disconnect() {
    clearTimeout(retryTimer)
    retryTimer = undefined
    if (source) {
      source.close()
      source = null
    }
  }

  function loadInitial() {
    // The stream sends the latest snapshot as soon as it connects, but a
    // plain request can be quicker, and still works behind a proxy that
    // buffers event streams.
    const ctrl = new AbortController()
    initialFetch = ctrl
    fetchFn(metricsUrl, { signal: ctrl.signal })
      .then(async (res) => {
        if (!res.ok) return
        const snap = (await res.json()) as Snapshot
        if (!haveSnapshot && !ctrl.signal.aborted) accept(snap)
      })
      .catch(() => {
        // The stream reports connection problems; nothing to add here.
      })
      .finally(() => {
        if (initialFetch === ctrl) initialFetch = null
      })
  }

  function onVisibilityChange() {
    if (!doc) return
    clearTimeout(pauseTimer)
    pauseTimer = undefined
    if (doc.hidden) {
      pauseTimer = setTimeout(pause, HIDDEN_PAUSE_MS)
    } else if (paused) {
      paused = false
      failures = 0
      setState('reconnecting')
      connect()
    }
  }

  function pause() {
    pauseTimer = undefined
    paused = true
    disconnect()
  }

  function onOnline() {
    // Back on the network: try now instead of waiting out the backoff.
    if (retryTimer !== undefined) {
      failures = 0
      connect()
    }
  }

  function start() {
    paused = false
    failures = 0
    setState(haveSnapshot ? 'reconnecting' : 'connecting')
    if (!haveSnapshot) loadInitial()
    connect()
    doc?.addEventListener('visibilitychange', onVisibilityChange)
    win?.addEventListener('online', onOnline)
    if (doc?.hidden) onVisibilityChange()
  }

  function stop() {
    doc?.removeEventListener('visibilitychange', onVisibilityChange)
    win?.removeEventListener('online', onOnline)
    clearTimeout(pauseTimer)
    pauseTimer = undefined
    initialFetch?.abort()
    initialFetch = null
    disconnect()
  }

  // Every public store counts as a user of the connection.
  let users = 0
  function shared<T>(inner: Readable<T>, initial: T): Readable<T> {
    return readable(initial, (set) => {
      if (users++ === 0) start()
      const unsubscribe = inner.subscribe(set)
      return () => {
        unsubscribe()
        if (--users === 0) stop()
      }
    })
  }

  const history = shared(bufferStore, buffer)
  const historyByKey = new Map<string, Readable<SeriesHistory>>()

  return {
    snapshot: shared(latest, null),
    connection: shared(connection, initialConnection),
    seriesHistory(key) {
      let store = historyByKey.get(key)
      if (!store) {
        store = derived(history, (b) => b.history(key))
        historyByKey.set(key, store)
      }
      return store
    },
  }
}

const live = createLive()

/** The latest snapshot, or null until the first one arrives. */
export const snapshot = live.snapshot
/** Stream state and time of the last update, for the header indicator. */
export const connection = live.connection
/**
 * The last five minutes of one series as [unix seconds[], values[]], ready
 * for uPlot. Samples without the key are left out.
 */
export const seriesHistory = live.seriesHistory
