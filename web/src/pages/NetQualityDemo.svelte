<script lang="ts">
  // Dev-only page (/dev/netquality) showing the internet health widget with
  // mock data in each of its states, as a dev machine without the backend
  // has nothing to ping. Not routed in production builds.
  import type { SeriesHistory } from '../lib/live'
  import type {
    NetQualityDns,
    NetQualitySection,
    NetQualityTarget,
  } from '../types/netquality'
  import type { Snapshot } from '../types/snapshot'
  import NetQualityWidget from '../widgets/NetQualityWidget.svelte'

  const STEP = 5 // seconds between samples, like the backend default
  const POINTS = 60 // five minutes

  const now = Math.floor(Date.now() / 1000)
  const times = Array.from(
    { length: POINTS },
    (_, i) => now - (POINTS - 1 - i) * STEP,
  )
  const iso = (s: number) => new Date(s * 1000).toISOString()

  type Histories = Record<string, SeriesHistory>

  /** A latency line wobbling around base, left out where lost(i) is true. */
  function latency(
    base: number,
    swing: number,
    lost: (i: number) => boolean = () => false,
  ): SeriesHistory {
    const t: number[] = []
    const v: number[] = []
    times.forEach((time, i) => {
      if (lost(i)) return
      t.push(time)
      v.push(Math.round((base + Math.sin(i / 2.5) * swing) * 1000) / 1000)
    })
    return [t, v]
  }

  function constant(value: (i: number) => number): SeriesHistory {
    return [times, times.map((_, i) => value(i))]
  }

  function target(
    key: string,
    over: Partial<NetQualityTarget> = {},
  ): NetQualityTarget {
    return {
      name: key,
      key,
      kind: 'internet',
      method: 'icmp',
      latency_ms: 12.3,
      jitter_ms: 0.8,
      loss_pct: 0,
      last_ok: iso(now),
      ...over,
    }
  }

  const gateway = (over: Partial<NetQualityTarget> = {}) =>
    target('gateway', {
      kind: 'gateway',
      address: '192.168.1.1',
      latency_ms: 0.612,
      jitter_ms: 0.104,
      ...over,
    })

  const dns = (over: Partial<NetQualityDns> = {}): NetQualityDns => ({
    name: 'example.com',
    resolver: '192.168.1.1:53',
    ok: true,
    lookup_ms: 18.42,
    failures: 0,
    last_ok: iso(now),
    ...over,
  })

  function snapshot(netquality: unknown): Snapshot {
    return {
      timestamp: iso(now),
      collectors: { netquality },
      series: {},
    }
  }

  function section(over: Partial<NetQualitySection>): NetQualitySection {
    return {
      available: true,
      status: 'good',
      interval_s: 10,
      probed_at: iso(now),
      ...over,
    }
  }

  // Good: everything answers quickly.
  const good: Histories = {
    'netq.gateway.latency_ms': latency(0.6, 0.1),
    'netq.gateway.loss_pct': constant(() => 0),
    'netq.1-1-1-1.latency_ms': latency(12.3, 1.1),
    'netq.1-1-1-1.loss_pct': constant(() => 0),
    'netq.8-8-8-8.latency_ms': latency(14.1, 0.9),
    'netq.8-8-8-8.loss_pct': constant(() => 0),
    'netq.dns.failures': constant(() => 0),
  }
  const goodTargets = [
    gateway(),
    target('1-1-1-1', { name: '1.1.1.1' }),
    target('8-8-8-8', { name: '8.8.8.8', latency_ms: 14.08 }),
  ]

  // Good again after a 40 second outage two minutes ago.
  const blip = (i: number) => i >= 28 && i < 36
  const recovered: Histories = {
    ...good,
    'netq.1-1-1-1.latency_ms': latency(12.3, 1.1, blip),
    'netq.1-1-1-1.loss_pct': constant((i) => (blip(i) ? 100 : 0)),
    'netq.8-8-8-8.latency_ms': latency(14.1, 0.9, blip),
    'netq.8-8-8-8.loss_pct': constant((i) => (blip(i) ? 100 : 0)),
    'netq.dns.failures': constant((i) => (i >= 30 ? 3 : 2)),
  }

  // Degraded: ICMP not allowed, one host drops pings and one is slow, and a
  // DNS lookup failed a minute ago.
  const degraded: Histories = {
    'netq.gateway.latency_ms': latency(1.9, 0.4),
    'netq.gateway.loss_pct': constant(() => 0),
    'netq.1-1-1-1.latency_ms': latency(23, 6),
    'netq.1-1-1-1.loss_pct': constant((i) => (i % 7 === 0 ? 20 : 0)),
    'netq.8-8-8-8.latency_ms': latency(168, 25),
    'netq.8-8-8-8.loss_pct': constant(() => 0),
    'netq.dns.failures': constant((i) => (i >= 48 ? 5 : 4)),
  }
  const degradedTargets = [
    gateway({ method: 'tcp', latency_ms: 1.874, jitter_ms: 0.391 }),
    target('1-1-1-1', {
      name: '1.1.1.1',
      method: 'tcp',
      latency_ms: 24.71,
      jitter_ms: 6.02,
      loss_pct: 20,
    }),
    target('8-8-8-8', {
      name: '8.8.8.8',
      method: 'tcp',
      latency_ms: 171.4,
      jitter_ms: 22.9,
    }),
  ]

  // Down: the router answers, nothing beyond it has for a minute and a half.
  const cut = (i: number) => i >= 42
  const down: Histories = {
    'netq.gateway.latency_ms': latency(0.7, 0.1),
    'netq.gateway.loss_pct': constant(() => 0),
    'netq.1-1-1-1.latency_ms': latency(12.3, 1.1, cut),
    'netq.1-1-1-1.loss_pct': constant((i) => (cut(i) ? 100 : 0)),
    'netq.8-8-8-8.latency_ms': latency(14.1, 0.9, cut),
    'netq.8-8-8-8.loss_pct': constant((i) => (cut(i) ? 100 : 0)),
    'netq.dns.failures': constant((i) => (cut(i) ? 1 + i - 42 : 0)),
  }
  const lost = { latency_ms: null, jitter_ms: null, loss_pct: 100 }
  const downTargets = [
    gateway({ latency_ms: 0.702 }),
    target('1-1-1-1', {
      name: '1.1.1.1',
      ...lost,
      last_ok: iso(times[41]),
    }),
    target('8-8-8-8', {
      name: 'dns.google',
      address: '8.8.8.8',
      ...lost,
      last_ok: iso(times[41]),
      error: 'lookup dns.google: i/o timeout',
    }),
  ]

  const empty: Histories = {}

  const cases: { title: string; snap: Snapshot | null; history: Histories }[] =
    [
      {
        title: 'Good',
        snap: snapshot(
          section({ targets: goodTargets, dns: dns({ failures: 0 }) }),
        ),
        history: good,
      },
      {
        title: 'Degraded (TCP fallback, loss, slow host)',
        snap: snapshot(
          section({
            status: 'degraded',
            targets: degradedTargets,
            dns: dns({ lookup_ms: 62.8, failures: 5 }),
          }),
        ),
        history: degraded,
      },
      {
        title: 'Down (internet outage in progress)',
        snap: snapshot(
          section({
            status: 'down',
            targets: downTargets,
            dns: dns({
              ok: false,
              lookup_ms: null,
              failures: 18,
              last_ok: iso(times[41]),
              error: 'no answer from 192.168.1.1:53 within 2s',
            }),
          }),
        ),
        history: down,
      },
      {
        title: 'Good after a short outage',
        snap: snapshot(
          section({ targets: goodTargets, dns: dns({ failures: 3 }) }),
        ),
        history: recovered,
      },
      {
        title: 'Only the router, no DNS lookups',
        snap: snapshot(section({ targets: [gateway()] })),
        history: good,
      },
      {
        title: 'First round still running',
        snap: snapshot({ available: true, status: 'pending', interval_s: 10 }),
        history: empty,
      },
      {
        title: 'Turned off (PISENS_NETQ=off)',
        snap: snapshot({ available: false }),
        history: empty,
      },
      {
        title: 'Collector error',
        snap: snapshot({ error: 'netquality: probe goroutine stopped' }),
        history: empty,
      },
      {
        title: 'Waiting for the first snapshot',
        snap: null,
        history: empty,
      },
    ]
</script>

<h1>Internet health widget</h1>
<p class="muted">Mock data for each state of the Overview's internet card.</p>

<div class="grid">
  {#each cases as c (c.title)}
    <div>
      <h2>{c.title}</h2>
      <NetQualityWidget snap={c.snap} history={c.history} />
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
