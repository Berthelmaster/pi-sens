<script lang="ts">
  import Card, { type CardStatus } from '../components/Card.svelte'
  import Sparkline from '../components/Sparkline.svelte'
  import { num } from '../lib/format'
  import {
    seriesHistory,
    snapshot as liveSnapshot,
    type SeriesHistory,
  } from '../lib/live'
  import {
    energySection,
    formatCost,
    formatEnergy,
    formatSince,
    type EnergySection,
  } from '../types/energy'
  import { formatWatts, POWER_SERIES } from '../types/pihealth'
  import { isCollectorError, type Snapshot } from '../types/snapshot'

  interface Props {
    /**
     * Shows this snapshot instead of the live one. Only for the dev page
     * (/dev/energy); the Overview leaves it out.
     */
    snap?: Snapshot | null
    /** The power history for the sparkline in place of the live one. */
    history?: SeriesHistory
  }

  let { snap, history }: Props = $props()

  // The energy section only has the latest power reading; the 5-minute line
  // comes from the power collector's series, which feeds the same meter.
  const livePower = seriesHistory(POWER_SERIES)

  let current = $derived(snap === undefined ? $liveSnapshot : snap)
  let power = $derived(history ?? $livePower)

  let raw = $derived(energySection(current))
  let error = $derived(isCollectorError(raw) ? raw.error : null)
  let energy = $derived<Partial<EnergySection> | undefined>(
    raw && !isCollectorError(raw) ? raw : undefined,
  )
  let unavailable = $derived(error !== null || energy?.available === false)

  let watts = $derived(num(energy?.power_w))
  let currency = $derived(energy?.currency)
  let since = $derived(formatSince(energy?.since))

  let periods = $derived([
    { label: 'Today', kwh: energy?.today_kwh, cost: energy?.today_cost },
    { label: 'Yesterday', kwh: energy?.yesterday_kwh, cost: undefined },
    { label: 'This month', kwh: energy?.month_kwh, cost: energy?.month_cost },
  ])
  // The backend only sends a cost when it has a price; yesterday never has
  // one.
  let withCost = $derived(periods.some((p) => num(p.cost) !== undefined))

  // Neutral while there is data: energy use has no healthy or unhealthy
  // range. Unknown when the Pi cannot measure it.
  let status = $derived<CardStatus | undefined>(
    current && unavailable ? 'unknown' : undefined,
  )
</script>

<Card title="Energy" {status}>
  {#if !current}
    <p class="muted">Waiting for data…</p>
  {:else if unavailable}
    <p class="muted" title={error ?? undefined}>
      Energy data needs a Raspberry Pi 5
    </p>
  {:else if !energy}
    <p class="muted">The latest snapshot has no energy data.</p>
  {:else}
    <div class="power">
      <div class="reading">
        <span class="watts">{formatWatts(watts)}</span>
        <span
          class="muted hint"
          title="Sum of the PMIC rails; USB devices and PMIC losses are not included"
        >
          power now
        </span>
      </div>
      {#if power[0].length > 1}
        <div class="chart">
          <Sparkline
            data={power}
            min={0}
            label="Estimated power draw, last 5 minutes"
            height={40}
          />
        </div>
      {/if}
    </div>

    <dl class="periods" class:with-cost={withCost}>
      {#each periods as p (p.label)}
        <div class="row">
          <dt>{p.label}</dt>
          <dd class="num">{formatEnergy(p.kwh)}</dd>
          {#if num(p.cost) !== undefined}
            <dd class="num cost">{formatCost(p.cost, currency)}</dd>
          {/if}
        </div>
      {/each}
      <div class="row">
        <dt>
          Total
          {#if since}
            <span class="since"
              >since <time datetime={energy.since}>{since}</time></span
            >
          {/if}
        </dt>
        <dd class="num">{formatEnergy(energy.total_kwh)}</dd>
      </div>
    </dl>
  {/if}
</Card>

<style>
  .power {
    display: flex;
    align-items: center;
    gap: var(--space-4);
  }

  .reading {
    display: flex;
    flex-direction: column;
    flex-shrink: 0;
  }

  .watts {
    font-size: 2rem;
    font-weight: 600;
    line-height: 1.2;
    font-variant-numeric: tabular-nums;
  }

  .hint {
    font-size: 0.75rem;
  }

  .chart {
    flex: 1;
    min-width: 0;
  }

  .periods {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    column-gap: var(--space-3);
    margin: var(--space-4) 0 0;
  }

  .periods.with-cost {
    grid-template-columns: minmax(0, 1fr) auto auto;
  }

  /* Rows share the columns, so the kWh values line up with or without a cost. */
  .row {
    display: grid;
    grid-column: 1 / -1;
    grid-template-columns: subgrid;
    align-items: baseline;
    padding: var(--space-1) 0;
    border-top: 1px solid var(--color-border);
  }

  dt {
    color: var(--color-text-muted);
    font-size: 0.875rem;
  }

  .since {
    font-size: 0.75rem;
  }

  dd {
    margin: 0;
    text-align: right;
  }

  .cost {
    color: var(--color-text-muted);
    font-size: 0.875rem;
  }

  .num {
    font-variant-numeric: tabular-nums;
  }
</style>
