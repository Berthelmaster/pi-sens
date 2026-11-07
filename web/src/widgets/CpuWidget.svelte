<script lang="ts" module>
  /** Usage thresholds for the gauge, the core bars and the card status. */
  const WARN_PCT = 70
  const CRITICAL_PCT = 90
</script>

<script lang="ts">
  import Card from '../components/Card.svelte'
  import Gauge, { gaugeStatus } from '../components/Gauge.svelte'
  import Sparkline from '../components/Sparkline.svelte'
  import { formatPercent, num } from '../lib/format'
  import { seriesHistory, snapshot } from '../lib/live'
  import {
    coreRows,
    cpuSection,
    cpufreqSection,
    formatLoad,
    formatMhz,
    governors,
    maxMhz,
    showIowait,
    type CoreRow,
    type CpuSection,
    type CpufreqSection,
  } from '../types/cpu'
  import { isCollectorError } from '../types/snapshot'

  const usageHistory = seriesHistory('cpu.usage_pct')
  const iowaitHistory = seriesHistory('cpu.iowait_pct')

  let cpuRaw = $derived(cpuSection($snapshot))
  let cpuError = $derived(isCollectorError(cpuRaw) ? cpuRaw.error : null)
  let cpu = $derived<Partial<CpuSection> | undefined>(
    cpuRaw && !isCollectorError(cpuRaw) ? cpuRaw : undefined,
  )

  let freqRaw = $derived(cpufreqSection($snapshot))
  let freqError = $derived(isCollectorError(freqRaw) ? freqRaw.error : null)
  let freq = $derived<Partial<CpufreqSection> | undefined>(
    freqRaw && !isCollectorError(freqRaw) ? freqRaw : undefined,
  )

  let usage = $derived(num(cpu?.usage?.usage_pct))
  let iowait = $derived(num(cpu?.usage?.iowait_pct))
  let load = $derived(cpu?.load)
  let cores = $derived(num(cpu?.cores))

  let avgMhz = $derived(num(freq?.avg_mhz))
  let topMhz = $derived(maxMhz(freq))
  let governor = $derived(governors(freq))

  let rows = $derived(coreRows(cpu, freq))
  let iowaitShown = $derived(showIowait(iowait, $iowaitHistory[1]))
  let hasStats = $derived(
    !cpuError ||
      !!freqError ||
      avgMhz !== undefined ||
      !!governor ||
      iowaitShown,
  )

  let status = $derived(
    cpuError ? 'unknown' : gaugeStatus(usage, WARN_PCT, CRITICAL_PCT),
  )

  function coreLabel(r: CoreRow): string {
    return r.mhz === undefined
      ? `Core ${r.core}`
      : `Core ${r.core} · ${formatMhz(r.mhz)}`
  }
</script>

<Card title="CPU" {status}>
  {#if cpuError}
    <p class="muted note">CPU usage unavailable: {cpuError}</p>
  {:else}
    <div class="usage">
      <div class="gauge">
        <Gauge
          value={usage}
          warn={WARN_PCT}
          critical={CRITICAL_PCT}
          label="Total"
        />
      </div>
      <div class="history">
        <Sparkline
          data={$usageHistory}
          min={0}
          max={100}
          height={56}
          label="CPU usage, last 5 minutes"
        />
        <span class="caption muted">Last 5 min</span>
      </div>
    </div>
  {/if}

  {#if hasStats}
    <dl class="stats">
      {#if !cpuError}
        <div>
          <dt>Load 1 · 5 · 15 min</dt>
          <dd class="num">
            {formatLoad(num(load?.load1))}
            {formatLoad(num(load?.load5))}
            {formatLoad(num(load?.load15))}
          </dd>
        </div>
      {/if}
      {#if freqError}
        <div>
          <dt>Frequency</dt>
          <dd class="muted" title={freqError}>Unavailable</dd>
        </div>
      {:else if avgMhz !== undefined}
        <div>
          <dt>Frequency{rows.length > 1 ? ' (avg)' : ''}</dt>
          <dd class="num">
            {formatMhz(avgMhz)}
            {#if topMhz !== undefined}
              <span class="muted small">of {formatMhz(topMhz)}</span>
            {/if}
          </dd>
        </div>
      {/if}
      {#if governor}
        <div>
          <dt>Governor</dt>
          <dd class="mono">{governor}</dd>
        </div>
      {/if}
      {#if iowaitShown}
        <div>
          <dt>I/O wait</dt>
          <dd class="num">{formatPercent(iowait)}</dd>
        </div>
      {/if}
    </dl>
  {/if}

  {#if rows.length > 0}
    <h3>
      {cores ?? rows.length}
      {(cores ?? rows.length) === 1 ? 'core' : 'cores'}
    </h3>
    <ul class="cores">
      {#each rows as r (r.core)}
        <li>
          <Gauge
            variant="bar"
            value={r.usage}
            warn={WARN_PCT}
            critical={CRITICAL_PCT}
            label={coreLabel(r)}
          />
        </li>
      {/each}
    </ul>
  {/if}
</Card>

<style>
  .note {
    margin: 0 0 var(--space-3);
    font-size: 0.875rem;
    overflow-wrap: anywhere;
  }

  .usage {
    display: flex;
    align-items: center;
    gap: var(--space-4);
  }

  .gauge {
    flex: 0 0 7rem;
    display: flex;
    justify-content: center;
  }

  .history {
    flex: 1 1 0;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
  }

  .caption {
    font-size: 0.75rem;
  }

  .stats {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(8.5rem, 1fr));
    gap: var(--space-3) var(--space-4);
    margin: var(--space-4) 0 0;
  }

  dt {
    color: var(--color-text-muted);
    font-size: 0.75rem;
  }

  dd {
    margin: 0;
    overflow-wrap: anywhere;
  }

  .num {
    font-variant-numeric: tabular-nums;
  }

  .mono {
    font-family: var(--font-mono);
    font-size: 0.875rem;
  }

  .small {
    font-size: 0.8125rem;
  }

  h3 {
    margin: var(--space-4) 0 var(--space-2);
    font-size: 0.75rem;
    font-weight: 600;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--color-text-muted);
  }

  .cores {
    display: grid;
    gap: var(--space-2);
    margin: 0;
    padding: 0;
    list-style: none;
  }
</style>
