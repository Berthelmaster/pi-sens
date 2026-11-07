<script lang="ts">
  import { derived } from 'svelte/store'
  import Card, { type CardStatus } from '../components/Card.svelte'
  import Sparkline from '../components/Sparkline.svelte'
  import {
    seriesHistory,
    snapshot as liveSnapshot,
    type SeriesHistory,
  } from '../lib/live'
  import { formatClock } from '../types/alerts'
  import {
    cardStatus,
    describeOutage,
    DNS_FAILURES_KEY,
    formatLoss,
    formatMs,
    lastDnsFailure,
    lastOutage,
    latencyKey,
    netqualitySection,
    outageKeys,
    STATUS_LABEL,
    statusOf,
    targetLabel,
    type NetQualitySection,
    type NetQualityTarget,
  } from '../types/netquality'
  import { isCollectorError, type Snapshot } from '../types/snapshot'

  interface Props {
    /**
     * Shows this snapshot instead of the live one. Only for the dev page
     * (/dev/netquality); the Overview leaves it out.
     */
    snap?: Snapshot | null
    /** Series histories by key in place of the live ones. */
    history?: Record<string, SeriesHistory>
  }

  let { snap, history }: Props = $props()

  const EMPTY: SeriesHistory = [[], []]

  let current = $derived(snap === undefined ? $liveSnapshot : snap)
  let raw = $derived(netqualitySection(current))
  let error = $derived(isCollectorError(raw) ? raw.error : null)
  let netq = $derived<Partial<NetQualitySection> | undefined>(
    raw && !isCollectorError(raw) ? raw : undefined,
  )
  let disabled = $derived(netq?.available === false)
  let status = $derived(statusOf(netq?.status))
  let targets = $derived<NetQualityTarget[]>(
    Array.isArray(netq?.targets) ? netq.targets : [],
  )
  let dns = $derived(netq?.dns)

  // Every series the widget reads from the rolling buffer. The key list is a
  // string so the subscriptions are only rebuilt when the targets change,
  // not on every snapshot.
  let lossKeys = $derived(outageKeys(targets))
  let historyKeys = $derived(
    [...targets.map(latencyKey), ...lossKeys, DNS_FAILURES_KEY].join('\n'),
  )
  let liveHistories = $state.raw<Record<string, SeriesHistory>>({})
  $effect(() => {
    if (history) return
    const keys = historyKeys.split('\n')
    return derived(keys.map(seriesHistory), (values) =>
      Object.fromEntries(keys.map((k, i) => [k, values[i]])),
    ).subscribe((v) => (liveHistories = v))
  })
  let histories = $derived(history ?? liveHistories)
  const hist = (key: string): SeriesHistory => histories[key] ?? EMPTY

  // Clock times show the date as well when they are not from today.
  let now = $derived(current ? Date.now() : 0)
  let outage = $derived(lastOutage(lossKeys.map(hist)))
  let dnsFailedAt = $derived(lastDnsFailure(hist(DNS_FAILURES_KEY)))

  let cardTone = $derived<CardStatus | undefined>(
    current && (error !== null || disabled) ? 'unknown' : cardStatus(status),
  )
</script>

<Card title="Internet" status={cardTone}>
  {#if !current}
    <p class="muted">Waiting for data…</p>
  {:else if error !== null}
    <p class="muted">Internet health unavailable: {error}</p>
  {:else if disabled}
    <p class="muted">Internet health checks are turned off.</p>
    <p class="muted hint">Set PISENS_NETQ=on to ping the router and beyond.</p>
  {:else if !netq}
    <p class="muted">The latest snapshot has no internet health data.</p>
  {:else}
    <div class="summary">
      {#if status}
        <span class="badge" data-status={status}>{STATUS_LABEL[status]}</span>
      {/if}
      {#if outage}
        <span class="outage" class:ongoing={outage.end === null}>
          {describeOutage(outage, now)}
        </span>
      {/if}
    </div>

    {#if status === 'pending'}
      <p class="muted">Running the first check…</p>
    {/if}

    {#if targets.length > 0}
      <ul class="targets">
        {#each targets as t (t.key)}
          {@const label = targetLabel(t)}
          {@const latency = hist(latencyKey(t))}
          <li>
            <div class="head">
              <span class="name">{label}</span>
              {#if t.kind === 'gateway' && t.address}
                <span class="muted mono">{t.address}</span>
              {/if}
              {#if t.method === 'tcp'}
                <span
                  class="method"
                  title="Timed with a TCP connect, as this system does not allow ICMP ping"
                  >TCP</span
                >
              {/if}
            </div>
            <dl class="stats">
              <div>
                <dt>Latency</dt>
                <dd class="num">{formatMs(t.latency_ms)}</dd>
              </div>
              <div>
                <dt>Jitter</dt>
                <dd class="num">{formatMs(t.jitter_ms)}</dd>
              </div>
              <div>
                <dt>Loss</dt>
                <dd
                  class="num"
                  class:warn={t.loss_pct > 0 && t.loss_pct < 100}
                  class:bad={t.loss_pct >= 100}
                >
                  {formatLoss(t.loss_pct)}
                </dd>
              </div>
            </dl>
            {#if latency[0].length > 1}
              <Sparkline
                data={latency}
                min={0}
                label="{label} latency, last 5 minutes"
                height={28}
              />
            {/if}
            {#if t.error && t.loss_pct >= 100}
              <p class="muted error">{t.error}</p>
            {/if}
          </li>
        {/each}
      </ul>
    {/if}

    {#if dns}
      <div class="dns">
        <div class="head">
          <span class="name">DNS</span>
          {#if dns.resolver}
            <span class="muted mono">{dns.resolver}</span>
          {/if}
        </div>
        <dl class="stats">
          <div>
            <dt>Lookup</dt>
            <dd class="num" class:bad={!dns.ok}>
              {dns.ok ? formatMs(dns.lookup_ms) : 'Failed'}
            </dd>
          </div>
          <div class="wide">
            <dt>Last failure</dt>
            <dd class="num">
              {#if !dns.ok}
                Now
              {:else if dnsFailedAt !== null}
                {formatClock(dnsFailedAt * 1000, now)}
              {:else if dns.failures > 0}
                <span title="Failed lookups since pisens started"
                  >{dns.failures} since start</span
                >
              {:else}
                None
              {/if}
            </dd>
          </div>
        </dl>
        {#if !dns.ok && dns.error}
          <p class="muted error">{dns.error}</p>
        {/if}
      </div>
    {/if}
  {/if}
</Card>

<style>
  .hint {
    font-size: 0.75rem;
  }

  .summary {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-1) var(--space-3);
    margin-bottom: var(--space-3);
    font-size: 0.875rem;
  }

  .badge {
    --badge-color: var(--color-unknown);

    padding: 0 var(--space-2);
    border: 1px solid var(--badge-color);
    border-radius: 999px;
    color: var(--badge-color);
    font-size: 0.8125rem;
    font-weight: 600;
    line-height: 1.5rem;
    white-space: nowrap;
  }

  .badge[data-status='good'] {
    --badge-color: var(--color-ok);
  }

  .badge[data-status='degraded'] {
    --badge-color: var(--color-warning);
  }

  .badge[data-status='down'] {
    --badge-color: var(--color-critical);
  }

  .outage {
    color: var(--color-text-muted);
  }

  .outage.ongoing {
    color: var(--color-critical);
  }

  .targets {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .targets li + li,
  .dns {
    padding-top: var(--space-3);
    border-top: 1px solid var(--color-border);
  }

  .dns {
    margin-top: var(--space-3);
  }

  .head {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: var(--space-1) var(--space-2);
    margin-bottom: var(--space-1);
  }

  .name {
    font-weight: 600;
  }

  .method {
    margin-left: auto;
    padding: 0 var(--space-1);
    border: 1px solid var(--color-border);
    border-radius: 4px;
    color: var(--color-text-muted);
    font-size: 0.6875rem;
    line-height: 1.125rem;
  }

  .stats {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: var(--space-2);
    margin: 0 0 var(--space-1);
  }

  .stats .wide {
    grid-column: span 2;
  }

  dt {
    color: var(--color-text-muted);
    font-size: 0.75rem;
  }

  dd {
    margin: 0;
  }

  .warn {
    color: var(--color-warning);
  }

  .bad {
    color: var(--color-critical);
  }

  .error {
    margin: var(--space-1) 0 0;
    font-size: 0.75rem;
    overflow-wrap: anywhere;
  }

  .mono {
    font-family: var(--font-mono);
    font-size: 0.8125rem;
  }

  .num {
    font-variant-numeric: tabular-nums;
  }
</style>
