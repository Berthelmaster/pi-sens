// Connects the update watcher in update.ts to the page: whether the "new
// version" toast shows, and what its buttons do. main.ts starts it in
// production builds only.

import { connection } from './live'
import { pageBuild, watchForUpdates, type UpdateWatcher } from './update'

export const updatePrompt = $state({ ready: false })

let watcher: UpdateWatcher | null = null

/**
 * Starts watching for a new version, through the service worker when it
 * registered and through /api/health otherwise.
 */
export async function startUpdateWatcher(
  registration: Promise<ServiceWorkerRegistration | null> | null,
): Promise<void> {
  const reg = registration ? await registration : null
  watcher = watchForUpdates(
    {
      document,
      fetch: (url, init) => fetch(url, init),
      reload: () => location.reload(),
      setInterval: (fn, ms) => setInterval(fn, ms),
      clearInterval: (id) => clearInterval(id as number),
      setTimeout: (fn, ms) => setTimeout(fn, ms),
      storage: sessionStorageOrNull(),
      serviceWorker: reg ? navigator.serviceWorker : undefined,
    },
    pageBuild(document),
    reg,
    (ready) => (updatePrompt.ready = ready),
  )

  // A deploy restarts pisens, which drops the live stream. When it comes
  // back the new version is likely there, so look straight away instead of
  // waiting for the next check.
  let dropped = false
  connection.subscribe(({ state }) => {
    if (state === 'reconnecting' || state === 'offline') dropped = true
    else if (state === 'live' && dropped) {
      dropped = false
      void watcher?.check()
    }
  })
}

/** Switches to the new version and reloads. */
export function reloadToUpdate(): void {
  watcher?.reload()
}

/** Hides the toast until a later check finds the new version again. */
export function dismissUpdate(): void {
  updatePrompt.ready = false
}

function sessionStorageOrNull(): Storage | null {
  try {
    return window.sessionStorage
  } catch {
    // Blocked storage throws on access.
    return null
  }
}
