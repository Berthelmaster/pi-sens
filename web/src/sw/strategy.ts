// What the service worker does with each request, kept apart from the worker
// globals so it can be tested in node with a mocked fetch and cache.
//
// Only two kinds of request are handled at all:
//
// - Page navigations go to the network first, so a new release and an expired
//   Cloudflare Access session (a redirect to *.cloudflareaccess.com) reach the
//   browser as they are. Only when the Pi cannot be reached does the worker
//   answer instead, with the cached shell marked as offline, or a standalone
//   offline page if nothing is cached.
// - Files of the precached app shell (hashed JS and CSS, icons, manifest) come
//   from the cache, so the installed app opens straight away.
//
// Everything else, /api/* and the /api/stream SSE stream in particular, is
// left to the browser and never touches Cache Storage.

/** The app shell, written into sw.js by the build (see vite-plugin-sw.ts). */
export interface PrecacheManifest {
  /** Changes whenever any file of the shell does. */
  version: string
  /** Absolute paths such as /index.html and /assets/index-abc123.js. */
  urls: string[]
}

/** Every cache the worker creates starts with this. */
export const CACHE_PREFIX = 'pisens-shell-'

/** The page the cached shell is served from. */
export const SHELL_URL = '/index.html'

/** The message the offline page and the offline shell show. */
export const OFFLINE_TITLE = "Can't reach pisens."
export const OFFLINE_DETAIL = 'It will reconnect when the Pi is back.'

/**
 * Name of the meta tag added to the cached shell when it stands in for a page
 * the network could not deliver. The app shows its offline view while the tag
 * is there and the live stream has not connected yet.
 */
export const OFFLINE_META = 'pisens-offline'

/**
 * Statuses that mean the Pi is unreachable rather than that it answered.
 * Cloudflare sends 502 when the tunnel is up but nothing listens behind it
 * (pisens stopped, Pi rebooting) and 530 when the tunnel itself is down; 504
 * and 520-524 are its other ways of saying the origin did not answer.
 * Anything else, a redirect or 401 included, is the real answer and goes to
 * the browser unchanged.
 */
const UNREACHABLE_STATUSES = new Set([502, 504, 520, 521, 522, 523, 524, 530])

export function cacheName(manifest: PrecacheManifest): string {
  return CACHE_PREFIX + manifest.version
}

/** Paths pisens serves itself rather than from the frontend. */
function isBackendPath(pathname: string): boolean {
  return (
    pathname === '/api' ||
    pathname.startsWith('/api/') ||
    pathname === '/metrics'
  )
}

export type Route = 'navigate' | 'precache'

/**
 * Decides how the worker handles a request: 'navigate' for page loads,
 * 'precache' for files of the shell, null to let the browser fetch it as if
 * there were no worker.
 */
export function routeFor(
  request: Pick<Request, 'method' | 'mode' | 'url'>,
  origin: string,
  precached: ReadonlySet<string>,
): Route | null {
  if (request.method !== 'GET') return null
  const url = new URL(request.url)
  if (url.origin !== origin || isBackendPath(url.pathname)) return null
  if (request.mode === 'navigate') return 'navigate'
  // Query strings are not used for the shell's files; a URL with one is
  // something else and goes to the network.
  if (url.search === '' && precached.has(url.pathname)) return 'precache'
  return null
}

/** The parts of the worker's environment the strategies use. */
export interface Env {
  fetch: (request: Request) => Promise<Response>
  /** Looks up a URL in the shell cache. */
  match: (url: string) => Promise<Response | undefined>
}

/**
 * Network first for a page load. The request is fetched as it is: a
 * navigation's redirect mode is 'manual', so a redirect to Cloudflare Access
 * comes back as an opaque redirect that the browser then follows to the login
 * page. Only a failed fetch or an unreachable-origin status falls back.
 */
export async function handleNavigation(
  request: Request,
  env: Env,
): Promise<Response> {
  let response: Response
  try {
    response = await env.fetch(request)
  } catch {
    return offlineFallback(env)
  }
  if (UNREACHABLE_STATUSES.has(response.status)) return offlineFallback(env)
  return response
}

/** Cache first for files of the shell, the network if one is missing. */
export async function handlePrecached(
  request: Request,
  env: Env,
): Promise<Response> {
  const cached = await env.match(new URL(request.url).pathname)
  return cached ?? env.fetch(request)
}

/** The cached shell marked as offline, or the offline page without one. */
export async function offlineFallback(env: Env): Promise<Response> {
  const shell = await env.match(SHELL_URL)
  if (shell) {
    const html = await shell.text()
    if (html.includes('</head>')) {
      return htmlResponse(
        html.replace('</head>', `<meta name="${OFFLINE_META}" />\n</head>`),
      )
    }
  }
  return htmlResponse(offlinePage())
}

function htmlResponse(html: string): Response {
  return new Response(html, {
    headers: {
      'Content-Type': 'text/html; charset=utf-8',
      'Cache-Control': 'no-store',
    },
  })
}

/**
 * How often the offline page checks whether pisens is back. Short enough to
 * feel immediate after a reboot, long enough not to matter for the Pi.
 */
export const OFFLINE_RETRY_MS = 5000

/**
 * A page that needs nothing from the cache. It polls /api/health and reloads
 * once the Pi answers. With redirect 'manual' an expired Access session counts
 * as an answer too, and the reload then goes to the login page.
 */
export function offlinePage(): string {
  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<meta name="color-scheme" content="light dark" />
<title>pisens – offline</title>
<style>
  body { margin: 0; min-height: 100vh; display: grid; place-items: center;
    font-family: system-ui, sans-serif; background: #0f1115; color: #e6e8eb; }
  @media (prefers-color-scheme: light) {
    body { background: #f5f6f8; color: #1a1d23; }
  }
  main { max-width: 28rem; padding: 1.5rem; text-align: center; }
  h1 { font-size: 1.25rem; margin: 0 0 0.5rem; }
  p { margin: 0; opacity: 0.75; }
</style>
</head>
<body>
<main role="status">
  <h1>${OFFLINE_TITLE}</h1>
  <p>${OFFLINE_DETAIL}</p>
</main>
<script>
  setInterval(function () {
    fetch('/api/health', { cache: 'no-store', redirect: 'manual' })
      .then(function (r) {
        if (r.ok || r.type === 'opaqueredirect') location.reload()
      })
      .catch(function () {})
  }, ${OFFLINE_RETRY_MS})
</script>
</body>
</html>
`
}

/** The parts of a Cache the precache step uses. */
export interface PrecacheTarget {
  put: (url: string, response: Response) => Promise<void>
}

/**
 * Downloads every file of the shell and stores it. Any failure, a redirect
 * included (an expired Access session would otherwise store the login page),
 * rejects, so the install fails and the browser tries again on a later load
 * instead of activating a worker with a broken shell.
 */
export async function precache(
  manifest: PrecacheManifest,
  fetchFn: (request: Request) => Promise<Response>,
  cache: PrecacheTarget,
  origin: string,
): Promise<void> {
  await Promise.all(
    manifest.urls.map(async (url) => {
      const request = new Request(new URL(url, origin), {
        // Past the HTTP cache, so a revalidating file is current.
        cache: 'reload',
        redirect: 'error',
      })
      const response = await fetchFn(request)
      if (!response.ok) {
        throw new Error(`precache ${url}: HTTP ${response.status}`)
      }
      await cache.put(url, response)
    }),
  )
}

/** Caches of earlier versions of the worker, to delete once this one runs. */
export function staleCaches(names: string[], current: string): string[] {
  return names.filter((n) => n.startsWith(CACHE_PREFIX) && n !== current)
}
