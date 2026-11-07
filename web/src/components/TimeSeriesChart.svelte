<script lang="ts" module>
  export interface ChartSeries {
    label: string
    /** Any CSS colour. Defaults to the next colour from the theme palette. */
    color?: string
  }
</script>

<script lang="ts">
  import uPlot from 'uplot'
  import 'uplot/dist/uPlot.min.css'
  import { untrack } from 'svelte'
  import {
    canvasFont,
    resolveColor,
    seriesColor,
    textWidth,
    yRange,
  } from '../lib/chart'
  import { theme } from '../lib/theme.svelte'

  interface Props {
    /**
     * `[timestamps, ...values]`: Unix timestamps in seconds, then one array
     * per entry in `series`. Use null for a gap. Pass a new array to update
     * the chart; in-place changes are not seen.
     */
    data: uPlot.AlignedData
    series: ChartSeries[]
    /** Formats y values for the axis and tooltip, e.g. `(v) => v + ' %'`. */
    format?: (value: number) => string
    /** Fixed bottom of the y scale. Left out, it follows the data. */
    min?: number
    /** Fixed top of the y scale. Left out, it follows the data. */
    max?: number
    /** Height in CSS pixels. The width follows the container. */
    height?: number
    /** Show the legend. Defaults to on when there is more than one series. */
    legend?: boolean
    /** Text for screen readers, e.g. "CPU usage per core, last hour". */
    label?: string
  }

  let {
    data,
    series,
    format = defaultFormat,
    min,
    max,
    height = 240,
    legend,
    label,
  }: Props = $props()

  function defaultFormat(value: number): string {
    return value.toLocaleString(undefined, { maximumFractionDigits: 2 })
  }

  const timeFormat = new Intl.DateTimeFormat(undefined, {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })

  let el: HTMLDivElement
  let width = $state(0)
  let chart = $state.raw<uPlot>()

  /** Series hidden from the legend, by index into `series`. */
  let hidden = $state<boolean[]>([])
  let colors = $derived(series.map((s, i) => s.color ?? seriesColor(i)))
  let showLegend = $derived(legend ?? series.length > 1)

  interface Hover {
    idx: number
    left: number
    top: number
    flip: boolean
  }
  let hover = $state<Hover | null>(null)

  // Build the chart. Theme, series and scale changes rebuild it, since the
  // resolved colours are baked into the options; data and size updates below
  // go through the existing instance.
  $effect(() => {
    void theme.current
    const fmt = format
    const axisColor = resolveColor(el, 'var(--chart-axis)')
    const gridColor = resolveColor(el, 'var(--chart-grid)')
    const font = canvasFont(el, 12)
    const strokes = colors.map((c) => resolveColor(el, c))
    const isHidden = untrack(() => hidden)

    const axis: uPlot.Axis = {
      stroke: axisColor,
      font,
      grid: { stroke: gridColor, width: 1 },
      ticks: { stroke: gridColor, width: 1, size: 4 },
    }

    const opts: uPlot.Options = {
      width: el.clientWidth,
      height: untrack(() => height),
      cursor: {
        y: false,
        drag: { x: false, y: false, setScale: false },
        points: { size: 8, width: 2 },
      },
      select: { show: false, left: 0, top: 0, width: 0, height: 0 },
      legend: { show: false },
      scales: { y: { range: yRange(min, max) } },
      axes: [
        { ...axis },
        {
          ...axis,
          values: (_u, splits) => splits.map(fmt),
          // Fit the widest label: tick + gap + text + a little air.
          size: (_u, values) =>
            Math.ceil(textWidth(values ?? [], font)) + 4 + 5 + 6,
        },
      ],
      series: [
        {},
        ...series.map((s, i): uPlot.Series => ({
          label: s.label,
          stroke: strokes[i],
          width: 2,
          points: { show: false },
          show: !isHidden[i],
        })),
      ],
      hooks: {
        setCursor: [
          (u) => {
            const { idx, left, top } = u.cursor
            if (idx == null || left == null || top == null || left < 0) {
              hover = null
              return
            }
            const plotWidth = u.over.clientWidth
            hover = {
              idx,
              left: u.over.offsetLeft + left,
              top: u.over.offsetTop + top,
              flip: left > plotWidth / 2,
            }
          },
        ],
      },
    }

    const u = new uPlot(
      opts,
      untrack(() => data),
      el,
    )
    chart = u
    return () => {
      u.destroy()
      chart = undefined
      hover = null
    }
  })

  $effect(() => {
    chart?.setData(data)
  })

  $effect(() => {
    if (width > 0) chart?.setSize({ width, height })
  })

  function toggle(i: number) {
    hidden[i] = !hidden[i]
    chart?.setSeries(i + 1, { show: !hidden[i] })
  }

  function valueAt(seriesIdx: number, idx: number): string {
    const v = data[seriesIdx + 1]?.[idx]
    return v == null ? '–' : format(v)
  }
</script>

<figure class="chart">
  <div class="plot" style:min-height="{height}px">
    <div
      class="canvas"
      bind:this={el}
      bind:clientWidth={width}
      role="img"
      aria-label={label}
    ></div>

    {#if hover && data[0][hover.idx] != null}
      <div
        class="tooltip"
        class:flip={hover.flip}
        style:left="{hover.left}px"
        style:top="{hover.top}px"
        aria-hidden="true"
      >
        <div class="time">
          {timeFormat.format(new Date(data[0][hover.idx] * 1000))}
        </div>
        {#each series as s, i (i)}
          {#if !hidden[i]}
            <div class="row">
              <span class="swatch" style:background={colors[i]}></span>
              <span class="name">{s.label}</span>
              <span class="value">{valueAt(i, hover.idx)}</span>
            </div>
          {/if}
        {/each}
      </div>
    {/if}
  </div>

  {#if showLegend}
    <figcaption class="legend">
      {#each series as s, i (i)}
        <button
          type="button"
          aria-pressed={!hidden[i]}
          title={hidden[i] ? `Show ${s.label}` : `Hide ${s.label}`}
          onclick={() => toggle(i)}
        >
          <span class="swatch" style:background={colors[i]}></span>
          {s.label}
        </button>
      {/each}
    </figcaption>
  {/if}
</figure>

<style>
  .chart {
    margin: 0;
    min-width: 0;
  }

  .plot {
    position: relative;
  }

  .canvas {
    width: 100%;
    min-width: 0;
    overflow: hidden;
  }

  .canvas :global(.u-hz .u-cursor-x) {
    border-right: 1px solid var(--chart-cursor);
  }

  .canvas :global(.u-cursor-pt) {
    /* A ring in the surface colour keeps the point clear of the line. */
    box-shadow: 0 0 0 2px var(--color-surface);
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
  }

  .legend {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-1) var(--space-3);
    margin-top: var(--space-2);
  }

  .legend button {
    display: inline-flex;
    align-items: center;
    gap: var(--space-2);
    padding: var(--space-1) 0;
    border: 0;
    background: none;
    color: var(--color-text);
    font: inherit;
    font-size: 0.8125rem;
    cursor: pointer;
  }

  .legend button[aria-pressed='false'] {
    color: var(--color-text-muted);
    text-decoration: line-through;
  }

  .legend button[aria-pressed='false'] .swatch {
    opacity: 0.3;
  }

  .legend button:focus-visible {
    outline: 2px solid var(--color-accent);
    outline-offset: 2px;
    border-radius: 2px;
  }
</style>
