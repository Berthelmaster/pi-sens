// Helpers shared by the uPlot chart components in src/components.
//
// uPlot draws on a canvas, and a canvas cannot read CSS variables. The theme
// tokens are written as light-dark(...) as well, which getComputedStyle hands
// back unresolved for a custom property. resolveColor turns any CSS colour
// into a plain rgb() string by letting the browser compute it on a probe
// element, so the charts can keep using the tokens from theme.css.

import uPlot from 'uplot'
import type { SeriesHistory } from './live'

/** Number of series colours in theme.css (--chart-1 to --chart-8). */
export const SERIES_COLORS = 8

/**
 * The CSS colour for the series at `index` (0-based). Colours go in a fixed
 * order and are never cycled: series past the palette fall back to the muted
 * axis colour, which is the cue to group them into "Other" or split the chart.
 */
export function seriesColor(index: number): string {
  return index < SERIES_COLORS
    ? `var(--chart-${index + 1})`
    : 'var(--chart-axis)'
}

/**
 * Resolve a CSS colour (a var(), light-dark(), named colour, ...) to the
 * concrete colour it has right now inside `context`, ready for a canvas.
 */
export function resolveColor(context: Element, value: string): string {
  const probe = document.createElement('span')
  probe.style.display = 'none'
  probe.style.color = value
  context.appendChild(probe)
  const color = getComputedStyle(probe).color
  probe.remove()
  return color
}

/** A canvas font string that matches the page font at `sizePx`. */
export function canvasFont(context: Element, sizePx = 12): string {
  return `${sizePx}px ${getComputedStyle(context).fontFamily}`
}

let measureCtx: CanvasRenderingContext2D | null = null

/** Width in CSS pixels of the widest string in `values` drawn in `font`. */
export function textWidth(values: string[], font: string): number {
  measureCtx ??= document.createElement('canvas').getContext('2d')
  if (!measureCtx) return 0
  measureCtx.font = font
  let widest = 0
  for (const v of values) {
    widest = Math.max(widest, measureCtx.measureText(v).width)
  }
  return widest
}

/**
 * A uPlot y range that pins whichever of `min` and `max` is given and pads
 * the other side around the data, e.g. `yRange(0, 100)` for a percentage or
 * `yRange(0)` for a rate that starts at zero.
 */
export function yRange(min?: number, max?: number): uPlot.Range.Function {
  return (_u, dataMin, dataMax) => {
    if (min !== undefined && max !== undefined) return [min, max]
    // dataMin/dataMax are null while there is no data yet.
    const lo = dataMin ?? min ?? 0
    const hi = dataMax ?? max ?? 1
    const [padLo, padHi] = uPlot.rangeNum(lo, hi, 0.1, true)
    return [min ?? padLo, max ?? padHi]
  }
}

/**
 * `color`, an rgb() or rgba() string as resolveColor returns it, with its
 * alpha set to `alpha`. Anything else comes back unchanged.
 */
export function withAlpha(color: string, alpha: number): string {
  const m = /^rgba?\(\s*([\d.]+)[\s,]+([\d.]+)[\s,]+([\d.]+)/.exec(color)
  if (!m) return color
  return `rgba(${m[1]}, ${m[2]}, ${m[3]}, ${alpha})`
}

/**
 * Several `[times, values]` histories as one uPlot data table, for a chart
 * with a line per history. The x values are every time any history has,
 * oldest first; a history without a sample at one of them gets a null there,
 * which uPlot draws as a gap.
 */
export function alignHistories(
  ...histories: SeriesHistory[]
): uPlot.AlignedData {
  const times = [...new Set(histories.flatMap(([t]) => t))].sort(
    (a, b) => a - b,
  )
  const index = new Map(times.map((t, i) => [t, i]))
  const columns = histories.map(([t, v]) => {
    const col: (number | null)[] = new Array(times.length).fill(null)
    t.forEach((time, i) => (col[index.get(time) as number] = v[i]))
    return col
  })
  return [times, ...columns]
}
