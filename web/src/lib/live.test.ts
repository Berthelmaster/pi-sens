import { get, type Readable } from 'svelte/store'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Alert, AlertSet } from '../types/alerts'
import type { Snapshot } from '../types/snapshot'
import {
  BACKOFF_MAX_MS,
  HIDDEN_PAUSE_MS,
  OFFLINE_AFTER_FAILURES,
  SeriesBuffer,
  backoffDelay,
  createLive,
  type EventSourceLike,
} from './live'

class FakeEventSource implements EventSourceLike {
  onopen: ((ev: Event) => unknown) | null = null
  onerror: ((ev: Event) => unknown) | null = null
  closed = false
  private listeners: { type: string; fn: (ev: MessageEvent) => void }[] = []

  constructor(readonly url: string) {}

  addEventListener(
    type: 'snapshot' | 'alerts',
    fn: (ev: MessageEvent) => void,
  ) {
    this.listeners.push({ type, fn })
  }

  close() {
    this.closed = true
  }

  open() {
    this.onopen?.(new Event('open'))
  }

  fail() {
    this.onerror?.(new Event('error'))
  }

  emit(type: string, data: string) {
    const ev = new MessageEvent(type, { data })
    for (const l of this.listeners) if (l.type === type) l.fn(ev)
  }

  send(snap: Snapshot) {
    this.emit('snapshot', JSON.stringify(snap))
  }

  sendAlerts(set: AlertSet) {
    this.emit('alerts', JSON.stringify(set))
  }
}

class FakeDocument extends EventTarget {
  hidden = false

  setHidden(hidden: boolean) {
    this.hidden = hidden
    this.dispatchEvent(new Event('visibilitychange'))
  }
}

function snap(t: number, series: Record<string, number> = {}): Snapshot {
  return { timestamp: new Date(t).toISOString(), collectors: {}, series }
}

function firing(rule: string): Alert {
  return {
    rule,
    series: 'temp.cpu_c',
    severity: 'critical',
    op: '>',
    threshold: 82,
    for_s: 30,
    state: 'firing',
    value: 83.4,
    pending_at: '2026-11-07T09:20:12Z',
    firing_at: '2026-11-07T09:20:42Z',
  }
}

function alertSet(version: number, ...rules: string[]): AlertSet {
  return { version, firing: rules.map(firing), resolved: [] }
}

/** Answers each URL with its own pending response. */
function fetchByUrl() {
  const resolvers = new Map<string, (r: Response) => void>()
  const fn = vi.fn(
    (url: string) =>
      new Promise<Response>((r) => {
        resolvers.set(url, r)
      }),
  )
  const respond = (url: string, res: Response) => resolvers.get(url)!(res)
  return { fn, respond }
}

/** Keeps a store subscribed and returns its current value on demand. */
function watch<T>(store: Readable<T>) {
  let value!: T
  const unsubscribe = store.subscribe((v) => (value = v))
  return { get: () => value, unsubscribe }
}

const T0 = Date.parse('2026-11-07T10:00:00Z')

describe('SeriesBuffer', () => {
  it('never holds more than the window', () => {
    const buf = new SeriesBuffer(10_000)
    for (let i = 0; i <= 100; i++) {
      buf.push(T0 + i * 1000, { 'cpu.usage_pct': i })
      const [times] = buf.history('cpu.usage_pct')
      expect(times.at(-1)! - times[0]).toBeLessThanOrEqual(10)
      expect(buf.size).toBeLessThanOrEqual(11)
    }
    expect(buf.history('cpu.usage_pct')).toEqual([
      Array.from({ length: 11 }, (_, i) => (T0 + (90 + i) * 1000) / 1000),
      Array.from({ length: 11 }, (_, i) => 90 + i),
    ])
  })

  it('drops everything after a long gap', () => {
    const buf = new SeriesBuffer(10_000)
    buf.push(T0, { a: 1 })
    buf.push(T0 + 1000, { a: 2 })
    buf.push(T0 + 60_000, { a: 3 })
    expect(buf.history('a')).toEqual([[(T0 + 60_000) / 1000], [3]])
  })

  it('leaves out samples without the key', () => {
    const buf = new SeriesBuffer()
    buf.push(T0, { a: 1, b: 10 })
    buf.push(T0 + 1000, { b: 11 })
    buf.push(T0 + 2000, { a: 3 })
    expect(buf.history('a')).toEqual([
      [T0 / 1000, (T0 + 2000) / 1000],
      [1, 3],
    ])
    expect(buf.history('missing')).toEqual([[], []])
  })

  it('replaces a sample with the same time', () => {
    const buf = new SeriesBuffer()
    buf.push(T0, { a: 1 })
    buf.push(T0, { a: 2 })
    expect(buf.history('a')).toEqual([[T0 / 1000], [2]])
  })

  it('starts over when the clock goes back', () => {
    const buf = new SeriesBuffer()
    buf.push(T0, { a: 1 })
    buf.push(T0 + 1000, { a: 2 })
    buf.push(T0 - 3_600_000, { a: 3 })
    expect(buf.history('a')).toEqual([[(T0 - 3_600_000) / 1000], [3]])
  })
})

describe('backoffDelay', () => {
  it('doubles up to the cap', () => {
    expect([1, 2, 3, 4, 5, 6, 7, 20].map((n) => backoffDelay(n))).toEqual([
      1000, 2000, 4000, 8000, 16_000, 30_000, 30_000, 30_000,
    ])
  })

  it('never exceeds the cap with jitter', () => {
    expect(backoffDelay(6, 0.999)).toBeLessThanOrEqual(BACKOFF_MAX_MS)
    expect(backoffDelay(1, 0)).toBe(800)
    expect(backoffDelay(1, 0.999)).toBeCloseTo(1200, 0)
  })
})

describe('createLive', () => {
  let sources: FakeEventSource[]
  let fetchMock: ReturnType<typeof vi.fn>
  let doc: FakeDocument
  let win: EventTarget

  const last = () => sources.at(-1)!

  function setup() {
    return createLive({
      openStream: (url) => {
        const es = new FakeEventSource(url)
        sources.push(es)
        return es
      },
      fetch: fetchMock as unknown as typeof fetch,
      random: () => 0.5,
      document: doc,
      window: win,
    })
  }

  beforeEach(() => {
    vi.useFakeTimers({ now: T0 })
    sources = []
    // Never resolves unless a test says so.
    fetchMock = vi.fn(() => new Promise<Response>(() => {}))
    doc = new FakeDocument()
    win = new EventTarget()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('stays idle until subscribed and closes after the last unsubscribe', () => {
    const live = setup()
    expect(sources).toHaveLength(0)

    const a = watch(live.snapshot)
    const b = watch(live.connection)
    const c = watch(live.seriesHistory('cpu.usage_pct'))
    expect(sources).toHaveLength(1)
    expect(sources[0].url).toBe('/api/stream')
    expect(fetchMock).toHaveBeenCalledWith('/api/metrics', expect.anything())

    a.unsubscribe()
    b.unsubscribe()
    expect(sources[0].closed).toBe(false)
    c.unsubscribe()
    expect(sources[0].closed).toBe(true)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('goes live and records snapshots', () => {
    const live = setup()
    const conn = watch(live.connection)
    const latest = watch(live.snapshot)
    const cpu = watch(live.seriesHistory('cpu.usage_pct'))
    expect(conn.get().state).toBe('connecting')
    expect(latest.get()).toBeNull()

    last().open()
    expect(conn.get().state).toBe('live')

    vi.advanceTimersByTime(500)
    last().send(snap(T0, { 'cpu.usage_pct': 12 }))
    last().send(snap(T0 + 2000, { 'cpu.usage_pct': 14 }))
    expect(latest.get()?.series['cpu.usage_pct']).toBe(14)
    expect(conn.get().lastUpdate).toBe(T0 + 500)
    expect(cpu.get()).toEqual([
      [T0 / 1000, (T0 + 2000) / 1000],
      [12, 14],
    ])
  })

  it('reconnects with backoff after the backend goes away', () => {
    const live = setup()
    const conn = watch(live.connection)
    last().open()
    last().send(snap(T0, { a: 1 }))

    // Backend killed: every attempt fails and the delay doubles.
    let expected = 1000
    for (let i = 1; i <= 7; i++) {
      const es = last()
      es.fail()
      expect(es.closed).toBe(true)
      expect(conn.get().state).toBe(
        i >= OFFLINE_AFTER_FAILURES ? 'offline' : 'reconnecting',
      )
      expect(conn.get().retryAt).toBe(Date.now() + expected)

      vi.advanceTimersByTime(expected - 1)
      expect(sources).toHaveLength(i)
      vi.advanceTimersByTime(1)
      expect(sources).toHaveLength(i + 1)
      expected = Math.min(expected * 2, BACKOFF_MAX_MS)
    }

    // Backend back: live again, and the next failure starts from 1 s.
    last().open()
    expect(conn.get()).toMatchObject({ state: 'live', retryAt: null })
    last().fail()
    expect(conn.get().retryAt).toBe(Date.now() + 1000)
  })

  it('ignores events from a stream it has closed', () => {
    const live = setup()
    const conn = watch(live.connection)
    const latest = watch(live.snapshot)
    const old = last()
    old.fail()
    vi.advanceTimersByTime(1000)
    old.open()
    old.send(snap(T0))
    expect(conn.get().state).toBe('reconnecting')
    expect(latest.get()).toBeNull()
  })

  it('skips the snapshot a reconnect repeats', () => {
    const live = setup()
    const a = watch(live.seriesHistory('a'))
    last().open()
    last().send(snap(T0, { a: 1 }))
    last().fail()
    vi.advanceTimersByTime(1000)
    last().open()
    last().send(snap(T0, { a: 1 }))
    last().send(snap(T0 + 2000, { a: 2 }))
    expect(a.get()[1]).toEqual([1, 2])
  })

  it('keeps the history within the window', () => {
    const live = setup()
    const a = watch(live.seriesHistory('a'))
    last().open()
    for (let i = 0; i < 1000; i++) last().send(snap(T0 + i * 1000, { a: i }))
    const [times, values] = a.get()
    expect(times).toHaveLength(301)
    expect(times.at(-1)! - times[0]).toBe(300)
    expect(values[0]).toBe(699)
  })

  it('uses /api/metrics until the stream delivers', async () => {
    const { fn, respond } = fetchByUrl()
    fetchMock = fn
    const live = setup()
    const latest = watch(live.snapshot)

    respond('/api/metrics', new Response(JSON.stringify(snap(T0, { a: 1 }))))
    await vi.waitFor(() => expect(latest.get()).not.toBeNull())
    expect(latest.get()?.timestamp).toBe(new Date(T0).toISOString())
  })

  it('lets the stream win over a slow /api/metrics', async () => {
    const { fn, respond } = fetchByUrl()
    fetchMock = fn
    const live = setup()
    const latest = watch(live.snapshot)
    last().send(snap(T0 + 2000, { a: 2 }))

    respond('/api/metrics', new Response(JSON.stringify(snap(T0, { a: 1 }))))
    await vi.advanceTimersByTimeAsync(0)
    expect(latest.get()?.series.a).toBe(2)
  })

  it('loads the alerts from /api/alerts', async () => {
    const { fn, respond } = fetchByUrl()
    fetchMock = fn
    const live = setup()
    const set = watch(live.alerts)
    expect(fetchMock).toHaveBeenCalledWith('/api/alerts', expect.anything())
    expect(set.get()).toBeNull()

    respond('/api/alerts', new Response(JSON.stringify(alertSet(3, 'hot'))))
    await vi.waitFor(() => expect(set.get()).not.toBeNull())
    expect(set.get()?.firing.map((a) => a.rule)).toEqual(['hot'])
  })

  it('stays without alerts when the backend has them turned off', async () => {
    const { fn, respond } = fetchByUrl()
    fetchMock = fn
    const live = setup()
    const set = watch(live.alerts)
    respond(
      '/api/alerts',
      new Response('{"error":"not found"}', { status: 404 }),
    )
    await vi.advanceTimersByTimeAsync(0)
    expect(set.get()).toBeNull()
  })

  it('follows alerts as they fire and resolve', () => {
    const live = setup()
    const set = watch(live.alerts)
    last().open()
    last().sendAlerts(alertSet(1))
    expect(set.get()?.firing).toEqual([])

    last().sendAlerts(alertSet(2, 'hot', 'full'))
    expect(set.get()?.firing).toHaveLength(2)

    last().sendAlerts({ ...alertSet(3, 'full'), resolved: [firing('hot')] })
    expect(set.get()?.firing.map((a) => a.rule)).toEqual(['full'])
    expect(set.get()?.resolved.map((a) => a.rule)).toEqual(['hot'])
  })

  it('takes the alerts a restarted backend sends', () => {
    const live = setup()
    const set = watch(live.alerts)
    last().open()
    last().sendAlerts(alertSet(7, 'hot'))
    last().fail()
    vi.advanceTimersByTime(1000)
    last().open()
    // Versions start over with the backend.
    last().sendAlerts(alertSet(0))
    expect(set.get()?.firing).toEqual([])
  })

  it('clears the alerts when a reconnected backend has them turned off', async () => {
    const { fn, respond } = fetchByUrl()
    fetchMock = fn
    const live = setup()
    const set = watch(live.alerts)
    const alertCalls = () =>
      fetchMock.mock.calls.filter(([url]) => url === '/api/alerts').length
    last().open()
    last().sendAlerts(alertSet(7, 'hot'))
    expect(alertCalls()).toBe(1)

    // Restarted without a rules file: the new stream never sends alerts.
    last().fail()
    vi.advanceTimersByTime(1000)
    last().open()
    expect(alertCalls()).toBe(2)
    respond(
      '/api/alerts',
      new Response('{"error":"not found"}', { status: 404 }),
    )
    await vi.waitFor(() => expect(set.get()).toBeNull())
  })

  it('takes the alerts /api/alerts returns after a reconnect', async () => {
    const { fn, respond } = fetchByUrl()
    fetchMock = fn
    const live = setup()
    const set = watch(live.alerts)
    last().open()
    last().sendAlerts(alertSet(7, 'hot'))
    last().fail()
    vi.advanceTimersByTime(1000)
    last().open()

    respond('/api/alerts', new Response(JSON.stringify(alertSet(0, 'full'))))
    await vi.waitFor(() =>
      expect(set.get()?.firing.map((a) => a.rule)).toEqual(['full']),
    )
  })

  it('lets the reconnected stream win over the alerts recheck', async () => {
    const { fn, respond } = fetchByUrl()
    fetchMock = fn
    const live = setup()
    const set = watch(live.alerts)
    last().open()
    last().sendAlerts(alertSet(7, 'hot'))
    last().fail()
    vi.advanceTimersByTime(1000)
    last().open()
    last().sendAlerts(alertSet(1, 'full'))

    respond(
      '/api/alerts',
      new Response('{"error":"not found"}', { status: 404 }),
    )
    await vi.advanceTimersByTimeAsync(0)
    expect(set.get()?.firing.map((a) => a.rule)).toEqual(['full'])
  })

  it('rechecks the alerts when resubscribed', async () => {
    const { fn, respond } = fetchByUrl()
    fetchMock = fn
    const live = setup()
    let set = watch(live.alerts)
    last().open()
    last().sendAlerts(alertSet(7, 'hot'))
    set.unsubscribe()

    set = watch(live.alerts)
    expect(set.get()?.firing).toHaveLength(1)
    last().open()
    respond(
      '/api/alerts',
      new Response('{"error":"not found"}', { status: 404 }),
    )
    await vi.waitFor(() => expect(set.get()).toBeNull())
  })

  it('lets the stream win over a slow /api/alerts', async () => {
    const { fn, respond } = fetchByUrl()
    fetchMock = fn
    const live = setup()
    const set = watch(live.alerts)
    last().sendAlerts(alertSet(5))

    respond('/api/alerts', new Response(JSON.stringify(alertSet(4, 'hot'))))
    await vi.advanceTimersByTimeAsync(0)
    expect(set.get()?.version).toBe(5)
  })

  it('ignores a bad alerts event', () => {
    const live = setup()
    const set = watch(live.alerts)
    last().sendAlerts(alertSet(1, 'hot'))
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    last().emit('alerts', '{nope')
    warn.mockRestore()
    expect(set.get()?.firing).toHaveLength(1)
  })

  it('keeps the connection open for alerts alone', () => {
    const live = setup()
    const set = watch(live.alerts)
    expect(sources).toHaveLength(1)
    set.unsubscribe()
    expect(sources[0].closed).toBe(true)
  })

  it('pauses while the tab is hidden and resumes when visible', () => {
    const live = setup()
    const conn = watch(live.connection)
    last().open()

    // A quick look at another tab keeps the stream.
    doc.setHidden(true)
    vi.advanceTimersByTime(HIDDEN_PAUSE_MS - 1)
    doc.setHidden(false)
    vi.advanceTimersByTime(HIDDEN_PAUSE_MS)
    expect(sources).toHaveLength(1)
    expect(last().closed).toBe(false)

    doc.setHidden(true)
    vi.advanceTimersByTime(HIDDEN_PAUSE_MS)
    expect(last().closed).toBe(true)
    expect(vi.getTimerCount()).toBe(0)

    doc.setHidden(false)
    expect(sources).toHaveLength(2)
    expect(conn.get().state).toBe('reconnecting')
    last().open()
    expect(conn.get().state).toBe('live')
  })

  it('does not retry while paused', () => {
    const live = setup()
    watch(live.connection)
    doc.setHidden(true)
    // Retries go on during the grace period, then stop with the pause.
    last().fail()
    vi.advanceTimersByTime(HIDDEN_PAUSE_MS)
    const before = sources.length
    expect(last().closed).toBe(true)
    vi.advanceTimersByTime(10 * BACKOFF_MAX_MS)
    expect(sources).toHaveLength(before)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('retries at once when the browser comes back online', () => {
    const live = setup()
    watch(live.connection)
    for (let i = 0; i < 5; i++) {
      last().fail()
      vi.runOnlyPendingTimers()
    }
    last().fail()
    const before = sources.length
    win.dispatchEvent(new Event('online'))
    expect(sources).toHaveLength(before + 1)
  })

  it('removes its listeners when stopped', () => {
    const live = setup()
    const conn = watch(live.connection)
    conn.unsubscribe()
    doc.setHidden(true)
    doc.setHidden(false)
    win.dispatchEvent(new Event('online'))
    expect(sources).toHaveLength(1)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('works with get()', () => {
    const live = setup()
    expect(get(live.connection).state).toBe('connecting')
    expect(sources[0].closed).toBe(true)
  })
})
