// The service worker, built to /sw.js by vite-plugin-sw.ts and registered by
// src/lib/serviceWorker.ts. The decisions live in strategy.ts; this file only
// wires them to the worker's events.

import {
  cacheName,
  handleNavigation,
  handlePrecached,
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
      // Take over from an older worker straight away. Pages are always loaded
      // from the network when it is there, so an open tab keeps working.
      await self.skipWaiting()
    })(),
  )
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
