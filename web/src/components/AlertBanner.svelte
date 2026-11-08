<script lang="ts">
  // Banner under the header with the firing alerts, critical ones in red and
  // warnings in amber. With more than a couple firing it collapses to the
  // most severe one. The recent (resolved) alerts open from here or from the
  // nav button; while they are open the banner stays even with nothing firing.
  import { alertPanel } from '../lib/alertPanel.svelte'
  import { alerts, snapshot } from '../lib/live'
  import {
    alertKey,
    describeCondition,
    describeRule,
    firingSince,
    resolvedSummary,
    worstSeverity,
  } from '../types/alerts'

  /** More firing alerts than this make the list collapsible. */
  const COLLAPSE_AFTER = 2
  /** How many resolved alerts the recent list shows. */
  const RECENT_LIMIT = 10

  let firing = $derived($alerts?.firing ?? [])
  let resolved = $derived(($alerts?.resolved ?? []).slice(0, RECENT_LIMIT))
  let collapsible = $derived(firing.length > COLLAPSE_AFTER)
  let shown = $derived(
    collapsible && !alertPanel.expanded ? firing.slice(0, 1) : firing,
  )
  let visible = $derived(
    $alerts !== null && (firing.length > 0 || alertPanel.recentOpen),
  )
  let severity = $derived(worstSeverity(firing))
  let announcement = $derived.by(() => {
    if (firing.length === 0) return ''
    const crit = firing.filter((a) => a.severity === 'critical').length
    const n = `${firing.length} ${firing.length === 1 ? 'alert' : 'alerts'} firing`
    return crit > 0 ? `${n}, ${crit} critical` : n
  })

  // Ticks every second while shown so the "since" durations stay current.
  let now = $state(Date.now())
  $effect(() => {
    if (!visible) return
    now = Date.now()
    const timer = setInterval(() => (now = Date.now()), 1000)
    return () => clearInterval(timer)
  })
</script>

<!-- Kept outside the banner so screen readers hear alerts come and go. -->
<p class="visually-hidden" aria-live="polite">{announcement}</p>

{#if visible}
  <section
    id="alerts"
    class="banner"
    data-severity={severity ?? 'none'}
    aria-label="Alerts"
  >
    <div class="inner">
      {#if firing.length > 0}
        <ul class="firing" id="firing-alerts">
          {#each shown as alert (alertKey(alert))}
            <li class="alert" data-severity={alert.severity}>
              <span class="severity">{alert.severity}</span>
              <span class="rule">{alert.rule}</span>
              <span class="condition">
                {describeCondition(
                  alert,
                  $snapshot?.series[alert.series] ?? alert.value,
                )}
              </span>
              <span class="since" title={alert.firing_at}>
                {firingSince(alert, now)}
              </span>
            </li>
          {/each}
        </ul>
      {:else}
        <p class="none">No alerts firing.</p>
      {/if}

      <div class="actions">
        {#if collapsible}
          <button
            type="button"
            aria-expanded={alertPanel.expanded}
            aria-controls="firing-alerts"
            onclick={() => (alertPanel.expanded = !alertPanel.expanded)}
          >
            {alertPanel.expanded
              ? 'Show fewer'
              : `Show all ${firing.length} firing`}
          </button>
        {/if}
        <button
          type="button"
          aria-expanded={alertPanel.recentOpen}
          aria-controls="recent-alerts"
          onclick={() => (alertPanel.recentOpen = !alertPanel.recentOpen)}
        >
          {alertPanel.recentOpen ? 'Hide recent alerts' : 'Recent alerts'}
        </button>
      </div>

      {#if alertPanel.recentOpen}
        <div class="recent" id="recent-alerts">
          <h2>Recent alerts</h2>
          {#if resolved.length === 0}
            <p class="muted">Nothing has resolved yet.</p>
          {:else}
            <ul>
              {#each resolved as alert (alertKey(alert))}
                <li class="alert" data-severity={alert.severity}>
                  <span class="severity">{alert.severity}</span>
                  <span class="rule">{alert.rule}</span>
                  <span class="condition">{describeRule(alert)}</span>
                  <span class="since" title={alert.resolved_at}>
                    {resolvedSummary(alert, now)}
                  </span>
                </li>
              {/each}
            </ul>
          {/if}
        </div>
      {/if}
    </div>
  </section>
{/if}

<style>
  .banner {
    --tone: var(--color-text-muted);

    padding: 0 var(--safe-right) 0 var(--safe-left);
    background: color-mix(in srgb, var(--tone) 10%, var(--color-surface));
    border-bottom: 1px solid
      color-mix(in srgb, var(--tone) 45%, var(--color-border));
  }

  .banner[data-severity='critical'] {
    --tone: var(--color-critical);
  }
  .banner[data-severity='warning'] {
    --tone: var(--color-warning);
  }

  .inner {
    display: grid;
    gap: var(--space-2);
    max-width: var(--content-max-width);
    margin: 0 auto;
    padding: var(--space-3) var(--space-4);
  }

  ul {
    display: grid;
    gap: var(--space-1);
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .alert {
    --tone: var(--color-warning);

    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: var(--space-1) var(--space-3);
    padding: var(--space-1) var(--space-3);
    border-left: 3px solid var(--tone);
    font-size: 0.875rem;
  }

  .alert[data-severity='critical'] {
    --tone: var(--color-critical);
  }

  .severity {
    color: var(--tone);
    font-size: 0.75rem;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }

  .rule {
    font-weight: 600;
  }

  .condition {
    font-family: var(--font-mono);
    color: var(--color-text-muted);
  }

  .since {
    color: var(--color-text-muted);
    white-space: nowrap;
  }

  .none {
    margin: 0;
    font-size: 0.875rem;
    color: var(--color-text-muted);
  }

  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
  }

  button {
    padding: var(--space-1) var(--space-3);
    border: 1px solid var(--color-border);
    border-radius: var(--radius);
    background: var(--color-surface);
    color: var(--color-text);
    font: inherit;
    font-size: 0.8125rem;
    cursor: pointer;
  }

  button:hover {
    background: var(--color-surface-raised);
  }

  button:focus-visible {
    outline: 2px solid var(--color-accent);
    outline-offset: 2px;
  }

  .recent {
    padding-top: var(--space-2);
    border-top: 1px solid var(--color-border);
  }

  .recent .alert {
    opacity: 0.85;
  }

  h2 {
    margin: 0 0 var(--space-2);
    font-size: 0.875rem;
    font-weight: 600;
  }

  .recent p {
    margin: 0;
    font-size: 0.875rem;
  }

  @media (min-width: 1024px) {
    .inner {
      padding: var(--space-3) var(--space-6);
    }
  }
</style>
