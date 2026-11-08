// Notices when a new version of pisens has been deployed while a page is
// open, so it can offer a reload instead of running the old frontend against
// the new backend until the app is closed.
//
// Two ways to find out, depending on whether a service worker runs:
//
// - With one (HTTPS or localhost) the browser checks /sw.js, which changes
//   with every build. The new worker installs and then waits, and the page
//   offers the reload. Reload tells the waiting worker to take over and
//   reloads once it has.
// - Without one (plain http on the LAN) the page compares its build ID with
//   the one /api/health reports and offers a plain reload when they differ.
//
// Either way the page checks when it comes back to the foreground and every
// UPDATE_CHECK_INTERVAL_MS while it is open, and never reloads on its own.

/**
 * Name of the meta tag holding the build ID that vite-plugin-build-id.ts
 * stamps into index.html. The dev server has none, so it never offers a
 * reload.
 */
export const BUILD_META = 'pisens-build'

/**
 * The message that tells a waiting service worker to take over. Same as
 * SKIP_WAITING_MESSAGE in src/sw/strategy.ts, which the app does not import
 * (see serviceWorker.ts).
 */
export const SKIP_WAITING_MESSAGE = 'pisens-skip-waiting'

/** How often an open page checks for a new version. */
export const UPDATE_CHECK_INTERVAL_MS = 30 * 60 * 1000

/**
 * How long Reload waits for the new service worker to take over before it
 * reloads anyway. The new page is fetched from the network either way.
 */
export const TAKEOVER_TIMEOUT_MS = 3000

/** Session storage key for the server build a reload was asked for. */
export const RELOADED_FOR_KEY = 'pisens-reloaded-for'

/** The build ID of the page, null without one (dev server). */
export function pageBuild(doc: Pick<Document, 'querySelector'>): string | null {
  const meta = doc.querySelector<HTMLMetaElement>(`meta[name="${BUILD_META}"]`)
  return meta?.content || null
}

/**
 * The build ID of the frontend the server has, from the /api/health body;
 * null when it reports none.
 */
export function serverBuild(health: unknown): string | null {
  if (typeof health !== 'object' || health === null) return null
  const web = (health as { web?: unknown }).web
  return typeof web === 'string' && web !== '' ? web : null
}

/**
 * Whether the server has a different frontend than the page, and so a reload
 * would load a new version. Unknown on either side means no: a page from the
 * dev server, or a server from before build IDs, is never told to reload.
 *
 * reloadedFor is the server build this tab last reloaded for. If the page
 * still differs from that same build after the reload, something in between
 * keeps serving the old page; offering the reload again would only loop.
 */
export function isNewBuild(
  page: string | null,
  server: string | null,
  reloadedFor: string | null = null,
): boolean {
  if (page === null || server === null) return false
  return page !== server && server !== reloadedFor
}

/** The parts of a ServiceWorkerRegistration the watcher uses. */
export type Registration = Pick<
  ServiceWorkerRegistration,
  'installing' | 'waiting' | 'update' | 'addEventListener'
>

/** The parts of window the watcher uses, so tests can fake them. */
export interface UpdateHost {
  document: Pick<
    Document,
    'visibilityState' | 'addEventListener' | 'removeEventListener'
  >
  fetch: (url: string, init?: RequestInit) => Promise<Response>
  reload: () => void
  setInterval: (fn: () => void, ms: number) => unknown
  clearInterval: (id: unknown) => void
  setTimeout: (fn: () => void, ms: number) => unknown
  /** Session storage; null where it cannot be used. */
  storage: Pick<Storage, 'getItem' | 'setItem'> | null
  /** The service worker container, when the page has a registration. */
  serviceWorker?: Pick<
    ServiceWorkerContainer,
    'controller' | 'addEventListener'
  >
}

export interface UpdateWatcher {
  /** Looks for a new version now. */
  check: () => Promise<void>
  /** Switches to the new version and reloads the page once. */
  reload: () => void
  /** Stops checking. */
  stop: () => void
}

/**
 * Watches for a new version and calls onReady(true) once there is one to
 * reload to. Pass the service worker registration when there is one; without
 * it the watcher compares build IDs with /api/health.
 */
export function watchForUpdates(
  host: UpdateHost,
  page: string | null,
  registration: Registration | null,
  onReady: (ready: boolean) => void,
): UpdateWatcher {
  let reloading = false
  function reloadOnce() {
    if (reloading) return
    reloading = true
    host.reload()
  }

  const mode = registration
    ? workerMode(host, registration, onReady, reloadOnce)
    : healthMode(host, page, onReady, reloadOnce)

  const onVisibility = () => {
    if (host.document.visibilityState === 'visible') void mode.check()
  }
  host.document.addEventListener('visibilitychange', onVisibility)
  const timer = host.setInterval(
    () => void mode.check(),
    UPDATE_CHECK_INTERVAL_MS,
  )

  return {
    check: mode.check,
    reload: mode.reload,
    stop() {
      host.document.removeEventListener('visibilitychange', onVisibility)
      host.clearInterval(timer)
    },
  }
}

interface Mode {
  check: () => Promise<void>
  reload: () => void
}

function workerMode(
  host: UpdateHost,
  registration: Registration,
  onReady: (ready: boolean) => void,
  reloadOnce: () => void,
): Mode {
  const container = host.serviceWorker
  let asked = false

  // A worker that installs while an older one controls the page waits until
  // asked to take over. Without a controller it is the first install, which
  // takes over by itself and is not an update.
  const offerIfWaiting = () => {
    if (registration.waiting && container?.controller) onReady(true)
  }
  offerIfWaiting()
  registration.addEventListener('updatefound', () => {
    const worker = registration.installing
    worker?.addEventListener('statechange', () => {
      if (worker.state === 'installed') offerIfWaiting()
    })
  })

  // The page only reloads when the new worker takes over because Reload
  // asked it to, and only once, so a worker that takes over for any other
  // reason cannot start a reload loop.
  container?.addEventListener('controllerchange', () => {
    if (asked) reloadOnce()
  })

  return {
    async check() {
      try {
        await registration.update()
      } catch {
        // Offline, or the Pi is restarting; the next check tries again.
      }
      // A worker found earlier may still be waiting after the toast was
      // dismissed; offer it again.
      offerIfWaiting()
    },
    reload() {
      const waiting = registration.waiting
      if (!waiting) {
        reloadOnce()
        return
      }
      asked = true
      waiting.postMessage({ type: SKIP_WAITING_MESSAGE })
      host.setTimeout(reloadOnce, TAKEOVER_TIMEOUT_MS)
    },
  }
}

function healthMode(
  host: UpdateHost,
  page: string | null,
  onReady: (ready: boolean) => void,
  reloadOnce: () => void,
): Mode {
  let target: string | null = null

  return {
    async check() {
      if (page === null) return
      let server: string | null
      try {
        const res = await host.fetch('/api/health', { cache: 'no-store' })
        if (!res.ok) return
        server = serverBuild(await res.json())
      } catch {
        // Offline, restarting or behind an expired login; try again later.
        return
      }
      const reloadedFor = readStorage(host, RELOADED_FOR_KEY)
      if (isNewBuild(page, server, reloadedFor)) {
        target = server
        onReady(true)
      } else if (server === page) {
        // Back on the version the page has, after a rollback for instance.
        target = null
        onReady(false)
      }
    },
    reload() {
      if (target !== null) writeStorage(host, RELOADED_FOR_KEY, target)
      reloadOnce()
    },
  }
}

function readStorage(host: UpdateHost, key: string): string | null {
  try {
    return host.storage?.getItem(key) ?? null
  } catch {
    return null
  }
}

function writeStorage(host: UpdateHost, key: string, value: string) {
  try {
    host.storage?.setItem(key, value)
  } catch {
    // Without storage the loop guard is off, but Reload still works.
  }
}
