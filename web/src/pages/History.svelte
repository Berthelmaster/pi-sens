<script lang="ts">
  import { onMount, untrack } from 'svelte'
  import { getSeriesKeys } from '../api'
  import Card from '../components/Card.svelte'
  import HistoryChart from '../components/HistoryChart.svelte'
  import {
    AUTO_REFRESH_MS,
    autoRefreshes,
    commonUnit,
    CUSTOM_MAX_SERIES,
    defaultGroups,
    describeStep,
    fromLocalInput,
    historyQuery,
    isAggregated,
    parseHistoryUrl,
    presetById,
    RANGE_PRESETS,
    resolveRange,
    toLocalInput,
    type PanelSeries,
    type RangeSelection,
  } from '../lib/history'
  import { HistoryQuery } from '../lib/historyQuery.svelte'

  // The range and the custom chart's series live in the URL so a link to the
  // page shows the same charts.
  const initial = parseHistoryUrl(location.search)
  let range = $state.raw<RangeSelection>(initial.range)
  let custom = $state.raw<string[]>(initial.custom)

  $effect(() => {
    const qs = historyQuery({ range, custom })
    if (location.search !== qs) {
      history.replaceState(history.state, '', location.pathname + qs)
    }
  })

  // Every stored series key, to pick the default charts and fill the custom
  // chart's picker. null until loaded.
  let keys = $state.raw<string[] | null>(null)
  let keysError = $state<string | null>(null)
  let keysCtrl: AbortController | undefined

  async function loadKeys() {
    keysCtrl?.abort()
    const ctrl = new AbortController()
    keysCtrl = ctrl
    keysError = null
    try {
      const list = await getSeriesKeys(ctrl.signal)
      if (!ctrl.signal.aborted) keys = list
    } catch (err) {
      if (!ctrl.signal.aborted) {
        keysError = err instanceof Error ? err.message : String(err)
      }
    }
  }

  const main = new HistoryQuery()
  const customQuery = new HistoryQuery()

  onMount(() => {
    loadKeys()
    return () => {
      keysCtrl?.abort()
      main.abort()
      customQuery.abort()
    }
  })

  let groups = $derived(keys ? defaultGroups(keys) : [])
  // As a string, so the charts are only refetched when the list changes.
  let groupKeys = $derived(
    groups
      .flatMap((g) => g.panels.flatMap((p) => p.series.map((s) => s.key)))
      .join(','),
  )
  let customKeys = $derived(custom.join(','))

  function split(list: string): string[] {
    return list ? list.split(',') : []
  }

  function now(): number {
    return Date.now() / 1000
  }

  // Refetch on a new range or a new set of series. The chart instances stay
  // and take the new data.
  $effect(() => {
    const list = split(groupKeys)
    const { from, to } = resolveRange(range, now())
    if (keys === null) return
    untrack(() => main.load(list, from, to))
  })

  $effect(() => {
    const list = split(customKeys)
    const { from, to } = resolveRange(range, now())
    untrack(() => customQuery.load(list, from, to))
  })

  // Short ranges move on their own. A hidden tab skips its turns.
  $effect(() => {
    if (!autoRefreshes(range)) return
    const sel = range
    const timer = setInterval(() => {
      if (document.hidden) return
      const { from, to } = resolveRange(sel, now())
      if (keys !== null) main.load(split(groupKeys), from, to, true)
      customQuery.load(split(customKeys), from, to, true)
    }, AUTO_REFRESH_MS)
    return () => clearInterval(timer)
  })

  // Range picker

  let customOpen = $state(initial.range.kind === 'custom')
  let fromInput = $state('')
  let toInput = $state('')
  let rangeError = $state<string | null>(null)

  function fillCustomInputs() {
    const { from, to } = resolveRange(range, now())
    fromInput = toLocalInput(from)
    toInput = toLocalInput(to)
  }
  if (initial.range.kind === 'custom') fillCustomInputs()

  function selectPreset(id: string) {
    range = { kind: 'preset', id }
    customOpen = false
    rangeError = null
  }

  function toggleCustom() {
    customOpen = !customOpen
    rangeError = null
    if (customOpen) fillCustomInputs()
  }

  function applyCustom(event: SubmitEvent) {
    event.preventDefault()
    const from = fromLocalInput(fromInput)
    const to = fromLocalInput(toInput)
    if (from === undefined || to === undefined) {
      rangeError = 'Enter both a start and an end.'
      return
    }
    if (from >= to) {
      rangeError = 'The start has to be before the end.'
      return
    }
    rangeError = null
    range = { kind: 'custom', from, to }
  }

  const dateTime = new Intl.DateTimeFormat(undefined, {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })

  let rangeLabel = $derived(
    range.kind === 'preset'
      ? `last ${presetById(range.id)?.label ?? range.id}`
      : `${dateTime.format(range.from * 1000)} – ${dateTime.format(range.to * 1000)}`,
  )

  let resolutionLabel = $derived.by(() => {
    const res = main.result ?? customQuery.result
    if (!res) return ''
    return isAggregated(res)
      ? `${describeStep(res.step)} buckets, shaded band shows min–max`
      : 'every sample'
  })

  // Custom chart

  let pick = $state('')

  /** Keys not on the custom chart yet, grouped by area ("cpu", "net", ...). */
  let pickable = $derived.by(() => {
    const areas = new Map<string, string[]>()
    for (const key of keys ?? []) {
      if (custom.includes(key)) continue
      const area = key.split('.')[0]
      let list = areas.get(area)
      if (!list) areas.set(area, (list = []))
      list.push(key)
    }
    return [...areas]
  })

  let customFull = $derived(custom.length >= CUSTOM_MAX_SERIES)

  function addSeries(event: SubmitEvent) {
    event.preventDefault()
    if (!pick || customFull || custom.includes(pick)) return
    custom = [...custom, pick]
    pick = ''
  }

  function removeSeries(key: string) {
    custom = custom.filter((k) => k !== key)
  }

  let customSeries = $derived<PanelSeries[]>(
    split(customKeys).map((key) => ({ key, label: key })),
  )
  let customUnit = $derived(commonUnit(custom))
  let mixedUnits = $derived(custom.length > 1 && customUnit === 'number')
</script>

<h1 class="visually-hidden">History</h1>

<div class="toolbar">
  <div class="presets" role="group" aria-label="Time range">
    {#each RANGE_PRESETS as preset (preset.id)}
      <button
        type="button"
        aria-pressed={range.kind === 'preset' && range.id === preset.id}
        onclick={() => selectPreset(preset.id)}
      >
        {preset.label}
      </button>
    {/each}
    <button
      type="button"
      aria-pressed={range.kind === 'custom'}
      aria-expanded={customOpen}
      aria-controls="custom-range"
      onclick={toggleCustom}
    >
      Custom
    </button>
  </div>

  <p class="summary muted" aria-live="polite">
    <span>Showing {rangeLabel}</span>
    {#if resolutionLabel}<span>{resolutionLabel}</span>{/if}
    {#if autoRefreshes(range)}<span>refreshes every 30 s</span>{/if}
    {#if main.loading || customQuery.loading}<span>loading…</span>{/if}
  </p>
</div>

{#if customOpen}
  <form id="custom-range" class="custom-range" onsubmit={applyCustom}>
    <label>
      <span>From</span>
      <input type="datetime-local" bind:value={fromInput} required />
    </label>
    <label>
      <span>To</span>
      <input type="datetime-local" bind:value={toInput} required />
    </label>
    <button type="submit" class="primary">Apply</button>
    {#if rangeError}
      <p class="error" role="alert">{rangeError}</p>
    {/if}
  </form>
{/if}

{#if keysError}
  <Card title="History">
    <p class="error" role="alert">
      Couldn't load the stored series: {keysError}
    </p>
    <button type="button" onclick={loadKeys}>Try again</button>
  </Card>
{:else if keys === null}
  <Card title="History">
    <p class="muted loading">Loading…</p>
  </Card>
{:else if keys.length === 0}
  <Card title="History">
    <p class="muted">
      No history has been recorded yet. Charts show up here once pisens has
      written its first samples to the history database.
    </p>
  </Card>
{:else}
  {#if main.error}
    <p class="error banner" role="alert">Couldn't load history: {main.error}</p>
  {/if}

  <div class="grid">
    {#each groups as group (group.id)}
      <Card title={group.title}>
        {#each group.panels as panel (panel.title)}
          <div class="panel">
            {#if group.panels.length > 1}
              <h3>{panel.title}</h3>
            {/if}
            <HistoryChart
              result={main.result}
              loading={main.loading}
              error={main.error}
              series={panel.series}
              unit={panel.unit}
              min={panel.min}
              max={panel.max}
              height={group.panels.length > 1 ? 160 : 220}
              label="{panel.title}, {rangeLabel}"
            />
          </div>
        {/each}
      </Card>
    {/each}

    <div class="full">
      <Card title="Custom chart">
        <form class="picker" onsubmit={addSeries}>
          <label class="visually-hidden" for="custom-series">Series</label>
          <select id="custom-series" bind:value={pick} disabled={customFull}>
            <option value="">
              {customFull
                ? `At most ${CUSTOM_MAX_SERIES} series`
                : 'Pick a series…'}
            </option>
            {#each pickable as [area, list] (area)}
              <optgroup label={area}>
                {#each list as key (key)}
                  <option value={key}>{key}</option>
                {/each}
              </optgroup>
            {/each}
          </select>
          <button type="submit" disabled={!pick || customFull}>Add</button>
        </form>

        {#if custom.length > 0}
          <ul class="chips" aria-label="Series on the custom chart">
            {#each custom as key (key)}
              <li>
                <span>{key}</span>
                <button
                  type="button"
                  aria-label="Remove {key}"
                  title="Remove {key}"
                  onclick={() => removeSeries(key)}>×</button
                >
              </li>
            {/each}
          </ul>
          {#if mixedUnits}
            <p class="muted note">
              These series have different units and share one axis.
            </p>
          {/if}
          {#if customQuery.error}
            <p class="error" role="alert">
              Couldn't load the custom chart: {customQuery.error}
            </p>
          {/if}
          <HistoryChart
            result={customQuery.result}
            loading={customQuery.loading}
            error={customQuery.error}
            series={customSeries}
            unit={customUnit}
            height={260}
            label="Custom chart, {rangeLabel}"
          />
        {:else}
          <p class="muted note">
            Pick any stored series to plot them together, up to {CUSTOM_MAX_SERIES}.
          </p>
        {/if}
      </Card>
    </div>
  </div>
{/if}

<style>
  .toolbar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-2) var(--space-4);
    margin-bottom: var(--space-4);
  }

  .presets {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-1);
  }

  button,
  select,
  input {
    min-height: 2.25rem;
    padding: var(--space-1) var(--space-3);
    border: 1px solid var(--color-border);
    border-radius: var(--radius);
    background: var(--color-surface);
    color: var(--color-text);
    font: inherit;
    font-size: 0.875rem;
  }

  button {
    cursor: pointer;
  }

  button:hover:not(:disabled) {
    background: var(--color-surface-raised);
  }

  button:disabled,
  select:disabled {
    cursor: default;
    opacity: 0.6;
  }

  button[aria-pressed='true'],
  button.primary {
    border-color: var(--color-accent);
    background: var(--color-accent);
    color: var(--color-accent-contrast);
  }

  button[aria-pressed='true']:hover,
  button.primary:hover {
    background: var(--color-accent);
  }

  button:focus-visible,
  select:focus-visible,
  input:focus-visible {
    outline: 2px solid var(--color-accent);
    outline-offset: 2px;
  }

  .presets button {
    min-width: 3rem;
    font-variant-numeric: tabular-nums;
  }

  .summary {
    display: flex;
    flex-wrap: wrap;
    gap: 0 var(--space-2);
    margin: 0;
    font-size: 0.8125rem;
  }

  .summary span + span::before {
    content: '·';
    margin-right: var(--space-2);
  }

  .custom-range {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-end;
    gap: var(--space-3);
    margin-bottom: var(--space-4);
    padding: var(--space-3) var(--space-4);
    background: var(--color-surface);
    border: 1px solid var(--color-border);
    border-radius: var(--radius);
  }

  .custom-range label {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
    font-size: 0.8125rem;
    color: var(--color-text-muted);
  }

  .custom-range input {
    color-scheme: inherit;
  }

  .custom-range .error {
    flex-basis: 100%;
    margin: 0;
  }

  /* Phones: the inputs take the full width, one under the other. */
  @media (max-width: 479px) {
    .custom-range label {
      flex: 1 1 100%;
    }

    .custom-range input {
      width: 100%;
    }
  }

  .grid {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    gap: var(--space-4);
  }

  @media (min-width: 1024px) {
    .grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }

  .full {
    grid-column: 1 / -1;
  }

  .panel + .panel {
    margin-top: var(--space-4);
  }

  h3 {
    margin: 0 0 var(--space-2);
    font-size: 0.8125rem;
    font-weight: 600;
    color: var(--color-text-muted);
  }

  .picker {
    display: flex;
    gap: var(--space-2);
    margin-bottom: var(--space-3);
  }

  .picker select {
    flex: 1;
    min-width: 0;
    max-width: 24rem;
  }

  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    margin: 0 0 var(--space-3);
    padding: 0;
    list-style: none;
  }

  .chips li {
    display: inline-flex;
    align-items: center;
    gap: var(--space-1);
    padding-left: var(--space-3);
    border: 1px solid var(--color-border);
    border-radius: 999px;
    background: var(--color-surface-raised);
    font-family: var(--font-mono);
    font-size: 0.8125rem;
    overflow-wrap: anywhere;
  }

  .chips button {
    min-height: 2rem;
    padding: 0 var(--space-3);
    border: 0;
    border-radius: 999px;
    background: none;
    color: var(--color-text-muted);
    font-size: 1rem;
    line-height: 1;
  }

  .chips button:hover {
    color: var(--color-text);
  }

  .note {
    margin: 0 0 var(--space-3);
    font-size: 0.875rem;
  }

  .error {
    color: var(--color-critical);
  }

  .banner {
    margin: 0 0 var(--space-4);
  }
</style>
