<script lang="ts">
  import uPlot from 'uplot'
  import 'uplot/dist/uPlot.min.css'
  import { untrack } from 'svelte'
  import { resolveColor, yRange } from '../lib/chart'
  import { theme } from '../lib/theme.svelte'

  interface Props {
    /**
     * `[x, y]`: x values (usually Unix seconds) and the matching y values.
     * Pass a new array to update the chart; in-place changes are not seen.
     */
    data: [number[], (number | null)[]]
    /** Fixed bottom of the y scale. Left out, it follows the data. */
    min?: number
    /** Fixed top of the y scale. Left out, it follows the data. */
    max?: number
    /** Any CSS colour, theme variables included. */
    color?: string
    /** Height in CSS pixels. The width follows the container. */
    height?: number
    /** Text for screen readers, e.g. "CPU usage, last 5 minutes". */
    label?: string
  }

  let {
    data,
    min,
    max,
    color = 'var(--chart-1)',
    height = 40,
    label,
  }: Props = $props()

  let el: HTMLDivElement
  let width = $state(0)
  let chart = $state.raw<uPlot>()

  // Build the chart. Theme, colour and scale changes rebuild it, since the
  // resolved colours are baked into the options; data and size updates below
  // go through the existing instance.
  $effect(() => {
    void theme.current
    const stroke = resolveColor(el, color)

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
      series: [{}, { stroke, width: 1.5, points: { show: false } }],
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
