// A minimal client-side router on top of the History API. The Go server (and
// the Vite dev server) answer every extension-less path with index.html, so
// routes such as /history survive a reload.

let path = $state(normalize(location.pathname))

window.addEventListener('popstate', () => {
  path = normalize(location.pathname)
})

function normalize(p: string): string {
  return p.length > 1 ? p.replace(/\/+$/, '') : p
}

export const router = {
  /** The current path without a trailing slash, e.g. "/" or "/history". */
  get path(): string {
    return path
  },
}

/** Go to a path inside the app without reloading the page. */
export function navigate(to: string): void {
  const next = normalize(to)
  if (next === path) return
  history.pushState(null, '', next)
  path = next
  window.scrollTo(0, 0)
}

/**
 * Click handler for in-app links: `<a href="/history" onclick={followLink}>`.
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
  navigate(url.pathname)
}
