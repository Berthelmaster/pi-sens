<script lang="ts">
  // Dev-only page (/dev/energy) showing the energy widget with mock data in
  // each of its states, since a dev machine has no PMIC to measure. Not
  // routed in production builds.
  import type { SeriesHistory } from '../lib/live'
  import type { EnergySection } from '../types/energy'
  import type { Snapshot } from '../types/snapshot'
  import EnergyWidget from '../widgets/EnergyWidget.svelte'

  const STEP = 5 // seconds between samples, like the backend default
  const POINTS = 60 // five minutes

  const now = Math.floor(Date.now() / 1000)

  // A deterministic wobble around 3.2 W with a short burst of load.
  const power: SeriesHistory = [
    Array.from({ length: POINTS }, (_, i) => now - (POINTS - 1 - i) * STEP),
    Array.from({ length: POINTS }, (_, i) => {
      const burst = i > 30 && i < 42 ? 2.4 : 0
      return Math.round((3.2 + Math.sin(i / 3) * 0.15 + burst) * 100) / 100
    }),
  ]
  const latest = power[1][POINTS - 1]

  function snapshot(energy: unknown): Snapshot {
    return {
      timestamp: new Date(now * 1000).toISOString(),
      collectors: { energy },
      series: { 'pi.power_w': latest },
    }
  }

  const base: EnergySection = {
    available: true,
    power_w: latest,
    today_kwh: 0.0412,
    yesterday_kwh: 0.0771,
    month_kwh: 0.5208,
    total_kwh: 3.1904,
    since: '2026-09-14T08:02:11.123456789Z',
  }

  const cases: {
    title: string
    snap: Snapshot | null
    history: SeriesHistory
  }[] = [
    {
      title: 'With a price (PISENS_ENERGY_PRICE=2.45, DKK)',
      snap: snapshot({
        ...base,
        today_cost: 0.1009,
        month_cost: 1.276,
        currency: 'DKK',
      }),
      history: power,
    },
    {
      title: 'Without a price',
      snap: snapshot(base),
      history: power,
    },
    {
      title: 'A price without a currency, over 1 kWh this month',
      snap: snapshot({
        ...base,
        today_kwh: 0.0009,
        month_kwh: 1.8342,
        total_kwh: 42.517,
        today_cost: 0.0022,
        month_cost: 4.4938,
        currency: '',
      }),
      history: power,
    },
    {
      title: 'Power reading stale, no history yet',
      snap: snapshot({ ...base, power_w: undefined }),
      history: [[], []],
    },
    {
      title: 'Unavailable (Pi 4 or older)',
      snap: snapshot({ available: false }),
      history: [[], []],
    },
    {
      title: 'Collector error',
      snap: snapshot({ error: 'energy: meter closed' }),
      history: [[], []],
    },
    {
      title: 'Waiting for the first snapshot',
      snap: null,
      history: [[], []],
    },
  ]
</script>

<h1>Energy widget</h1>
<p class="muted">Mock data for each state of the Overview's energy card.</p>

<div class="grid">
  {#each cases as c (c.title)}
    <div>
      <h2>{c.title}</h2>
      <EnergyWidget snap={c.snap} history={c.history} />
    </div>
  {/each}
</div>

<style>
  h1 {
    margin: 0 0 var(--space-2);
    font-size: 1.5rem;
  }

  h2 {
    margin: 0 0 var(--space-2);
    font-size: 0.875rem;
    font-weight: 500;
    color: var(--color-text-muted);
  }

  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(20rem, 1fr));
    gap: var(--space-4);
    margin-top: var(--space-4);
  }
</style>
