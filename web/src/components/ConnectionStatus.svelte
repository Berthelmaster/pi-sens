<script lang="ts">
  // Header slot for the backend connection state. For now it checks
  // /api/health once; the live data task swaps this for the SSE stream state.
  import { getHealth } from '../api'

  type State = 'connecting' | 'online' | 'offline'

  let current = $state<State>('connecting')
  let detail = $state('')

  const label: Record<State, string> = {
    connecting: 'Connecting',
    online: 'Online',
    offline: 'Offline',
  }

  $effect(() => {
    const ctrl = new AbortController()
    getHealth(ctrl.signal)
      .then((h) => {
        current = 'online'
        detail = `pisens ${h.version}`
      })
      .catch((err: unknown) => {
        if (ctrl.signal.aborted) return
        current = 'offline'
        detail = err instanceof Error ? err.message : String(err)
      })
    return () => ctrl.abort()
  })
</script>

<span class="status" data-state={current} title={detail} role="status">
  <span class="dot" aria-hidden="true"></span>
  {label[current]}
</span>

<style>
  .status {
    --dot: var(--color-unknown);

    display: inline-flex;
    align-items: center;
    gap: var(--space-2);
    font-size: 0.875rem;
    color: var(--color-text-muted);
    white-space: nowrap;
  }

  .status[data-state='online'] {
    --dot: var(--color-ok);
  }
  .status[data-state='offline'] {
    --dot: var(--color-critical);
  }

  .dot {
    width: 0.5rem;
    height: 0.5rem;
    border-radius: 50%;
    background: var(--dot);
  }
</style>
