<script lang="ts">
  // Bars of the energy used per day from /api/energy/daily, with the
  // loading, empty and error states in its place while there is nothing to
  // draw. Hovering a day shows its kWh and, with a price set, its cost.
  import type { EnergyDailyResponse } from '../api'
  import {
    formatCost,
    formatKWh,
    hasEnergyData,
    labelEvery,
    niceTicks,
    parseDate,
    tickDecimals,
  } from '../lib/energy'

  interface Props {
    data: EnergyDailyResponse | null
    loading: boolean
    error?: string | null
    /** Height of the plot in CSS pixels. The width follows the container. */
    height?: number
    /** Text for screen readers and the table caption. */
    label: string
  }

  let { data, loading, error = null, height = 220, label }: Props = $props()

  const uid = $props.id()

  // Room for the axis labels around the bars.
  const PAD_LEFT = 44
  const PAD_RIGHT = 4
  const PAD_TOP = 18
  const PAD_BOTTOM = 22
  /**
   * Room for a date label ("Nov 30" is about 40 px) with enough air that
   * labels never touch, even next to today's label pulled in at the edge.
   */
  const LABEL_WIDTH = 56
  /** Rounded data end of a bar. */
  const RADIUS = 4

  const dayFormat = new Intl.DateTimeFormat(undefined, {
    month: 'short',
    day: 'numeric',
  })
  const longFormat = new Intl.DateTimeFormat(undefined, {
    weekday: 'short',
    month: 'short',
    day: 'numeric',
  })

  let width = $state(0)
  let active = $state<number | null>(null)
  let showTable = $state(false)

  let days = $derived(data?.days ?? [])
  let currency = $derived(data?.currency)
  let priced = $derived(data?.price !== undefined)
  let shown = $derived(data !== null && hasEnergyData(days))

  let ticks = $derived(
    niceTicks(
      Math.max(0, ...days.map((d) => d.kwh ?? 0)),
      height < 180 ? 3 : 4,
    ),
  )
  let top = $derived(ticks[ticks.length - 1])
  let decimals = $derived(tickDecimals(ticks[1] - ticks[0]))

  let plotW = $derived(Math.max(0, width - PAD_LEFT - PAD_RIGHT))
  let plotH = $derived(Math.max(0, height - PAD_TOP - PAD_BOTTOM))
  let slot = $derived(days.length ? plotW / days.length : 0)
  // Bars fill most of their slot with at least a 2px gap between them.
  let gap = $derived(Math.max(2, slot * 0.25))
  let barW = $derived(Math.max(1, slot - gap))
  let every = $derived(labelEvery(slot, LABEL_WIDTH))

  function y(v: number): number {
    return PAD_TOP + plotH - (top > 0 ? (v / top) * plotH : 0)
  }

  /** A bar from the baseline up to v, rounded at the top only. */
  function barPath(i: number, v: number): string {
    const x = PAD_LEFT + i * slot + (slot - barW) / 2
    const base = PAD_TOP + plotH
    const h = base - y(v)
    if (h <= 0) return ''
    const r = Math.min(RADIUS, barW / 2, h)
    const t = base - h
    return (
      `M${x},${base}V${t + r}Q${x},${t} ${x + r},${t}` +
      `H${x + barW - r}Q${x + barW},${t} ${x + barW},${t + r}V${base}Z`
    )
  }

  function dayAt(clientX: number, target: HTMLElement): number | null {
    const x = clientX - target.getBoundingClientRect().left - PAD_LEFT
    if (slot <= 0 || x < 0 || x >= plotW) return null
    return Math.min(days.length - 1, Math.floor(x / slot))
  }

  function onPointer(event: PointerEvent) {
    active = dayAt(event.clientX, event.currentTarget as HTMLElement)
  }

  let isLast = (i: number) => i === days.length - 1

  // The tooltip sits above the hovered bar and flips to its left in the
  // right half so it stays inside the card.
  let tip = $derived.by(() => {
    if (active === null || !days[active]) return null
    const d = days[active]
    return {
      day: d,
      left: PAD_LEFT + (active + 0.5) * slot,
      top: Math.max(PAD_TOP, y(d.kwh ?? 0)),
      flip: active > days.length / 2,
      today: isLast(active),
    }
  })

  let total = $derived(days.reduce((sum, d) => sum + (d.kwh ?? 0), 0))
  let totalCost = $derived(
    priced ? days.reduce((sum, d) => sum + (d.cost ?? 0), 0) : undefined,
  )
</script>

<figure class="energy-chart" class:stale={loading} aria-busy={loading}>
  {#if shown}
    <div class="toolbar">
      <label>
        <input type="checkbox" bind:checked={showTable} />
        Show as table
      </label>
    </div>

    <!-- Hovering is a visual aid only; the table has every value. -->
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div
      class="plot"
      hidden={showTable}
      bind:clientWidth={width}
      style:height="{height}px"
      onpointermove={onPointer}
      onpointerdown={onPointer}
      onpointerleave={() => (active = null)}
    >
      {#if width > 0}
        <svg {width} {height} role="img" aria-label={label}>
          <text class="axis unit" x={PAD_LEFT - 6} y={10} text-anchor="end"
            >kWh</text
          >
          {#each ticks as t (t)}
            <line
              class="grid"
              x1={PAD_LEFT}
              x2={width - PAD_RIGHT}
              y1={y(t)}
              y2={y(t)}
            />
            <text
              class="axis"
              x={PAD_LEFT - 6}
              y={y(t)}
              dy="0.32em"
              text-anchor="end">{t.toFixed(decimals)}</text
            >
          {/each}

          {#if active !== null}
            <rect
              class="cursor"
              x={PAD_LEFT + active * slot}
              y={PAD_TOP}
              width={slot}
              height={plotH}
            />
          {/if}

          {#each days as d, i (d.date)}
            {#if d.kwh !== null}
              <path
                class="bar"
                class:partial={isLast(i)}
                d={barPath(i, d.kwh)}
              />
            {/if}
            {#if (days.length - 1 - i) % every === 0}
              <!-- Today's label ends at the plot's edge when centring it
                   under a narrow bar would cut it off. -->
              <text
                class="axis"
                x={isLast(i) && slot < LABEL_WIDTH
                  ? width - PAD_RIGHT
                  : PAD_LEFT + (i + 0.5) * slot}
                y={height - 6}
                text-anchor={isLast(i) && slot < LABEL_WIDTH ? 'end' : 'middle'}
                >{dayFormat.format(parseDate(d.date))}</text
              >
            {/if}
          {/each}
        </svg>
      {/if}

      {#if tip}
        <div
          class="tooltip"
          class:flip={tip.flip}
          style:left="{tip.left}px"
          style:top="{tip.top}px"
          aria-hidden="true"
        >
          <div class="time">
            {longFormat.format(parseDate(tip.day.date))}{tip.today
              ? ', so far'
              : ''}
          </div>
          {#if tip.day.kwh === null}
            <div class="row"><span class="name">No data</span></div>
          {:else}
            <div class="row">
              <span class="swatch"></span>
              <span class="name">Energy</span>
              <span class="value">{formatKWh(tip.day.kwh)}</span>
            </div>
            {#if tip.day.cost !== undefined}
              <div class="row">
                <span class="swatch none"></span>
                <span class="name">Cost</span>
                <span class="value">{formatCost(tip.day.cost, currency)}</span>
              </div>
            {/if}
          {/if}
        </div>
      {/if}
    </div>

    {#if showTable}
      <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
      <div
        class="table-scroll"
        role="region"
        aria-labelledby="{uid}-caption"
        tabindex="0"
        style:max-height="{height}px"
      >
        <table>
          <caption id="{uid}-caption">{label}</caption>
          <thead>
            <tr>
              <th scope="col">Day</th>
              <th class="num" scope="col">Energy</th>
              {#if priced}<th class="num" scope="col">Cost</th>{/if}
            </tr>
          </thead>
          <tbody>
            {#each days as d, i (d.date)}
              <tr>
                <th scope="row">
                  <time datetime={d.date}>
                    {longFormat.format(parseDate(d.date))}{isLast(i)
                      ? ', so far'
                      : ''}
                  </time>
                </th>
                <td class="num"
                  >{d.kwh === null ? 'No data' : formatKWh(d.kwh)}</td
                >
                {#if priced}
                  <td class="num">
                    {d.cost === undefined ? '–' : formatCost(d.cost, currency)}
                  </td>
                {/if}
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}

    <figcaption class="muted total">
      {days.length} days: {formatKWh(total)}{totalCost !== undefined
        ? `, ${formatCost(totalCost, currency)}`
        : ''}
    </figcaption>
  {:else}
    <div class="placeholder" style:height="{height}px">
      {#if loading || (!data && !error)}
        <span class="spinner" aria-hidden="true"></span> Loading…
      {:else if error && !data}
        Couldn't load the energy per day.
      {:else}
        No energy use recorded yet. The days fill in once pisens has stored
        energy readings, which need a Raspberry Pi 5.
      {/if}
    </div>
  {/if}
</figure>

<style>
  .energy-chart {
    margin: 0;
    min-width: 0;
  }

  svg {
    display: block;
    transition: opacity 0.15s;
  }

  .stale svg,
  .stale .table-scroll {
    opacity: 0.5;
  }

  .toolbar {
    display: flex;
    justify-content: flex-end;
    margin-bottom: var(--space-2);
    color: var(--color-text-muted);
    font-size: 0.8125rem;
  }

  .toolbar label {
    display: inline-flex;
    align-items: center;
    gap: var(--space-2);
    cursor: pointer;
  }

  .toolbar input {
    margin: 0;
    accent-color: var(--color-accent);
  }

  .plot {
    position: relative;
    touch-action: pan-y;
  }

  .grid {
    stroke: var(--chart-grid);
    stroke-width: 1;
    shape-rendering: crispEdges;
  }

  .axis {
    fill: var(--chart-axis);
    font-size: 12px;
    font-variant-numeric: tabular-nums;
  }

  .cursor {
    fill: var(--chart-grid);
    opacity: 0.6;
  }

  .bar {
    fill: var(--chart-1);
  }

  /* Today is still counting. */
  .bar.partial {
    fill-opacity: 0.55;
  }

  .tooltip {
    position: absolute;
    z-index: 1;
    transform: translate(12px, -50%);
    min-width: 8rem;
    padding: var(--space-2) var(--space-3);
    background: var(--color-surface-raised);
    border: 1px solid var(--color-border);
    border-radius: var(--radius);
    box-shadow: var(--shadow);
    color: var(--color-text);
    font-size: 0.8125rem;
    line-height: 1.4;
    pointer-events: none;
    white-space: nowrap;
  }

  .tooltip.flip {
    transform: translate(calc(-100% - 12px), -50%);
  }

  .time {
    margin-bottom: var(--space-1);
    color: var(--color-text-muted);
  }

  .row {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }

  .name {
    flex: 1;
    color: var(--color-text-muted);
  }

  .value {
    font-variant-numeric: tabular-nums;
    font-weight: 600;
  }

  .swatch {
    flex-shrink: 0;
    width: 0.625rem;
    height: 0.625rem;
    border-radius: 2px;
    background: var(--chart-1);
  }

  .swatch.none {
    background: none;
  }

  .total {
    margin-top: var(--space-2);
    font-size: 0.8125rem;
  }

  .table-scroll {
    overflow: auto;
    max-width: 100%;
  }

  .table-scroll:focus-visible {
    outline: 2px solid var(--color-accent);
    outline-offset: 2px;
    border-radius: 2px;
  }

  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 0.8125rem;
  }

  caption {
    padding-bottom: var(--space-2);
    text-align: left;
    color: var(--color-text);
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

  thead th {
    position: sticky;
    top: 0;
    background: var(--color-surface);
    border-bottom: 1px solid var(--color-border);
    color: var(--color-text-muted);
    font-size: 0.75rem;
    font-weight: 600;
  }

  tbody th {
    font-weight: normal;
    color: var(--color-text-muted);
  }

  tbody tr + tr > * {
    border-top: 1px solid var(--color-border);
  }

  .num {
    text-align: right;
    font-variant-numeric: tabular-nums;
  }

  .placeholder {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: var(--space-2);
    padding: 0 var(--space-4);
    border: 1px dashed var(--color-border);
    border-radius: var(--radius);
    color: var(--color-text-muted);
    font-size: 0.875rem;
    text-align: center;
  }

  .spinner {
    width: 0.875rem;
    height: 0.875rem;
    border: 2px solid var(--color-border);
    border-top-color: var(--color-text-muted);
    border-radius: 50%;
    animation: spin 0.8s linear infinite;
  }

  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .spinner {
      animation: none;
    }
  }
</style>
