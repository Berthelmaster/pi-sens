import { describe, expect, it, vi } from 'vitest'
import { SKIP_WAITING_MESSAGE as WORKER_SKIP_WAITING } from '../sw/strategy'
import {
  RELOADED_FOR_KEY,
  SKIP_WAITING_MESSAGE,
  TAKEOVER_TIMEOUT_MS,
  UPDATE_CHECK_INTERVAL_MS,
  isNewBuild,
  pageBuild,
  serverBuild,
  watchForUpdates,
  type Registration,
  type UpdateHost,
} from './update'

describe('isNewBuild', () => {
  it('is true when the server has a different frontend', () => {
    expect(isNewBuild('aaa', 'bbb')).toBe(true)
  })

  it('is false for the same build', () => {
    expect(isNewBuild('aaa', 'aaa')).toBe(false)
  })

  it('is false when either side does not know its build', () => {
    expect(isNewBuild(null, 'bbb')).toBe(false)
    expect(isNewBuild('aaa', null)).toBe(false)
    expect(isNewBuild(null, null)).toBe(false)
  })

  it('does not offer the same reload twice', () => {
    // Reloaded for bbb, but the page is still aaa.
    expect(isNewBuild('aaa', 'bbb', 'bbb')).toBe(false)
    // A later deploy is offered again.
    expect(isNewBuild('aaa', 'ccc', 'bbb')).toBe(true)
  })
})

describe('serverBuild', () => {
  it('reads the web build from /api/health', () => {
    expect(serverBuild({ status: 'ok', version: 'v1', web: 'abc' })).toBe('abc')
  })

  it('is null for a server without build IDs or a body that is not one', () => {
    expect(serverBuild({ status: 'ok', version: 'v1' })).toBeNull()
    expect(serverBuild({ web: '' })).toBeNull()
    expect(serverBuild({ web: 42 })).toBeNull()
    expect(serverBuild(null)).toBeNull()
    expect(serverBuild('abc')).toBeNull()
  })
})

describe('pageBuild', () => {
  const doc = (content: string | null) => ({
    querySelector: vi.fn(() =>
      content === null ? null : ({ content } as HTMLMetaElement),
    ),
  })

  it('reads the meta tag the build adds', () => {
    const d = doc('abc')
    expect(pageBuild(d)).toBe('abc')
    expect(d.querySelector).toHaveBeenCalledWith('meta[name="pisens-build"]')
  })

  it('is null on the dev server', () => {
    expect(pageBuild(doc(null))).toBeNull()
    expect(pageBuild(doc(''))).toBeNull()
  })
})

it('uses the same skip waiting message as the worker', () => {
  expect(SKIP_WAITING_MESSAGE).toBe(WORKER_SKIP_WAITING)
})

/** A fake window, with control over time, visibility and the network. */
function fakeHost(health: unknown = { status: 'ok', web: 'old' }) {
  const listeners = new Map<string, Set<() => void>>()
  const storage = new Map<string, string>()
  const intervals: (() => void)[] = []
  const timeouts: (() => void)[] = []
  const state = { health, visibility: 'visible' as DocumentVisibilityState }

  const host = {
    document: {
      get visibilityState() {
        return state.visibility
      },
      addEventListener: (type: string, fn: () => void) => {
        if (!listeners.has(type)) listeners.set(type, new Set())
        listeners.get(type)!.add(fn)
      },
      removeEventListener: (type: string, fn: () => void) => {
        listeners.get(type)?.delete(fn)
      },
    },
    fetch: vi.fn(async () => {
      if (state.health instanceof Error) throw state.health
      return new Response(JSON.stringify(state.health))
    }),
    reload: vi.fn(),
    setInterval: vi.fn((fn: () => void) => intervals.push(fn)),
    clearInterval: vi.fn(),
    setTimeout: vi.fn((fn: () => void) => timeouts.push(fn)),
    storage: {
      getItem: (k: string) => storage.get(k) ?? null,
      setItem: (k: string, v: string) => void storage.set(k, v),
    },
  } as unknown as UpdateHost & {
    fetch: ReturnType<typeof vi.fn>
    reload: ReturnType<typeof vi.fn>
    setInterval: ReturnType<typeof vi.fn>
    clearInterval: ReturnType<typeof vi.fn>
  }

  return {
    host,
    state,
    storage,
    timeouts,
    /** Puts the page in the background and back. */
    async returnToPage() {
      state.visibility = 'hidden'
      listeners.get('visibilitychange')?.forEach((fn) => fn())
      state.visibility = 'visible'
      listeners.get('visibilitychange')?.forEach((fn) => fn())
      await flush()
    },
    /** Lets the periodic check run once. */
    async tick() {
      intervals.forEach((fn) => fn())
      await flush()
    },
  }
}

async function flush() {
  for (let i = 0; i < 5; i++) await Promise.resolve()
  await new Promise((r) => setTimeout(r, 0))
}

describe('watchForUpdates without a service worker', () => {
  it('offers a reload when a new build is deployed and the page comes back', async () => {
    const f = fakeHost()
    const onReady = vi.fn()
    watchForUpdates(f.host, 'old', null, onReady)

    await f.returnToPage()
    expect(onReady).not.toHaveBeenCalledWith(true)

    f.state.health = { status: 'ok', web: 'new' }
    await f.returnToPage()
    expect(onReady).toHaveBeenLastCalledWith(true)
    expect(f.host.fetch).toHaveBeenCalledWith('/api/health', {
      cache: 'no-store',
    })
  })

  it('checks every 30 minutes', async () => {
    const f = fakeHost({ status: 'ok', web: 'new' })
    const onReady = vi.fn()
    watchForUpdates(f.host, 'old', null, onReady)
    expect(f.host.setInterval).toHaveBeenCalledWith(
      expect.any(Function),
      UPDATE_CHECK_INTERVAL_MS,
    )
    expect(UPDATE_CHECK_INTERVAL_MS).toBe(30 * 60 * 1000)

    await f.tick()
    expect(onReady).toHaveBeenLastCalledWith(true)
  })

  it('reloads once and remembers what for', async () => {
    const f = fakeHost({ status: 'ok', web: 'new' })
    const w = watchForUpdates(f.host, 'old', null, vi.fn())
    await w.check()

    w.reload()
    w.reload()
    expect(f.host.reload).toHaveBeenCalledTimes(1)
    expect(f.storage.get(RELOADED_FOR_KEY)).toBe('new')
  })

  it('does not offer the reload again if it brought back the old page', async () => {
    const f = fakeHost({ status: 'ok', web: 'new' })
    f.storage.set(RELOADED_FOR_KEY, 'new')
    const onReady = vi.fn()
    const w = watchForUpdates(f.host, 'old', null, onReady)

    await w.check()
    expect(onReady).not.toHaveBeenCalled()
  })

  it('hides the offer when the server is back on the page build', async () => {
    const f = fakeHost({ status: 'ok', web: 'new' })
    const onReady = vi.fn()
    const w = watchForUpdates(f.host, 'old', null, onReady)
    await w.check()
    f.state.health = { status: 'ok', web: 'old' }
    await w.check()
    expect(onReady).toHaveBeenLastCalledWith(false)
  })

  it('stays quiet when the Pi cannot be reached', async () => {
    const f = fakeHost(new TypeError('Failed to fetch'))
    const onReady = vi.fn()
    const w = watchForUpdates(f.host, 'old', null, onReady)
    await w.check()
    expect(onReady).not.toHaveBeenCalled()
  })

  it('never checks on the dev server', async () => {
    const f = fakeHost({ status: 'ok', web: 'new' })
    const w = watchForUpdates(f.host, null, null, vi.fn())
    await w.check()
    expect(f.host.fetch).not.toHaveBeenCalled()
  })

  it('stops checking', async () => {
    const f = fakeHost({ status: 'ok', web: 'new' })
    const w = watchForUpdates(f.host, 'old', null, vi.fn())
    w.stop()
    await f.returnToPage()
    expect(f.host.fetch).not.toHaveBeenCalled()
    expect(f.host.clearInterval).toHaveBeenCalled()
  })
})

/** A fake worker that records messages and fires state changes. */
function fakeWorker(state: ServiceWorkerState = 'installing') {
  const listeners: (() => void)[] = []
  return {
    state,
    postMessage: vi.fn(),
    addEventListener: (_: string, fn: () => void) => listeners.push(fn),
    become(s: ServiceWorkerState) {
      this.state = s
      listeners.forEach((fn) => fn())
    },
  }
}

function fakeRegistration() {
  const listeners: (() => void)[] = []
  const reg = {
    installing: null as ReturnType<typeof fakeWorker> | null,
    waiting: null as ReturnType<typeof fakeWorker> | null,
    update: vi.fn(async () => {}),
    addEventListener: (_: string, fn: () => void) => listeners.push(fn),
    /** A new worker starts installing and ends up waiting. */
    install() {
      const worker = fakeWorker()
      reg.installing = worker
      listeners.forEach((fn) => fn())
      reg.installing = null
      reg.waiting = worker
      worker.become('installed')
      return worker
    },
  }
  return reg
}

type Container = UpdateHost['serviceWorker']

function fakeContainer(controlled = true) {
  const listeners: (() => void)[] = []
  return {
    controller: controlled ? ({} as ServiceWorker) : null,
    addEventListener: (_: string, fn: () => void) => listeners.push(fn),
    takeOver() {
      listeners.forEach((fn) => fn())
    },
  }
}

function withWorker(controlled = true) {
  const f = fakeHost()
  const reg = fakeRegistration()
  const container = fakeContainer(controlled)
  f.host.serviceWorker = container as unknown as Container
  const onReady = vi.fn()
  const watcher = watchForUpdates(
    f.host,
    'old',
    reg as unknown as Registration,
    onReady,
  )
  return { ...f, reg, container, onReady, watcher }
}

describe('watchForUpdates with a service worker', () => {
  it('offers a reload once a new worker is waiting', () => {
    const t = withWorker()
    expect(t.onReady).not.toHaveBeenCalled()
    t.reg.install()
    expect(t.onReady).toHaveBeenCalledWith(true)
    // It does not ask the server for build IDs.
    expect(t.host.fetch).not.toHaveBeenCalled()
  })

  it('does not count the first install as an update', () => {
    const t = withWorker(false)
    t.reg.install()
    expect(t.onReady).not.toHaveBeenCalled()
  })

  it('offers a worker that was already waiting when the page loaded', () => {
    const f = fakeHost()
    const reg = fakeRegistration()
    reg.waiting = fakeWorker('installed')
    f.host.serviceWorker = fakeContainer() as unknown as Container
    const onReady = vi.fn()
    watchForUpdates(f.host, 'old', reg as unknown as Registration, onReady)
    expect(onReady).toHaveBeenCalledWith(true)
  })

  it('looks for a new worker when the page comes back and every 30 minutes', async () => {
    const t = withWorker()
    await t.returnToPage()
    expect(t.reg.update).toHaveBeenCalledTimes(1)
    await t.tick()
    expect(t.reg.update).toHaveBeenCalledTimes(2)
  })

  it('offers the waiting worker again on the next check', async () => {
    const t = withWorker()
    t.reg.install()
    t.onReady.mockClear()
    await t.watcher.check()
    expect(t.onReady).toHaveBeenCalledWith(true)
  })

  it('survives a failed update check', async () => {
    const t = withWorker()
    t.reg.update.mockRejectedValueOnce(new TypeError('offline'))
    await expect(t.watcher.check()).resolves.toBeUndefined()
  })

  it('asks the waiting worker to take over and reloads once it has', () => {
    const t = withWorker()
    const worker = t.reg.install()

    t.watcher.reload()
    expect(worker.postMessage).toHaveBeenCalledWith({
      type: SKIP_WAITING_MESSAGE,
    })
    expect(t.host.reload).not.toHaveBeenCalled()

    t.container.takeOver()
    expect(t.host.reload).toHaveBeenCalledTimes(1)

    // Neither another takeover nor the fallback timer reloads again.
    t.container.takeOver()
    t.timeouts.forEach((fn) => fn())
    expect(t.host.reload).toHaveBeenCalledTimes(1)
  })

  it('reloads anyway if the worker does not take over', () => {
    const t = withWorker()
    t.reg.install()
    t.watcher.reload()
    expect(t.host.setTimeout).toHaveBeenCalledWith(
      expect.any(Function),
      TAKEOVER_TIMEOUT_MS,
    )
    t.timeouts.forEach((fn) => fn())
    expect(t.host.reload).toHaveBeenCalledTimes(1)
  })

  it('never reloads on a takeover nobody asked for', () => {
    const t = withWorker()
    t.reg.install()
    t.container.takeOver()
    expect(t.host.reload).not.toHaveBeenCalled()
  })

  it('just reloads when another tab already switched workers', () => {
    const t = withWorker()
    t.watcher.reload()
    expect(t.host.reload).toHaveBeenCalledTimes(1)
  })
})
