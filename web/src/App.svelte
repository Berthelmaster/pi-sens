<script lang="ts">
  import type { Component } from 'svelte'
  import ConnectionStatus from './components/ConnectionStatus.svelte'
  import Hostname from './components/Hostname.svelte'
  import ThemeToggle from './components/ThemeToggle.svelte'
  import { followLink, router } from './lib/router.svelte'
  import History from './pages/History.svelte'
  import NotFound from './pages/NotFound.svelte'
  import Overview from './pages/Overview.svelte'

  interface Route {
    path: string
    label: string
    page: Component
  }

  const routes: Route[] = [
    { path: '/', label: 'Overview', page: Overview },
    { path: '/history', label: 'History', page: History },
  ]

  let active = $derived(routes.find((r) => r.path === router.path))
  let Page = $derived(active?.page ?? NotFound)

  $effect(() => {
    document.title = active ? `${active.label} · pisens` : 'pisens'
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
    </nav>

    <div class="actions">
      <ConnectionStatus />
      <ThemeToggle />
    </div>
  </div>
</header>

<main>
  <Page />
</main>

<style>
  .app-header {
    position: sticky;
    top: 0;
    z-index: 10;
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
    max-width: var(--content-max-width);
    margin: 0 auto;
    padding: var(--space-4);
  }

  @media (min-width: 1024px) {
    main {
      padding: var(--space-6);
    }
  }
</style>
