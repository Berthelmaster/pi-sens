<script lang="ts">
  import Card, { type CardStatus } from '../components/Card.svelte'
  import { snapshot } from '../lib/live'
  import {
    servicesSection,
    servicesStatus,
    shortUnitName,
    unitList,
    watchBadge,
    type ServicesSection,
  } from '../types/services'
  import { isCollectorError } from '../types/snapshot'

  let section = $derived(servicesSection($snapshot))
  let error = $derived(isCollectorError(section) ? section.error : null)
  let data = $derived<Partial<ServicesSection> | undefined>(
    section && !isCollectorError(section) ? section : undefined,
  )

  // Without systemctl or systemd (a container) there is nothing to show.
  let hidden = $derived(data?.available === false)

  let failedUnits = $derived(unitList(data?.failed_units))
  let watched = $derived(unitList(data?.watched))
  let failedCount = $derived(
    typeof data?.failed === 'number' ? data.failed : failedUnits.length,
  )

  // Keep the card neutral until the first snapshot arrives.
  let status = $derived<CardStatus | undefined>(
    !$snapshot
      ? undefined
      : error
        ? 'unknown'
        : data
          ? servicesStatus(data)
          : undefined,
  )

  function count(v: unknown): string {
    return typeof v === 'number' && Number.isFinite(v) ? String(v) : '–'
  }
</script>

{#if !hidden}
  <Card title="Services" {status}>
    {#if error}
      <p class="muted">Services unavailable: {error}</p>
    {:else if !$snapshot}
      <p class="muted">Waiting for data…</p>
    {:else if !data}
      <p class="muted">The latest snapshot has no service data.</p>
    {:else}
      <dl class="counts">
        <div>
          <dt>Active</dt>
          <dd class="num">{count(data.active)}</dd>
        </div>
        <div>
          <dt>Failed</dt>
          <dd class="num" class:bad={failedCount > 0}>
            {count(failedCount)}
          </dd>
        </div>
        <div>
          <dt>Total</dt>
          <dd class="num">{count(data.total)}</dd>
        </div>
      </dl>

      {#if failedUnits.length > 0}
        <h3>Failed</h3>
        <ul class="failed">
          {#each failedUnits as u (u.unit)}
            <li title={u.description || u.unit}>
              <span class="name">{u.unit}</span>
              {#if u.description}
                <span class="desc">{u.description}</span>
              {/if}
            </li>
          {/each}
        </ul>
      {/if}

      {#if watched.length > 0}
        <h3>Watched</h3>
        <ul class="watched">
          {#each watched as u (u.unit)}
            {@const badge = watchBadge(u)}
            <li>
              <span class="name" title={u.description || u.unit}>
                {shortUnitName(u.unit)}
              </span>
              <span
                class="badge"
                data-state={badge.state}
                title={u.sub ? `${u.active} (${u.sub})` : u.active}
              >
                {badge.label}
              </span>
            </li>
          {/each}
        </ul>
      {/if}
    {/if}
  </Card>
{/if}

<style>
  h3 {
    margin: var(--space-4) 0 var(--space-2);
    font-size: 0.75rem;
    font-weight: 600;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--color-text-muted);
  }

  .counts {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: var(--space-3) var(--space-4);
    margin: 0;
  }

  dt {
    color: var(--color-text-muted);
    font-size: 0.75rem;
  }

  dd {
    margin: 0;
    font-size: 1.5rem;
    font-weight: 600;
    line-height: 1.2;
  }

  .num {
    font-variant-numeric: tabular-nums;
  }

  .bad {
    color: var(--color-critical);
  }

  ul {
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .failed li {
    display: flex;
    flex-direction: column;
    padding: var(--space-1) 0;
    color: var(--color-critical);
    min-width: 0;
  }

  .failed .name {
    font-weight: 600;
    overflow-wrap: anywhere;
  }

  .failed .desc {
    font-size: 0.75rem;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .watched li {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-2);
    padding: var(--space-1) 0;
    min-width: 0;
  }

  .watched .name {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .badge {
    --badge-color: var(--color-unknown);

    flex-shrink: 0;
    padding: 0 var(--space-2);
    border: 1px solid var(--badge-color);
    border-radius: 999px;
    color: var(--badge-color);
    background: color-mix(in srgb, var(--badge-color) 12%, transparent);
    font-size: 0.75rem;
    font-weight: 600;
    line-height: 1.25rem;
  }

  .badge[data-state='active'] {
    --badge-color: var(--color-ok);
  }
  .badge[data-state='failed'] {
    --badge-color: var(--color-critical);
  }
  .badge[data-state='inactive'] {
    --badge-color: var(--color-warning);
  }
</style>
