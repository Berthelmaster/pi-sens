<script lang="ts">
  // Nav entry for the alerts: a count badge of the firing alerts, coloured by
  // the most severe one. It opens the recent alerts in the banner. Hidden
  // when the backend has alerts turned off.
  import { alertPanel } from '../lib/alertPanel.svelte'
  import { alerts } from '../lib/live'
  import { worstSeverity } from '../types/alerts'

  let firing = $derived($alerts?.firing ?? [])
  let severity = $derived(worstSeverity(firing))
  let label = $derived(
    firing.length === 0
      ? 'Alerts, none firing'
      : `Alerts, ${firing.length} firing`,
  )

  function toggle() {
    alertPanel.recentOpen = !alertPanel.recentOpen
    // The banner sits at the top of the page, under the sticky header.
    if (alertPanel.recentOpen) window.scrollTo(0, 0)
  }
</script>

{#if $alerts}
  <button
    type="button"
    class="alerts"
    aria-expanded={alertPanel.recentOpen}
    aria-label={label}
    title={label}
    onclick={toggle}
  >
    Alerts
    {#if firing.length > 0}
      <span class="badge" data-severity={severity} aria-hidden="true">
        {firing.length}
      </span>
    {/if}
  </button>
{/if}

<style>
  /* Looks like the nav links next to it. */
  .alerts {
    display: inline-flex;
    align-items: center;
    gap: var(--space-2);
    padding: var(--space-2) var(--space-3);
    border: 0;
    border-bottom: 2px solid transparent;
    background: transparent;
    color: var(--color-text-muted);
    font: inherit;
    font-weight: 500;
    cursor: pointer;
  }

  .alerts:hover,
  .alerts[aria-expanded='true'] {
    color: var(--color-text);
  }

  .alerts:focus-visible {
    outline: 2px solid var(--color-accent);
    outline-offset: -2px;
  }

  .badge {
    min-width: 1.25rem;
    padding: 0 var(--space-1);
    border-radius: 999px;
    background: var(--color-warning);
    /* The page background reads well on both status colours in both themes. */
    color: var(--color-bg);
    font-size: 0.75rem;
    font-weight: 700;
    line-height: 1.25rem;
    text-align: center;
  }

  .badge[data-severity='critical'] {
    background: var(--color-critical);
  }

  @media (min-width: 640px) {
    .alerts {
      padding: var(--space-4) var(--space-3);
    }
  }
</style>
