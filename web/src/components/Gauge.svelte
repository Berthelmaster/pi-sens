<script lang="ts" module>
  import type { CardStatus } from './Card.svelte'

  /**
   * The health state of a percentage against warn/critical thresholds, the
   * same rule the gauge colours by. Handy for passing `status` to a Card.
   */
  export function gaugeStatus(
    value: number | null | undefined,
    warn: number,
    critical: number,
  ): CardStatus {
    if (value == null || !Number.isFinite(value)) return 'unknown'
    if (value >= critical) return 'critical'
    if (value >= warn) return 'warning'
    return 'ok'
  }
</script>

<script lang="ts">
  interface Props {
    /** Percentage, 0 to 100. null or undefined shows an empty gauge. */
    value: number | null | undefined
    /** Turns the gauge to the warning colour at and above this value. */
    warn?: number
    /** Turns the gauge to the critical colour at and above this value. */
    critical?: number
    /** Caption under the gauge; also its accessible name. */
    label?: string
    /** A 240° dial, or a horizontal bar for lists and tight spaces. */
    variant?: 'radial' | 'bar'
    /** Formats the value shown on the gauge. */
    format?: (value: number) => string
  }

  let {
    value,
    warn = 75,
    critical = 90,
    label,
    variant = 'radial',
    format = (v) => `${Math.round(v)}%`,
  }: Props = $props()

  const statusLabel: Record<CardStatus, string> = {
    ok: 'OK',
    warning: 'Warning',
    critical: 'Critical',
    unknown: 'No data',
  }

  let status = $derived(gaugeStatus(value, warn, critical))
  let known = $derived(value != null && Number.isFinite(value))
  let pct = $derived(known ? Math.min(100, Math.max(0, value as number)) : 0)
  let text = $derived(known ? format(value as number) : '–')
  let valueText = $derived(
    known ? `${text}, ${statusLabel[status].toLowerCase()}` : 'No data',
  )
  let thresholds = $derived([warn, critical].filter((t) => t > 0 && t < 100))

  // The dial runs clockwise from -120° to +120°, 0° pointing up.
  const R = 40
  const START = -120
  const SWEEP = 240

  function point(angle: number, r: number): [number, number] {
    const rad = (angle * Math.PI) / 180
    return [50 + r * Math.sin(rad), 50 - r * Math.cos(rad)]
  }

  const [sx, sy] = point(START, R)
  const [ex, ey] = point(START + SWEEP, R)
  const arc = `M ${sx} ${sy} A ${R} ${R} 0 1 1 ${ex} ${ey}`

  function tick(t: number) {
    const angle = START + (SWEEP * t) / 100
    const [x1, y1] = point(angle, 46.5)
    const [x2, y2] = point(angle, 50)
    return { x1, y1, x2, y2 }
  }
</script>

<div
  class="gauge {variant}"
  data-status={status}
  role="meter"
  aria-label={label}
  aria-valuemin={0}
  aria-valuemax={100}
  aria-valuenow={known ? pct : undefined}
  aria-valuetext={valueText}
>
  {#if variant === 'radial'}
    <svg viewBox="0 0 100 80" aria-hidden="true">
      <path class="track" d={arc} pathLength="100" />
      {#if known}
        <path
          class="fill"
          d={arc}
          pathLength="100"
          stroke-dasharray="{pct} 100"
        />
      {/if}
      {#each thresholds as t (t)}
        <line class="threshold" {...tick(t)} />
      {/each}
      <text class="value" x="50" y="57">{text}</text>
      {#if status === 'warning' || status === 'critical'}
        <text class="status" x="50" y="73">{statusLabel[status]}</text>
      {/if}
    </svg>
    {#if label}
      <div class="label">{label}</div>
    {/if}
  {:else}
    <div class="bar-head" aria-hidden="true">
      {#if label}<span class="label">{label}</span>{/if}
      <span class="bar-value">
        {#if status === 'warning' || status === 'critical'}
          <span class="status">{statusLabel[status]}</span>
        {/if}
        {text}
      </span>
    </div>
    <div class="bar-track" aria-hidden="true">
      <div class="bar-fill" style:width="{pct}%"></div>
      {#each thresholds as t (t)}
        <span class="bar-threshold" style:left="{t}%"></span>
      {/each}
    </div>
  {/if}
</div>

<style>
  .gauge {
    --gauge-color: var(--color-ok);
    min-width: 0;
  }

  .gauge[data-status='warning'] {
    --gauge-color: var(--color-warning);
  }
  .gauge[data-status='critical'] {
    --gauge-color: var(--color-critical);
  }
  .gauge[data-status='unknown'] {
    --gauge-color: var(--color-unknown);
  }

  .label {
    color: var(--color-text-muted);
    font-size: 0.8125rem;
  }

  .status {
    color: var(--gauge-color);
    fill: var(--gauge-color);
    font-weight: 600;
  }

  /* Radial */

  .radial {
    display: flex;
    flex-direction: column;
    align-items: center;
    width: 100%;
    max-width: 10rem;
  }

  svg {
    display: block;
    width: 100%;
    overflow: visible;
  }

  .track,
  .fill {
    fill: none;
    stroke-width: 10;
    stroke-linecap: round;
  }

  .track {
    stroke: var(--color-surface-raised);
  }

  .fill {
    stroke: var(--gauge-color);
  }

  .threshold {
    stroke: var(--color-text-muted);
    stroke-width: 1.5;
    stroke-linecap: round;
  }

  text {
    text-anchor: middle;
    dominant-baseline: middle;
    font-family: var(--font-sans);
  }

  .value {
    fill: var(--color-text);
    font-size: 20px;
    font-weight: 600;
  }

  text.status {
    font-size: 8px;
    letter-spacing: 0.04em;
    text-transform: uppercase;
  }

  .radial .label {
    margin-top: calc(-1 * var(--space-1));
    text-align: center;
  }

  /* Bar */

  .bar {
    width: 100%;
  }

  .bar-head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: var(--space-2);
    margin-bottom: var(--space-1);
  }

  .bar-value {
    margin-left: auto;
    font-variant-numeric: tabular-nums;
    font-weight: 600;
    font-size: 0.875rem;
  }

  .bar-value .status {
    margin-right: var(--space-2);
    font-size: 0.75rem;
    letter-spacing: 0.04em;
    text-transform: uppercase;
  }

  .bar-track {
    position: relative;
    height: 0.5rem;
    border-radius: 999px;
    background: var(--color-surface-raised);
  }

  .bar-fill {
    height: 100%;
    border-radius: inherit;
    background: var(--gauge-color);
  }

  .bar-threshold {
    position: absolute;
    top: -2px;
    bottom: -2px;
    width: 2px;
    margin-left: -1px;
    background: var(--color-text-muted);
    border-radius: 1px;
  }

  @media (prefers-reduced-motion: no-preference) {
    .fill {
      transition:
        stroke-dasharray 0.4s ease,
        stroke 0.4s ease;
    }

    .bar-fill {
      transition:
        width 0.4s ease,
        background-color 0.4s ease;
    }
  }
</style>
