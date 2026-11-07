<script lang="ts" module>
  export type CardStatus = 'ok' | 'warning' | 'critical' | 'unknown'
</script>

<script lang="ts">
  import type { Snippet } from 'svelte'

  interface Props {
    title: string
    /** Colours the card's top edge and title dot. Leave out for a neutral card. */
    status?: CardStatus
    children?: Snippet
  }

  let { title, status, children }: Props = $props()

  const statusLabel: Record<CardStatus, string> = {
    ok: 'OK',
    warning: 'Warning',
    critical: 'Critical',
    unknown: 'Unknown',
  }
</script>

<section class="card" data-status={status}>
  <header>
    {#if status}
      <span class="dot" role="img" aria-label={statusLabel[status]}></span>
    {/if}
    <h2>{title}</h2>
  </header>
  <div class="body">
    {@render children?.()}
  </div>
</section>

<style>
  .card {
    --status-color: transparent;

    display: flex;
    flex-direction: column;
    height: 100%;
    min-width: 0;
    background: var(--color-surface);
    border: 1px solid var(--color-border);
    border-top: 3px solid var(--status-color);
    border-radius: var(--radius);
    box-shadow: var(--shadow);
  }

  .card[data-status='ok'] {
    --status-color: var(--color-ok);
  }
  .card[data-status='warning'] {
    --status-color: var(--color-warning);
  }
  .card[data-status='critical'] {
    --status-color: var(--color-critical);
  }
  .card[data-status='unknown'] {
    --status-color: var(--color-unknown);
  }

  header {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    padding: var(--space-3) var(--space-4) 0;
  }

  h2 {
    margin: 0;
    font-size: 0.8125rem;
    font-weight: 600;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--color-text-muted);
  }

  .dot {
    width: 0.5rem;
    height: 0.5rem;
    border-radius: 50%;
    background: var(--status-color);
    flex-shrink: 0;
  }

  .body {
    flex: 1;
    padding: var(--space-3) var(--space-4) var(--space-4);
    min-width: 0;
  }

  .body > :global(:first-child) {
    margin-top: 0;
  }
  .body > :global(:last-child) {
    margin-bottom: 0;
  }
</style>
