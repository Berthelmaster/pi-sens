import { describe, expect, it, vi } from 'vitest'
import { OFFLINE_META as APP_OFFLINE_META } from '../lib/serviceWorker'
import {
  CACHE_PREFIX,
  OFFLINE_DETAIL,
  OFFLINE_META,
  OFFLINE_TITLE,
  SHELL_URL,
  cacheName,
  handleNavigation,
  handlePrecached,
  offlineFallback,
  precache,
  routeFor,
  staleCaches,
  type Env,
  type PrecacheManifest,
} from './strategy'

const ORIGIN = 'https://pi.example.com'
const SHELL =
  '<!doctype html><html><head><title>pisens</title></head><body></body></html>'

const manifest: PrecacheManifest = {
  version: 'abc123',
  urls: [
    '/favicon.svg',
    '/icons/icon-192.png',
    '/index.html',
    '/manifest.webmanifest',
    '/assets/index-abc.js',
    '/assets/index-def.css',
  ],
}
const precached = new Set(manifest.urls)

function nav(path: string): Request {
  // Node cannot construct a navigate-mode Request, so the tests fake the mode
  // the way the browser hands it to the worker.
  const request = new Request(ORIGIN + path)
  Object.defineProperty(request, 'mode', { value: 'navigate' })
  return request
}

/** A cache holding the given paths, as the worker's shell cache would. */
function cacheWith(files: Record<string, string>): Env['match'] {
  return async (url) =>
    url in files ? new Response(files[url], { status: 200 }) : undefined
}

function env(
  fetchImpl: Env['fetch'],
  files: Record<string, string> = { [SHELL_URL]: SHELL },
): Env {
  return { fetch: vi.fn(fetchImpl), match: cacheWith(files) }
}

describe('routeFor', () => {
  const route = (url: string, mode: RequestMode = 'cors', method = 'GET') =>
    routeFor({ url, mode, method }, ORIGIN, precached)

  it('handles page loads anywhere in the app', () => {
    expect(route(ORIGIN + '/', 'navigate')).toBe('navigate')
    expect(route(ORIGIN + '/history', 'navigate')).toBe('navigate')
    expect(route(ORIGIN + '/history?range=1h', 'navigate')).toBe('navigate')
  })

  it('serves the files of the shell from the cache', () => {
    expect(route(ORIGIN + '/assets/index-abc.js', 'no-cors')).toBe('precache')
    expect(route(ORIGIN + '/manifest.webmanifest')).toBe('precache')
    expect(route(ORIGIN + '/icons/icon-192.png', 'no-cors')).toBe('precache')
  })

  it('never touches the API or the live stream', () => {
    for (const path of [
      '/api',
      '/api/stream',
      '/api/metrics',
      '/api/health',
      '/api/history?range=1h',
      '/metrics',
    ]) {
      expect(route(ORIGIN + path), path).toBeNull()
      // Not even when opened in a tab.
      expect(route(ORIGIN + path, 'navigate'), path).toBeNull()
    }
  })

  it('leaves other origins, other methods and unknown files alone', () => {
    expect(route('https://team.cloudflareaccess.com/', 'navigate')).toBeNull()
    expect(route(ORIGIN + '/assets/index-abc.js', 'cors', 'POST')).toBeNull()
    expect(route(ORIGIN + '/history', 'navigate', 'POST')).toBeNull()
    expect(route(ORIGIN + '/assets/other-123.js')).toBeNull()
    expect(route(ORIGIN + '/assets/index-abc.js?v=2')).toBeNull()
  })
})

describe('handleNavigation', () => {
  it('returns the network response when the Pi answers', async () => {
    const page = new Response('<p>fresh</p>', { status: 200 })
    const e = env(async () => page)
    const request = nav('/history')

    expect(await handleNavigation(request, e)).toBe(page)
    // The request goes out as it is, so it keeps the navigation's redirect
    // mode ('manual') and Access redirects reach the browser.
    expect(e.fetch).toHaveBeenCalledWith(request)
  })

  it('passes a redirect to Cloudflare Access through', async () => {
    const login =
      'https://team.cloudflareaccess.com/cdn-cgi/access/login/pi.example.com'
    const redirect = Response.redirect(login, 302)
    const e = env(async () => redirect)

    const response = await handleNavigation(nav('/'), e)
    expect(response).toBe(redirect)
    expect(response.headers.get('Location')).toBe(login)
  })

  it('passes an opaque redirect through', async () => {
    // What fetch() in a worker returns for a navigation that redirects.
    const opaque = {
      type: 'opaqueredirect',
      status: 0,
      ok: false,
    } as unknown as Response
    expect(
      await handleNavigation(
        nav('/'),
        env(async () => opaque),
      ),
    ).toBe(opaque)
  })

  it('passes other answers from the Pi through', async () => {
    for (const status of [401, 403, 404, 500, 503]) {
      const response = new Response('', { status })
      expect(
        await handleNavigation(
          nav('/'),
          env(async () => response),
        ),
        `status ${status}`,
      ).toBe(response)
    }
  })

  it('shows the offline shell when the network fails', async () => {
    const e = env(async () => {
      throw new TypeError('Failed to fetch')
    })
    const response = await handleNavigation(nav('/'), e)
    await expectOfflineShell(response)
  })

  it.each([502, 530])(
    'shows the offline shell for a Cloudflare %i',
    async (status) => {
      const e = env(async () => new Response('Bad gateway', { status }))
      await expectOfflineShell(await handleNavigation(nav('/history'), e))
    },
  )

  it.each([502, 530])(
    'shows the offline page for a %i when nothing is cached',
    async (status) => {
      const e = env(async () => new Response('', { status }), {})
      await expectOfflinePage(await handleNavigation(nav('/'), e))
    },
  )

  it('shows the offline page when the network fails and nothing is cached', async () => {
    const e = env(async () => {
      throw new TypeError('Failed to fetch')
    }, {})
    await expectOfflinePage(await handleNavigation(nav('/'), e))
  })
})

async function expectOfflineShell(response: Response) {
  expect(response.status).toBe(200)
  expect(response.headers.get('Content-Type')).toMatch(/^text\/html/)
  const html = await response.text()
  expect(html).toContain('<title>pisens</title>')
  expect(html).toContain(`<meta name="${OFFLINE_META}" />`)
  expect(html.indexOf(OFFLINE_META)).toBeLessThan(html.indexOf('</head>'))
}

async function expectOfflinePage(response: Response) {
  expect(response.status).toBe(200)
  expect(response.headers.get('Content-Type')).toMatch(/^text\/html/)
  const html = await response.text()
  expect(html).toContain(OFFLINE_TITLE)
  expect(html).toContain(OFFLINE_DETAIL)
}

describe('offlineFallback', () => {
  it('uses the offline page if the cached shell has no head', async () => {
    const e = env(async () => new Response(), { [SHELL_URL]: 'garbage' })
    await expectOfflinePage(await offlineFallback(e))
  })

  it('says what the task asks for', () => {
    expect(`${OFFLINE_TITLE} ${OFFLINE_DETAIL}`).toBe(
      "Can't reach pisens. It will reconnect when the Pi is back.",
    )
  })

  it('uses the same meta tag as the app', () => {
    expect(OFFLINE_META).toBe(APP_OFFLINE_META)
  })
})

describe('handlePrecached', () => {
  it('serves a cached file without the network', async () => {
    const e = env(
      async () => {
        throw new TypeError('offline')
      },
      { '/assets/index-abc.js': 'console.log(1)' },
    )
    const response = await handlePrecached(
      new Request(ORIGIN + '/assets/index-abc.js'),
      e,
    )
    expect(await response.text()).toBe('console.log(1)')
    expect(e.fetch).not.toHaveBeenCalled()
  })

  it('fetches a file missing from the cache', async () => {
    const e = env(async () => new Response('from network'), {})
    const response = await handlePrecached(
      new Request(ORIGIN + '/assets/index-abc.js'),
      e,
    )
    expect(await response.text()).toBe('from network')
  })
})

describe('precache', () => {
  it('stores every file of the shell and nothing else', async () => {
    const put = vi.fn(async (_url: string, _response: Response) => {})
    const fetchFn = vi.fn(async (r: Request) => new Response(r.url))

    await precache(manifest, fetchFn, { put }, ORIGIN)

    expect(put.mock.calls.map(([url]) => url).sort()).toEqual(
      [...manifest.urls].sort(),
    )
    for (const [request] of fetchFn.mock.calls) {
      expect(request.cache).toBe('reload')
      expect(request.redirect).toBe('error')
    }
  })

  it('fails the install when a file cannot be fetched', async () => {
    const fetchFn = async (r: Request) =>
      new Response('', { status: r.url.endsWith('.css') ? 502 : 200 })
    await expect(
      precache(manifest, fetchFn, { put: async () => {} }, ORIGIN),
    ).rejects.toThrow(/index-def\.css: HTTP 502/)
  })
})

describe('caches', () => {
  it('names the cache after the version', () => {
    expect(cacheName(manifest)).toBe(CACHE_PREFIX + 'abc123')
  })

  it('deletes only older shell caches', () => {
    expect(
      staleCaches(
        [CACHE_PREFIX + 'old', CACHE_PREFIX + 'abc123', 'something-else'],
        CACHE_PREFIX + 'abc123',
      ),
    ).toEqual([CACHE_PREFIX + 'old'])
  })
})
