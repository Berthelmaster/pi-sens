// The service worker, built to /sw.js by vite-plugin-sw.ts and registered by
// src/lib/serviceWorker.ts. The decisions live in strategy.ts; this file only
// wires them to the worker's events.

import {
  cacheName,
  handleNavigation,
  handlePrecached,
  isSkipWaiting,
  precache,
  routeFor,
  staleCaches,
  type Env,
  type PrecacheManifest,
} from './strategy'

declare const self: ServiceWorkerGlobalScope

// Replaced with the real list of files by the build. The build fails if it
// cannot find this name in the output.
declare const __PISENS_PRECACHE__: PrecacheManifest

const manifest = __PISENS_PRECACHE__
const shellCache = cacheName(manifest)
const precached = new Set(manifest.urls)

const env: Env = {
  fetch: (request) => fetch(request),
  match: async (url) => {
    const cache = await caches.open(shellCache)
    return cache.match(url)
  },
}

self.addEventListener('install', (event) => {
  event.waitUntil(
    (async () => {
      const cache = await caches.open(shellCache)
      await precache(manifest, env.fetch, cache, self.location.origin)
      // A worker replacing an older one waits until a page asks it to take
      // over (below), so open pages can offer the reload instead of being
      // switched under their feet. The first worker takes over at once.
    })(),
  )
})

self.addEventListener('message', (event) => {
  if (isSkipWaiting(event.data)) event.waitUntil(self.skipWaiting())
})

self.addEventListener('activate', (event) => {
  event.waitUntil(
    (async () => {
      const stale = staleCaches(await caches.keys(), shellCache)
      await Promise.all(stale.map((name) => caches.delete(name)))
      await self.clients.claim()
    })(),
  )
})

self.addEventListener('fetch', (event) => {
  const route = routeFor(event.request, self.location.origin, precached)
  if (route === 'navigate') {
    event.respondWith(handleNavigation(event.request, env))
  } else if (route === 'precache') {
    event.respondWith(handlePrecached(event.request, env))
  }
  // Anything else, /api/* included, is not intercepted.
})
