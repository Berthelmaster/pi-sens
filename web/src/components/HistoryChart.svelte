<script lang="ts">
  // A TimeSeriesChart over a loaded history answer, with the loading, empty
  // and error states in its place while there is nothing to draw.
  import {
    chartData,
    unitFormat,
    type PanelSeries,
    type Unit,
  } from '../lib/history'
  import type { LoadedHistory } from '../lib/historyQuery.svelte'
  import TimeSeriesChart from './TimeSeriesChart.svelte'

  interface Props {
    result: LoadedHistory | null
    loading: boolean
    error?: string | null
    series: PanelSeries[]
    unit: Unit
    min?: number
    max?: number
    height?: number
    /** Text for screen readers. */
    label: string
    /** Links the crosshair with other charts that use the same key. */
    syncKey?: string
  }

  let {
    result,
    loading,
    error = null,
    series,
    unit,
    min,
    max,
    height = 220,
    label,
    syncKey,
  }: Props = $props()

  let chartSeries = $derived(series.map((s) => ({ label: s.label })))
  let chart = $derived(result ? chartData(result, series) : null)
  let xRange = $derived.by((): [number, number] | undefined => {
    if (!result || !chart) return undefined
    // The first bucket can start a little before the range.
    const first = chart.data[0][0] ?? result.from
    return [Math.min(result.from, first), result.to]
  })
</script>

<div class="history-chart" class:stale={loading} aria-busy={loading}>
  {#if chart?.hasData}
    <TimeSeriesChart
      data={chart.data}
      bands={chart.bands}
      {xRange}
      series={chartSeries}
      format={unitFormat[unit]}
      {min}
      {max}
      {height}
      {label}
      {syncKey}
      table
    />
  {:else}
    <div class="placeholder" style:height="{height}px">
      {#if loading || (!result && !error)}
        <span class="spinner" aria-hidden="true"></span> Loading…
      {:else if error && !result}
        Couldn't load this chart.
      {:else}
        No data in this range.
      {/if}
    </div>
  {/if}
</div>

<style>
  .history-chart :global(.chart) {
    transition: opacity 0.15s;
  }

  .history-chart.stale :global(.chart) {
    opacity: 0.5;
  }

  .placeholder {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: var(--space-2);
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
