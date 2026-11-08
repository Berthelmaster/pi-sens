// Registers the service worker (src/sw/sw.ts, built to /sw.js) that caches the
// app shell and shows an offline page when the Pi cannot be reached.
//
// Browsers only run service workers in a secure context: HTTPS, as through a
// Cloudflare Tunnel, or localhost. Over plain http on the LAN there is no
// navigator.serviceWorker, and the dashboard works as before without one.

/**
 * The meta tag the worker adds to the cached shell it serves while the Pi is
 * unreachable. Same as OFFLINE_META in src/sw/strategy.ts, which the app does
 * not import: shared code would end up in a chunk sw.js cannot load.
 */
export const OFFLINE_META = 'pisens-offline'

/** The parts of window the registration looks at. */
export interface SWHost {
  isSecureContext: boolean
  navigator: { serviceWorker?: Pick<ServiceWorkerContainer, 'register'> }
}

/**
 * Registers /sw.js when the context allows it and reports whether it tried.
 * A failed registration only means no offline support, so it is logged as a
 * warning and otherwise ignored.
 */
export function registerServiceWorker(host: SWHost = window): boolean {
  const container = host.navigator.serviceWorker
  if (!host.isSecureContext || !container) return false
  container.register('/sw.js').catch((err: unknown) => {
    console.warn('pisens: service worker registration failed', err)
  })
  return true
}

/**
 * Whether this page is the cached shell the service worker served because
 * the Pi could not be reached.
 */
export function servedOffline(doc: Pick<Document, 'querySelector'>): boolean {
  return doc.querySelector(`meta[name="${OFFLINE_META}"]`) !== null
}
