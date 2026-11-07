// A minimal client-side router on top of the History API. The Go server (and
// the Vite dev server) answer every extension-less path with index.html, so
// routes such as /history survive a reload.

let path = $state(normalize(location.pathname))
// Bumped on every navigation, also between two URLs on the same path, so the
// page can be rebuilt for a new query string.
let visit = $state(0)

window.addEventListener('popstate', () => {
  path = normalize(location.pathname)
  visit++
})

function normalize(p: string): string {
  return p.length > 1 ? p.replace(/\/+$/, '') : p
}

export const router = {
  /** The current path without a trailing slash, e.g. "/" or "/history". */
  get path(): string {
    return path
  },
  /**
   * Changes on every navigation, including one that only changes the query
   * string. Pages read their query string when they are created, so the app
   * rebuilds the page when this changes.
   */
  get visit(): number {
    return visit
  },
}

/**
 * Go to a path inside the app without reloading the page. The path may carry
 * a query string, e.g. "/history?range=7d"; it ends up in the address bar
 * while `router.path` stays the bare path.
 */
export function navigate(to: string): void {
  const url = new URL(to, location.origin)
  const next = normalize(url.pathname)
  if (next === path && url.search === location.search) return
  history.pushState(null, '', next + url.search)
  path = next
  visit++
  window.scrollTo(0, 0)
}

/**
 * Click handler for in-app links: `<a href="/history" onclick={followLink}>`.
 * The link's query string is kept.
 * Modified clicks (new tab, new window, ...) and links that leave the app are
 * left to the browser.
 */
export function followLink(event: MouseEvent): void {
  if (
    event.defaultPrevented ||
    event.button !== 0 ||
    event.metaKey ||
    event.ctrlKey ||
    event.shiftKey ||
    event.altKey
  ) {
    return
  }
  const anchor = event.currentTarget
  if (!(anchor instanceof HTMLAnchorElement)) return
  if (anchor.target && anchor.target !== '_self') return
  if (anchor.hasAttribute('download')) return

  const url = new URL(anchor.href)
  if (url.origin !== location.origin) return

  event.preventDefault()
  navigate(url.pathname + url.search)
}
