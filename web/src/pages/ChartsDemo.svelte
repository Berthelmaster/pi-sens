<script lang="ts">
  // Dev-only page (/dev/charts) for eyeballing the chart components with
  // static and live demo data in both themes. Not routed in production builds.
  import type uPlot from 'uplot'
  import Card from '../components/Card.svelte'
  import Gauge, { gaugeStatus } from '../components/Gauge.svelte'
  import Sparkline from '../components/Sparkline.svelte'
  import TimeSeriesChart from '../components/TimeSeriesChart.svelte'
  import { seriesColor } from '../lib/chart'

  const STEP = 5 // seconds between samples
  const POINTS = 720 // one hour

  // Small deterministic PRNG so the demo looks the same on every load.
  function rng(seed: number) {
    return () => {
      seed = (seed * 16807) % 2147483647
      return seed / 2147483647
    }
  }

  function walk(seed: number, base: number, spread: number, n: number) {
    const rand = rng(seed)
    const out: number[] = []
    let v = base
    for (let i = 0; i < n; i++) {
      v += (rand() - 0.5) * spread
      v = Math.min(100, Math.max(0, v * 0.97 + base * 0.03))
      out.push(Math.round(v * 10) / 10)
    }
    return out
  }

  const now = Math.floor(Date.now() / 1000)
  const times = Array.from(
    { length: POINTS },
    (_, i) => now - (POINTS - 1 - i) * STEP,
  )

  let cores = $state.raw<uPlot.AlignedData>([
    times,
    walk(1, 30, 8, POINTS),
    walk(2, 45, 8, POINTS),
    walk(3, 20, 8, POINTS),
    walk(4, 60, 8, POINTS),
  ])

  const temps = walk(7, 55, 2, POINTS).map((v, i) =>
    // A gap in the middle to show missing samples.
    i > 300 && i < 330 ? null : v,
  )

  let cpu = $derived(cores[1] as number[])
  let spark = $derived<[number[], number[]]>([
    (cores[0] as number[]).slice(-60),
    cpu.slice(-60),
  ])
  let latest = $derived(cpu[cpu.length - 1])
  // Two lines on one sparkline, like rx/tx on the network card. The second
  // line has a gap to show a sample only one series has.
  let spark2 = $derived<uPlot.AlignedData>([
    spark[0],
    spark[1],
    (cores[2] as number[])
      .slice(-60)
      .map((v, i) => (i > 20 && i < 26 ? null : v)),
  ])

  let gaugeValue = $state(82)

  // Live mode appends a sample every second to exercise setData.
  let live = $state(false)
  const liveRand = rng(42)

  $effect(() => {
    if (!live) return
    const timer = setInterval(() => {
      const t = (cores[0][cores[0].length - 1] as number) + STEP
      cores = cores.map((col, i) => {
        const prev = col[col.length - 1] as number
        const next =
          i === 0
            ? t
            : Math.round(
                Math.min(100, Math.max(0, prev + (liveRand() - 0.5) * 10)) * 10,
              ) / 10
        return [...col.slice(1), next]
      }) as uPlot.AlignedData
    }, 1000)
    return () => clearInterval(timer)
  })

  // Unmounting and remounting the charts is a quick leak check: watch the
  // canvas count in devtools stay flat.
  let mounted = $state(true)

  const pct = (v: number) => `${v.toFixed(0)}%`
  const celsius = (v: number) => `${v.toFixed(1)} °C`
</script>

<h1 class="visually-hidden">Chart components</h1>

<div class="controls">
  <label><input type="checkbox" bind:checked={live} /> Live data</label>
  <label><input type="checkbox" bind:checked={mounted} /> Mounted</label>
  <label>
    Gauge value
    <input type="range" min="0" max="100" bind:value={gaugeValue} />
  </label>
</div>

{#if mounted}
  <div class="grid">
    <Card title="Sparkline">
      <div class="stat">
        <span class="big">{pct(latest)}</span>
        <span class="muted">CPU, last 5 min</span>
      </div>
      <Sparkline
        data={spark}
        min={0}
        max={100}
        label="CPU usage, last 5 minutes"
      />
      <Sparkline
        data={spark}
        color="var(--chart-2)"
        height={24}
        label="Same data, auto scale, smaller"
      />
      <Sparkline
        data={spark2}
        min={0}
        max={100}
        colors={[seriesColor(0), seriesColor(1)]}
        label="Core 0 and core 1 usage, last 5 minutes"
      />
    </Card>

    <Card title="Radial gauges" status={gaugeStatus(gaugeValue, 75, 90)}>
      <div class="gauges">
        <Gauge value={gaugeValue} label="Slider" />
        <Gauge value={40} label="OK" />
        <Gauge value={78} label="Warning" />
        <Gauge value={95} label="Critical" />
        <Gauge value={null} label="No data" />
      </div>
    </Card>

    <Card title="Bar gauges">
      <div class="bars">
        <Gauge variant="bar" value={gaugeValue} label="Slider" />
        <Gauge variant="bar" value={34} label="/" />
        <Gauge variant="bar" value={81} label="/var" />
        <Gauge variant="bar" value={97} label="/boot/firmware" />
        <Gauge variant="bar" value={undefined} label="/mnt/usb" />
      </div>
    </Card>

    <div class="wide">
      <Card title="CPU per core">
        <TimeSeriesChart
          data={cores}
          series={[
            { label: 'Core 0' },
            { label: 'Core 1' },
            { label: 'Core 2' },
            { label: 'Core 3' },
          ]}
          format={pct}
          min={0}
          max={100}
          label="CPU usage per core, last hour"
        />
      </Card>
    </div>

    <div class="wide">
      <Card title="CPU temperature">
        <TimeSeriesChart
          data={[times, temps]}
          series={[{ label: 'CPU' }]}
          format={celsius}
          height={180}
          label="CPU temperature, last hour"
        />
      </Card>
    </div>
  </div>
{/if}

<style>
  .controls {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-4);
    margin-bottom: var(--space-4);
    color: var(--color-text-muted);
    font-size: 0.875rem;
  }

  .controls label {
    display: inline-flex;
    align-items: center;
    gap: var(--space-2);
  }

  .grid {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    gap: var(--space-4);
  }

  @media (min-width: 900px) {
    .grid {
      grid-template-columns: repeat(3, minmax(0, 1fr));
    }

    .wide {
      grid-column: 1 / -1;
    }
  }

  .stat {
    display: flex;
    align-items: baseline;
    gap: var(--space-2);
    margin-bottom: var(--space-2);
  }

  .big {
    font-size: 1.75rem;
    font-weight: 600;
  }

  .gauges {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(6rem, 1fr));
    gap: var(--space-3);
    justify-items: center;
  }

  .bars {
    display: grid;
    gap: var(--space-3);
  }
</style>
