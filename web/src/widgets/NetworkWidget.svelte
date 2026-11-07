<script lang="ts">
  import { derived } from 'svelte/store'
  import type uPlot from 'uplot'
  import Card from '../components/Card.svelte'
  import Sparkline from '../components/Sparkline.svelte'
  import { alignHistories, seriesColor } from '../lib/chart'
  import {
    formatBitsPerSecond,
    formatBytes,
    formatHz,
    formatPercent,
    PLACEHOLDER,
  } from '../lib/format'
  import { seriesHistory, snapshot, type SeriesHistory } from '../lib/live'
  import {
    formatLinkSpeed,
    interfaceViews,
    isLinkLocal6,
    netinfoSection,
    networkSection,
    niceCeil,
    num,
    okSection,
    SIGNAL_BARS,
    signalBars,
    stateLabel,
    text,
    type InterfaceView,
  } from '../types/network'
  import { isCollectorError } from '../types/snapshot'

  const RX_COLOR = seriesColor(0)
  const TX_COLOR = seriesColor(1)

  let networkRaw = $derived(networkSection($snapshot))
  let netinfoRaw = $derived(netinfoSection($snapshot))
  let networkError = $derived(
    isCollectorError(networkRaw) ? networkRaw.error : null,
  )
  let netinfoError = $derived(
    isCollectorError(netinfoRaw) ? netinfoRaw.error : null,
  )
  let network = $derived(okSection(networkRaw))
  let netinfo = $derived(okSection(netinfoRaw))

  let views = $derived(interfaceViews(network, netinfo))
  let gateway = $derived(netinfo?.default_gateway)

  // One history pair per interface. The key list is a string so the
  // subscriptions are only rebuilt when the set of interfaces changes, not on
  // every snapshot.
  interface RateHistory {
    rx: SeriesHistory
    tx: SeriesHistory
  }
  let historyKeys = $derived(
    views.flatMap((v) => (v.seriesKey ? [v.seriesKey] : [])).join('\n'),
  )
  let histories = $state.raw<Record<string, RateHistory>>({})
  $effect(() => {
    const keys = historyKeys ? historyKeys.split('\n') : []
    if (keys.length === 0) {
      histories = {}
      return
    }
    const stores = keys.flatMap((k) => [
      seriesHistory(`${k}.rx_bps`),
      seriesHistory(`${k}.tx_bps`),
    ])
    return derived(stores, (values) => {
      const out: Record<string, RateHistory> = {}
      keys.forEach((k, i) => {
        out[k] = { rx: values[2 * i], tx: values[2 * i + 1] }
      })
      return out
    }).subscribe((v) => (histories = v))
  })

  /** Bytes per second history as bits per second, for the sparkline. */
  function toBits([t, v]: SeriesHistory): SeriesHistory {
    return [t, v.map((b) => b * 8)]
  }

  /**
   * Whether the network collector counts traffic on this interface. Bridges
   * and docker interfaces only come from netinfo, so they get no rate line.
   */
  function hasCounters(v: InterfaceView): boolean {
    return (
      v.seriesKey !== undefined ||
      v.rxBits !== undefined ||
      v.txBits !== undefined
    )
  }

  interface Chart {
    /** `[times, rx bits/s, tx bits/s]`. */
    data: uPlot.AlignedData
    max: number
  }

  /** The rx and tx lines of an interface for one sparkline. */
  function chartOf(v: InterfaceView): Chart | undefined {
    const h = v.seriesKey ? histories[v.seriesKey] : undefined
    if (!h || (h.rx[0].length < 2 && h.tx[0].length < 2)) return undefined
    const rx = toBits(h.rx)
    const tx = toBits(h.tx)
    let peak = 0
    for (const b of rx[1]) peak = Math.max(peak, b)
    for (const b of tx[1]) peak = Math.max(peak, b)
    // At least 8 kb/s so an idle link stays a flat line at the bottom
    // instead of blowing up background chatter.
    return {
      data: alignHistories(rx, tx),
      max: niceCeil(Math.max(peak, 8000)),
    }
  }

  let bothFailed = $derived(!!networkError && !!netinfoError)
</script>

<Card title="Network" status={bothFailed ? 'unknown' : undefined}>
  {#if networkError}
    <p class="muted note">Traffic unavailable: {networkError}</p>
  {/if}
  {#if netinfoError}
    <p class="muted note">Interface details unavailable: {netinfoError}</p>
  {/if}

  {#if !bothFailed}
    {#if netinfo}
      <p class="gateway">
        <span class="label">Default gateway</span>
        {#if gateway && text(gateway.address)}
          <span class="mono">{gateway.address}</span>
          {#if text(gateway.interface)}
            <span class="muted">via {gateway.interface}</span>
          {/if}
        {:else}
          <span class="muted">None</span>
        {/if}
      </p>
    {/if}

    {#if !$snapshot}
      <p class="muted">Waiting for data…</p>
    {:else if views.length === 0}
      <p class="muted">No network interfaces.</p>
    {/if}

    <ul class="ifaces">
      {#each views as v (v.name)}
        {@const counted = hasCounters(v)}
        {@const chart = counted ? chartOf(v) : undefined}
        <li class="iface" class:inactive={!v.active}>
          <div class="head">
            <span class="name mono">{v.name}</span>
            <span class="badge" data-state={v.state}>{stateLabel(v.state)}</span
            >
            {#if counted}
              <span class="rates num">
                <span title="Receive">
                  <span class="swatch" style:background={RX_COLOR}></span>↓
                  {formatBitsPerSecond(v.rxBits)}
                </span>
                <span title="Transmit">
                  <span class="swatch" style:background={TX_COLOR}></span>↑
                  {formatBitsPerSecond(v.txBits)}
                </span>
              </span>
            {/if}
          </div>

          {#if chart && v.active}
            <div class="spark">
              <Sparkline
                data={chart.data}
                min={0}
                max={chart.max}
                colors={[RX_COLOR, TX_COLOR]}
                height={32}
                label="{v.name} receive and transmit rate, last 5 minutes"
              />
              <span class="scale num" aria-hidden="true">
                {formatBitsPerSecond(chart.max, 0)}
              </span>
            </div>
          {/if}

          <dl>
            {#if v.wifi}
              <dt>Wi-Fi</dt>
              <dd>
                <span
                  class="signal"
                  role="img"
                  aria-label="Signal {signalBars(
                    num(v.wifi.signal_dbm),
                  )} of {SIGNAL_BARS} bars"
                >
                  {#each { length: SIGNAL_BARS }, i (i)}
                    <span
                      class="bar"
                      class:on={i < signalBars(num(v.wifi.signal_dbm))}
                      style:height="{((i + 1) / SIGNAL_BARS) * 100}%"
                    ></span>
                  {/each}
                </span>
                <span class="num">
                  {num(v.wifi.signal_dbm) ?? PLACEHOLDER} dBm
                  <span class="muted"
                    >· {formatPercent(num(v.wifi.quality_pct), 0)}</span
                  >
                </span>
              </dd>
              {#if text(v.wifi.ssid)}
                <dt>SSID</dt>
                <dd>
                  {v.wifi.ssid}
                  {#if num(v.wifi.frequency_mhz)}
                    <span class="muted num"
                      >· {formatHz(
                        (num(v.wifi.frequency_mhz) as number) * 1e6,
                      )}</span
                    >
                  {/if}
                </dd>
              {/if}
            {:else if v.wireless && v.active}
              <dt>Wi-Fi</dt>
              <dd class="muted">Not connected</dd>
            {/if}

            {#if v.ipv4.length > 0}
              <dt>IPv4</dt>
              <dd class="mono">
                {#each v.ipv4 as a (a)}<div>{a}</div>{/each}
              </dd>
            {/if}
            {#if v.ipv6.length > 0}
              <dt>IPv6</dt>
              <dd class="mono">
                {#each v.ipv6 as a (a)}
                  <div class:muted={isLinkLocal6(a)}>{a}</div>
                {/each}
              </dd>
            {/if}
            {#if v.mac}
              <dt>MAC</dt>
              <dd class="mono">{v.mac}</dd>
            {/if}
            {#if v.speedMbps !== undefined}
              <dt>Link</dt>
              <dd class="num">{formatLinkSpeed(v.speedMbps)}</dd>
            {/if}
            {#if v.rxBytes !== undefined || v.txBytes !== undefined}
              <dt title="Since the interface came up">Total</dt>
              <dd class="num">
                ↓ {formatBytes(v.rxBytes)}
                <span class="muted">·</span>
                ↑ {formatBytes(v.txBytes)}
              </dd>
            {/if}
          </dl>
        </li>
      {/each}
    </ul>
  {/if}
</Card>

<style>
  .note {
    margin: 0 0 var(--space-2);
    font-size: 0.875rem;
  }

  .gateway {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: var(--space-2);
    margin: 0 0 var(--space-3);
    font-size: 0.875rem;
  }

  .label {
    color: var(--color-text-muted);
  }

  .ifaces {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .iface + .iface {
    padding-top: var(--space-3);
    border-top: 1px solid var(--color-border);
  }

  .iface.inactive {
    opacity: 0.5;
  }

  .head {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-1) var(--space-2);
    margin-bottom: var(--space-2);
  }

  .name {
    font-weight: 600;
  }

  .badge {
    --badge-color: var(--color-unknown);

    padding: 0 var(--space-2);
    border: 1px solid var(--badge-color);
    border-radius: 999px;
    color: var(--badge-color);
    font-size: 0.75rem;
    line-height: 1.25rem;
    white-space: nowrap;
  }

  .badge[data-state='up'] {
    --badge-color: var(--color-ok);
  }

  .badge[data-state='dormant'],
  .badge[data-state='testing'] {
    --badge-color: var(--color-warning);
  }

  .rates {
    display: flex;
    gap: var(--space-3);
    margin-left: auto;
    font-size: 0.875rem;
    white-space: nowrap;
  }

  .swatch {
    display: inline-block;
    width: 0.5rem;
    height: 0.5rem;
    border-radius: 2px;
    vertical-align: middle;
  }

  .spark {
    position: relative;
    margin-bottom: var(--space-2);
  }

  .scale {
    position: absolute;
    top: 0;
    right: 0;
    padding: 0 var(--space-1);
    color: var(--color-text-muted);
    font-size: 0.6875rem;
    line-height: 1;
    background: var(--color-surface);
    opacity: 0.85;
  }

  dl {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr);
    gap: var(--space-1) var(--space-4);
    margin: 0;
  }

  dt {
    color: var(--color-text-muted);
    font-size: 0.875rem;
    line-height: 1.5rem;
  }

  dd {
    margin: 0;
    line-height: 1.5rem;
    overflow-wrap: anywhere;
  }

  .mono {
    font-family: var(--font-mono);
    font-size: 0.875rem;
  }

  .num {
    font-variant-numeric: tabular-nums;
  }

  .signal {
    display: inline-flex;
    align-items: flex-end;
    gap: 2px;
    height: 0.875rem;
    margin-right: var(--space-2);
    vertical-align: -1px;
  }

  .bar {
    width: 3px;
    border-radius: 1px;
    background: var(--color-border);
  }

  .bar.on {
    background: var(--color-text);
  }
</style>
