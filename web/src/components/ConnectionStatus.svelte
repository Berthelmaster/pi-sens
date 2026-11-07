<script lang="ts">
  // Header slot for the live stream state: a coloured dot and label, with
  // the time since the last update in the tooltip.
  import { formatDuration } from '../lib/format'
  import { connection, type ConnectionState } from '../lib/live'

  const label: Record<ConnectionState, string> = {
    connecting: 'Connecting',
    live: 'Live',
    reconnecting: 'Reconnecting',
    offline: 'Offline',
  }

  // Ticks every second so the "ago" text in the tooltip stays current.
  let now = $state(Date.now())
  $effect(() => {
    const timer = setInterval(() => (now = Date.now()), 1000)
    return () => clearInterval(timer)
  })

  function since(t: number): string {
    return formatDuration(Math.max(0, (now - t) / 1000))
  }

  let detail = $derived.by(() => {
    const { state, lastUpdate, retryAt } = $connection
    const parts = [label[state]]
    if (retryAt !== null && retryAt > now) {
      parts.push(
        `retrying in ${formatDuration(Math.ceil((retryAt - now) / 1000))}`,
      )
    }
    parts.push(
      lastUpdate === null
        ? 'no data yet'
        : `last update ${since(lastUpdate)} ago`,
    )
    return parts.join(' · ')
  })
</script>

<span
  class="status"
  data-state={$connection.state}
  title={detail}
  role="status"
>
  <span class="dot" aria-hidden="true"></span>
  {label[$connection.state]}
</span>

<style>
  .status {
    --dot: var(--color-warning);

    display: inline-flex;
    align-items: center;
    gap: var(--space-2);
    font-size: 0.875rem;
    color: var(--color-text-muted);
    white-space: nowrap;
  }

  .status[data-state='live'] {
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
