<script lang="ts">
  import Card from '../components/Card.svelte'
  import { formatBytes, formatPercent } from '../lib/format'
  import { snapshot } from '../lib/live'
  import {
    DEFAULT_SORT,
    nextSort,
    processesSection,
    processStateName,
    sortProcesses,
    type ProcessInfo,
    type ProcessSort,
    type ProcessSortKey,
  } from '../types/processes'
  import { isCollectorError } from '../types/snapshot'

  let sort = $state<ProcessSort>(DEFAULT_SORT)

  let section = $derived(processesSection($snapshot))
  let error = $derived(isCollectorError(section) ? section.error : null)
  let data = $derived(section && !isCollectorError(section) ? section : null)
  let list = $derived<ProcessInfo[]>(
    Array.isArray(data?.processes) ? data.processes : [],
  )
  let rows = $derived(sortProcesses(list, sort))

  const columns: { key: ProcessSortKey; label: string }[] = [
    { key: 'cpu', label: 'CPU %' },
    { key: 'mem', label: 'Memory' },
  ]

  function ariaSort(key: ProcessSortKey) {
    if (sort.key !== key) return 'none'
    return sort.dir === 'desc' ? 'descending' : 'ascending'
  }
</script>

{#snippet sortHeader(key: ProcessSortKey, label: string)}
  <th class="num" scope="col" aria-sort={ariaSort(key)}>
    <button
      type="button"
      class:active={sort.key === key}
      onclick={() => (sort = nextSort(sort, key))}
    >
      {label}
      <span class="arrow" aria-hidden="true">
        {sort.key !== key ? '↕' : sort.dir === 'desc' ? '↓' : '↑'}
      </span>
    </button>
  </th>
{/snippet}

<Card title="Processes" status={error ? 'unknown' : undefined}>
  {#if error}
    <p class="muted">Processes unavailable: {error}</p>
  {:else if !$snapshot}
    <p class="muted">Waiting for data…</p>
  {:else if !data}
    <p class="muted">The latest snapshot has no process data.</p>
  {:else if rows.length === 0}
    <p class="muted">No processes to show.</p>
  {:else}
    <div class="scroll">
      <table>
        <thead>
          <tr>
            <th class="num" scope="col">PID</th>
            <th scope="col">User</th>
            <th class="name" scope="col">Name</th>
            {#each columns as c (c.key)}
              {@render sortHeader(c.key, c.label)}
            {/each}
            <th class="state" scope="col">State</th>
          </tr>
        </thead>
        <tbody>
          {#each rows as p (p.pid)}
            <tr>
              <td class="num">{p.pid}</td>
              <td class="user" title={p.user}>{p.user}</td>
              <td class="name" title={p.command}>{p.name}</td>
              <td class="num">{formatPercent(p.cpu_pct)}</td>
              <td class="num">{formatBytes(p.rss_bytes)}</td>
              <td class="state" title={processStateName(p.state)}>
                {p.state}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
    <p class="muted footer">
      Top {rows.length} of {data.count} processes by CPU and memory. CPU % is of one
      core.
    </p>
  {/if}
</Card>

<style>
  .scroll {
    overflow-x: auto;
    /* Let the table scroll inside the card instead of widening the page. */
    max-width: 100%;
  }

  table {
    width: 100%;
    min-width: 32rem;
    border-collapse: collapse;
    font-size: 0.875rem;
  }

  th,
  td {
    padding: var(--space-1) var(--space-2);
    text-align: left;
    white-space: nowrap;
  }

  th:first-child,
  td:first-child {
    padding-left: 0;
  }

  th:last-child,
  td:last-child {
    padding-right: 0;
  }

  th {
    font-size: 0.75rem;
    font-weight: 600;
    color: var(--color-text-muted);
    border-bottom: 1px solid var(--color-border);
  }

  tbody tr + tr td {
    border-top: 1px solid var(--color-border);
  }

  tbody tr:hover td {
    background: var(--color-surface-raised);
  }

  .num {
    text-align: right;
    font-variant-numeric: tabular-nums;
  }

  /* Fixed widths keep the columns still while the values change. */
  th.num,
  td.num {
    width: 6.5rem;
  }

  .user {
    max-width: 8rem;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  /* The name takes the space that is left. */
  .name {
    width: 100%;
    max-width: 0;
    min-width: 8rem;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  td.name {
    font-family: var(--font-mono);
    font-size: 0.8125rem;
  }

  .state {
    width: 3rem;
    text-align: center;
  }

  th button {
    display: inline-flex;
    align-items: center;
    gap: var(--space-1);
    padding: 0;
    border: 0;
    background: none;
    font: inherit;
    color: inherit;
    cursor: pointer;
  }

  th button:hover,
  th button.active {
    color: var(--color-text);
  }

  th button:focus-visible {
    outline: 2px solid var(--color-accent);
    outline-offset: 2px;
    border-radius: 2px;
  }

  .arrow {
    display: inline-block;
    width: 0.75em;
    opacity: 0.6;
  }

  th button.active .arrow {
    opacity: 1;
  }

  .footer {
    margin: var(--space-2) 0 0;
    font-size: 0.8125rem;
  }
</style>
