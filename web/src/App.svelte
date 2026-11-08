<script lang="ts">
  import type { Component } from 'svelte'
  import AlertBanner from './components/AlertBanner.svelte'
  import AlertsNavButton from './components/AlertsNavButton.svelte'
  import ConnectionStatus from './components/ConnectionStatus.svelte'
  import Hostname from './components/Hostname.svelte'
  import ThemeToggle from './components/ThemeToggle.svelte'
  import { alerts, connection, snapshot } from './lib/live'
  import { followLink, router } from './lib/router.svelte'
  import { servedOffline } from './lib/serviceWorker'
  import History from './pages/History.svelte'
  import NotFound from './pages/NotFound.svelte'
  import Offline from './pages/Offline.svelte'
  import Overview from './pages/Overview.svelte'
  import { hasCritical } from './types/alerts'
  import { hostnameOf } from './types/sysinfo'

  interface Route {
    path: string
    label: string
    page: Component
  }

  interface LazyRoute {
    path: string
    label: string
    load: () => Promise<{ default: Component }>
  }

  const routes: Route[] = [
    { path: '/', label: 'Overview', page: Overview },
    { path: '/history', label: 'History', page: History },
  ]

  // Dev-only pages: reachable by URL under `npm run dev`, left out of the nav.
  // They are loaded with a dynamic import inside the DEV branch, so production
  // builds drop the branch and never emit the page, its components or its CSS.
  const devRoutes: LazyRoute[] = import.meta.env.DEV
    ? [
        {
          path: '/dev/charts',
          label: 'Charts',
          load: () => import('./pages/ChartsDemo.svelte'),
        },
        {
          path: '/dev/energy',
          label: 'Energy',
          load: () => import('./pages/EnergyDemo.svelte'),
        },
        {
          path: '/dev/netquality',
          label: 'Internet health',
          load: () => import('./pages/NetQualityDemo.svelte'),
        },
      ]
    : []

  let active = $derived<Route | LazyRoute | undefined>(
    [...routes, ...devRoutes].find((r) => r.path === router.path),
  )
  let Page = $derived(active && 'page' in active ? active.page : NotFound)
  let lazy = $derived(active && 'load' in active ? active.load() : undefined)

  // The service worker serves the cached shell, marked as offline, when the
  // Pi cannot be reached. Show the offline page instead of the dashboard
  // until the live stream connects, which it keeps retrying in the meantime.
  let offline = $state(servedOffline(document))
  $effect(() => {
    if ($connection.state === 'live') offline = false
  })

  let hostname = $derived(hostnameOf($snapshot))
  let critical = $derived(hasCritical($alerts?.firing ?? []))

  // Name the tab after the Pi once its hostname is known, so several Pis open
  // side by side can be told apart, and flag it while a critical alert fires.
  $effect(() => {
    let title = 'pisens'
    if (hostname) title = `${hostname} – pisens`
    else if (active) title = `${active.label} · pisens`
    document.title = critical ? `(!) ${title}` : title
  })
</script>

<header class="app-header">
  <div class="bar">
    <div class="brand">
      <a class="name" href="/" onclick={followLink}>pisens</a>
      <Hostname />
    </div>

    <nav aria-label="Main">
      {#each routes as route (route.path)}
        <a
          href={route.path}
          onclick={followLink}
          aria-current={route === active ? 'page' : undefined}
        >
          {route.label}
        </a>
      {/each}
      <AlertsNavButton />
    </nav>

    <div class="actions">
      <ConnectionStatus />
      <ThemeToggle />
    </div>
  </div>
</header>

<AlertBanner />

<main>
  {#if offline}
    <Offline />
  {:else}
    {#key router.visit}
      {#if lazy}
        {#await lazy then { default: LazyPage }}
          <LazyPage />
        {/await}
      {:else}
        <Page />
      {/if}
    {/key}
  {/if}
</main>

<style>
  .app-header {
    position: sticky;
    top: 0;
    z-index: 10;
    /*
     * Installed on a phone the page draws under the status bar and notch.
     * The background still fills that area; the bar inside stays clear.
     */
    padding: var(--safe-top) var(--safe-right) 0 var(--safe-left);
    background: var(--color-surface);
    border-bottom: 1px solid var(--color-border);
  }

  /*
   * Phones: brand and actions share the first row, the nav gets its own row
   * underneath. From tablets up everything sits on one row.
   */
  .bar {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    grid-template-areas:
      'brand actions'
      'nav nav';
    align-items: center;
    gap: var(--space-2) var(--space-4);
    max-width: var(--content-max-width);
    margin: 0 auto;
    padding: var(--space-2) var(--space-4) 0;
  }

  .brand {
    grid-area: brand;
    display: flex;
    align-items: baseline;
    gap: var(--space-3);
    min-width: 0;
  }

  .name {
    font-size: 1.125rem;
    font-weight: 700;
    color: var(--color-text);
    text-decoration: none;
  }

  nav {
    grid-area: nav;
    display: flex;
    gap: var(--space-1);
  }

  nav a {
    padding: var(--space-2) var(--space-3);
    border-bottom: 2px solid transparent;
    color: var(--color-text-muted);
    text-decoration: none;
    font-weight: 500;
  }

  nav a:hover {
    color: var(--color-text);
  }

  nav a[aria-current='page'] {
    color: var(--color-text);
    border-bottom-color: var(--color-accent);
  }

  .actions {
    grid-area: actions;
    display: flex;
    align-items: center;
    gap: var(--space-3);
  }

  @media (min-width: 640px) {
    .bar {
      grid-template-columns: auto minmax(0, 1fr) auto;
      grid-template-areas: 'brand nav actions';
      padding-top: 0;
    }

    nav a {
      padding: var(--space-4) var(--space-3);
    }
  }

  main {
    --pad: var(--space-4);

    max-width: var(--content-max-width);
    margin: 0 auto;
    padding: var(--pad) calc(var(--pad) + var(--safe-right))
      calc(var(--pad) + var(--safe-bottom)) calc(var(--pad) + var(--safe-left));
  }

  @media (min-width: 1024px) {
    main {
      --pad: var(--space-6);
    }
  }
</style>
