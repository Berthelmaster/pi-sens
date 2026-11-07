<script lang="ts">
  import uPlot from 'uplot'
  import 'uplot/dist/uPlot.min.css'
  import { untrack } from 'svelte'
  import { resolveColor, seriesColor, yRange } from '../lib/chart'
  import { theme } from '../lib/theme.svelte'

  interface Props {
    /**
     * `[x, y]` or `[x, y1, y2, ...]`: x values (usually Unix seconds) and one
     * array of matching y values per line. All lines share the y scale.
     * Pass a new array to update the chart; in-place changes are not seen.
     */
    data: [number[], (number | null)[]] | uPlot.AlignedData
    /** Fixed bottom of the y scale. Left out, it follows the data. */
    min?: number
    /** Fixed top of the y scale. Left out, it follows the data. */
    max?: number
    /** Any CSS colour, theme variables included. Colours the first line. */
    color?: string
    /**
     * One CSS colour per line, in the order of `data`. Lines without one
     * take `color` (the first) or `seriesColor` of their index.
     */
    colors?: string[]
    /** Height in CSS pixels. The width follows the container. */
    height?: number
    /** Text for screen readers, e.g. "CPU usage, last 5 minutes". */
    label?: string
  }

  let {
    data,
    min,
    max,
    color = seriesColor(0),
    colors,
    height = 40,
    label,
  }: Props = $props()

  let el: HTMLDivElement
  let width = $state(0)
  let chart = $state.raw<uPlot>()

  // The CSS colour of every line, joined into a string so a new but equal
  // `colors` array (an inline literal, say) does not rebuild the chart.
  let lineColors = $derived(
    Array.from(
      { length: Math.max(1, data.length - 1) },
      (_, i) => colors?.[i] ?? (i === 0 ? color : seriesColor(i)),
    ).join('\n'),
  )

  // Build the chart. Theme, colour, scale and line count changes rebuild it,
  // since those are baked into the options; data and size updates below go
  // through the existing instance.
  $effect(() => {
    void theme.current
    const strokes = lineColors.split('\n').map((c) => resolveColor(el, c))

    const opts: uPlot.Options = {
      width: el.clientWidth,
      height: untrack(() => height),
      pxAlign: false,
      cursor: { show: false },
      select: { show: false, left: 0, top: 0, width: 0, height: 0 },
      legend: { show: false },
      scales: {
        x: { time: false },
        y: { range: yRange(min, max) },
      },
      axes: [{ show: false }, { show: false }],
      series: [
        {},
        ...strokes.map((stroke) => ({
          stroke,
          width: 1.5,
          points: { show: false },
        })),
      ],
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
    }
  })

  $effect(() => {
    chart?.setData(data)
  })

  $effect(() => {
    if (width > 0) chart?.setSize({ width, height })
  })
</script>

<div
  class="sparkline"
  style:height="{height}px"
  bind:this={el}
  bind:clientWidth={width}
  role="img"
  aria-label={label}
  aria-hidden={label ? undefined : 'true'}
></div>

<style>
  .sparkline {
    width: 100%;
    min-width: 0;
    overflow: hidden;
  }
</style>
