<script lang="ts">
  import Card from '../components/Card.svelte'
  import Gauge, { gaugeStatus } from '../components/Gauge.svelte'
  import Sparkline from '../components/Sparkline.svelte'
  import { formatBytes, formatPercent } from '../lib/format'
  import { seriesHistory, snapshot } from '../lib/live'
  import {
    formatRatio,
    memoryBreakdown,
    memorySection,
    num,
    zramSwapSummary,
    type MemorySection,
  } from '../types/memory'
  import { isCollectorError } from '../types/snapshot'

  const WARN = 75
  const CRITICAL = 90

  const history = seriesHistory('mem.used_pct')

  let section = $derived(memorySection($snapshot))
  let error = $derived(isCollectorError(section) ? section.error : null)
  let mem = $derived<Partial<MemorySection>>(
    section && !isCollectorError(section) ? section : {},
  )
  let known = $derived(num(mem.total_bytes) > 0)

  let usedPct = $derived(known ? mem.used_pct : undefined)
  let segments = $derived(memoryBreakdown(mem))

  let swap = $derived(mem.swap)
  let hasSwap = $derived(num(swap?.total_bytes) > 0)
  let zram = $derived(zramSwapSummary(mem.zram))
</script>

<Card
  title="Memory"
  status={error
    ? 'unknown'
    : known
      ? gaugeStatus(usedPct, WARN, CRITICAL)
      : undefined}
>
  {#if error}
    <p class="muted">Memory unavailable: {error}</p>
  {:else}
    {#if $snapshot && !section}
      <p class="muted note">The latest snapshot has no memory data.</p>
    {/if}

    <div class="top">
      <div class="gauge">
        <Gauge
          value={usedPct}
          warn={WARN}
          critical={CRITICAL}
          label="RAM used"
        />
      </div>
      <dl>
        <dt>Used</dt>
        <dd class="num">{formatBytes(known ? mem.used_bytes : undefined)}</dd>
        <dt>Total</dt>
        <dd class="num">{formatBytes(known ? mem.total_bytes : undefined)}</dd>
        <dt>Available</dt>
        <dd class="num">
          {formatBytes(known ? mem.available_bytes : undefined)}
        </dd>
      </dl>
    </div>

    <div
      class="stack"
      role="img"
      aria-label={known
        ? segments.map((s) => `${s.label} ${formatBytes(s.bytes)}`).join(', ')
        : 'No data'}
    >
      {#if known}
        {#each segments as s (s.key)}
          {#if s.pct > 0}
            <span class="seg {s.key}" style:width="{s.pct}%"></span>
          {/if}
        {/each}
      {/if}
    </div>
    <ul class="legend" aria-hidden="true">
      {#each segments as s (s.key)}
        <li>
          <span class="swatch {s.key}"></span>
          {s.label}
          <span class="num">{formatBytes(known ? s.bytes : undefined)}</span>
        </li>
      {/each}
    </ul>

    <div class="spark">
      <div class="spark-head">
        <span class="muted">Used, last 5 min</span>
        <span class="num">{formatPercent(usedPct)}</span>
      </div>
      <Sparkline
        data={$history}
        min={0}
        max={100}
        height={32}
        label="Memory used, last 5 minutes"
      />
    </div>

    {#if hasSwap && swap}
      <div class="swap">
        <Gauge
          variant="bar"
          value={swap.used_pct}
          warn={WARN}
          critical={CRITICAL}
          label={zram ? 'Swap (zram)' : 'Swap'}
          format={() =>
            `${formatBytes(swap.used_bytes)} / ${formatBytes(swap.total_bytes)}`}
        />
        {#if zram}
          <p class="zram muted">
            {#if zram.algorithms.length > 0}
              {zram.algorithms.join(', ')} ·
            {/if}
            {#if zram.ratio !== undefined}
              <span class="num">{formatRatio(zram.ratio)}</span> compression,
              <span class="num">{formatBytes(zram.origBytes)}</span> in
              <span class="num">{formatBytes(zram.memUsedBytes)}</span> RAM
            {:else}
              nothing compressed yet
            {/if}
          </p>
        {/if}
      </div>
    {/if}
  {/if}
</Card>

<style>
  .top {
    display: flex;
    align-items: center;
    gap: var(--space-4);
  }

  .gauge {
    flex: 0 1 8rem;
    min-width: 6rem;
  }

  dl {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr);
    gap: var(--space-1) var(--space-4);
    margin: 0;
  }

  dt {
    color: var(--color-text-muted);
    font-size: 0.875rem;
    line-height: 1.5rem;
  }

  dd {
    margin: 0;
    line-height: 1.5rem;
  }

  .num {
    font-variant-numeric: tabular-nums;
  }

  .stack {
    display: flex;
    height: 0.75rem;
    margin-top: var(--space-3);
    border-radius: 999px;
    overflow: hidden;
    background: var(--color-surface-raised);
  }

  .seg {
    height: 100%;
  }

  .used {
    background: var(--chart-1);
  }
  .cache {
    background: var(--chart-7);
  }
  .free {
    background: var(--color-surface-raised);
  }

  .legend {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-1) var(--space-4);
    margin: var(--space-2) 0 0;
    padding: 0;
    list-style: none;
    font-size: 0.8125rem;
    color: var(--color-text-muted);
  }

  .legend li {
    display: flex;
    align-items: center;
    gap: var(--space-1);
  }

  .legend .num {
    color: var(--color-text);
  }

  .swatch {
    width: 0.625rem;
    height: 0.625rem;
    border-radius: 2px;
  }

  .swatch.free {
    border: 1px solid var(--color-border);
  }

  .spark {
    margin-top: var(--space-4);
  }

  .spark-head {
    display: flex;
    justify-content: space-between;
    margin-bottom: var(--space-1);
    font-size: 0.8125rem;
  }

  .swap {
    margin-top: var(--space-4);
  }

  .zram {
    margin: var(--space-1) 0 0;
    font-size: 0.8125rem;
  }

  .note {
    margin: 0 0 var(--space-2);
    font-size: 0.875rem;
  }

  @media (prefers-reduced-motion: no-preference) {
    .seg {
      transition: width 0.4s ease;
    }
  }
</style>
