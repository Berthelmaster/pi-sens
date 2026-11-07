<script lang="ts">
  import { derived } from 'svelte/store'
  import Card from '../components/Card.svelte'
  import Gauge from '../components/Gauge.svelte'
  import Sparkline from '../components/Sparkline.svelte'
  import { formatBytes, formatPercent, PLACEHOLDER } from '../lib/format'
  import { seriesHistory, snapshot, type SeriesHistory } from '../lib/live'
  import { isCollectorError } from '../types/snapshot'
  import {
    diskioSection,
    diskSeriesKey,
    filesystemSection,
    type DiskDevice,
    type FilesystemMount,
  } from '../types/storage'
  import {
    formatIops,
    formatRate,
    FS_CRITICAL_PCT,
    FS_WARN_PCT,
    isNumber,
    moreLabel,
    storageStatus,
    sumHistories,
    VISIBLE_MOUNTS,
  } from './storage'

  let fsSection = $derived(filesystemSection($snapshot))
  let fsError = $derived(isCollectorError(fsSection) ? fsSection.error : null)
  let mounts = $derived<FilesystemMount[]>(
    fsSection && !isCollectorError(fsSection) && Array.isArray(fsSection.mounts)
      ? fsSection.mounts
      : [],
  )

  let ioSection = $derived(diskioSection($snapshot))
  let ioError = $derived(isCollectorError(ioSection) ? ioSection.error : null)
  let disks = $derived<DiskDevice[]>(
    ioSection &&
      !isCollectorError(ioSection) &&
      Array.isArray(ioSection.devices)
      ? ioSection.devices
      : [],
  )

  let status = $derived(fsError ? 'unknown' : storageStatus(mounts))

  let expanded = $state(false)
  let shownMounts = $derived(
    expanded ? mounts : mounts.slice(0, VISIBLE_MOUNTS),
  )
  let hiddenMounts = $derived(mounts.slice(VISIBLE_MOUNTS))

  // Combined read + write throughput per disk for the sparklines. The series
  // keys depend on which disks there are, so the subscriptions are redone
  // when that list changes, not on every snapshot.
  let diskNames = $derived(disks.map((d) => d.name).join('\n'))
  let throughput = $state.raw<Record<string, SeriesHistory>>({})
  $effect(() => {
    const names = diskNames === '' ? [] : diskNames.split('\n')
    // Collected here rather than spread from throughput, which would make
    // the effect depend on what it writes.
    const latest: Record<string, SeriesHistory> = {}
    const unsubscribe = names.map((name) =>
      derived(
        [
          seriesHistory(diskSeriesKey(name, 'read_bps')),
          seriesHistory(diskSeriesKey(name, 'write_bps')),
        ],
        ([read, write]) => sumHistories(read, write),
      ).subscribe((h) => {
        latest[name] = h
        throughput = { ...latest }
      }),
    )
    return () => {
      unsubscribe.forEach((u) => u())
      throughput = {}
    }
  })

  const noHistory: SeriesHistory = [[], []]
</script>

<Card title="Storage" {status}>
  {#if !$snapshot}
    <p class="muted">Waiting for data…</p>
  {:else}
    <section aria-labelledby="storage-fs">
      <h3 id="storage-fs">Filesystems</h3>
      {#if fsError}
        <p class="muted note">Filesystems unavailable: {fsError}</p>
      {:else if mounts.length === 0}
        <p class="muted note">No filesystems reported.</p>
      {:else}
        <ul class="mounts">
          {#each shownMounts as m (m.name)}
            <li>
              {#if m.error || !isNumber(m.used_pct)}
                <div class="mount-head">
                  <span class="mountpoint">{m.mountpoint}</span>
                </div>
                <p class="muted note">{m.error ?? 'Usage unknown'}</p>
              {:else}
                <Gauge
                  variant="bar"
                  label={m.mountpoint}
                  value={m.used_pct}
                  warn={FS_WARN_PCT}
                  critical={FS_CRITICAL_PCT}
                  format={(v) => formatPercent(v, 0)}
                />
              {/if}
              <div class="meta">
                <span class="mono">{m.device}</span>
                <span>
                  {m.fstype}{m.read_only ? ' · read-only' : ''}
                </span>
                {#if isNumber(m.size_bytes)}
                  <span class="num size">
                    {formatBytes(m.used_bytes)} / {formatBytes(m.size_bytes)}
                  </span>
                {/if}
              </div>
            </li>
          {/each}
        </ul>
        {#if hiddenMounts.length > 0}
          <button
            type="button"
            class="more"
            aria-expanded={expanded}
            onclick={() => (expanded = !expanded)}
          >
            {expanded ? 'Show fewer' : `Show ${moreLabel(hiddenMounts)}`}
          </button>
        {/if}
      {/if}
    </section>

    <section aria-labelledby="storage-disks">
      <h3 id="storage-disks">Disks</h3>
      {#if ioError}
        <p class="muted note">Disk I/O unavailable: {ioError}</p>
      {:else if disks.length === 0}
        <p class="muted note">No disks reported.</p>
      {:else}
        <ul class="disks">
          {#each disks as d (d.name)}
            <li>
              <div class="disk-head">
                <span class="mono">{d.name}</span>
                {#if d.model}<span class="muted model">{d.model}</span>{/if}
              </div>
              <dl class="rates">
                <div>
                  <dt>Read</dt>
                  <dd class="num">{formatRate(d.rates?.read_bps)}</dd>
                </div>
                <div>
                  <dt>Write</dt>
                  <dd class="num">{formatRate(d.rates?.write_bps)}</dd>
                </div>
                <div>
                  <dt>IOPS r/w</dt>
                  <dd class="num">
                    {d.rates
                      ? `${formatIops(d.rates.read_iops)} / ${formatIops(d.rates.write_iops)}`
                      : PLACEHOLDER}
                  </dd>
                </div>
                <div>
                  <dt>Util</dt>
                  <dd class="num">{formatPercent(d.rates?.util_pct, 0)}</dd>
                </div>
              </dl>
              <Sparkline
                data={throughput[d.name] ?? noHistory}
                min={0}
                height={32}
                label="{d.name} read and write throughput, last 5 minutes"
              />
            </li>
          {/each}
        </ul>
      {/if}
    </section>
  {/if}
</Card>

<style>
  section + section {
    margin-top: var(--space-4);
  }

  h3 {
    margin: 0 0 var(--space-2);
    font-size: 0.75rem;
    font-weight: 600;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--color-text-muted);
  }

  ul {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
  }

  li {
    min-width: 0;
    /* Long mount points and device paths wrap instead of widening the card. */
    overflow-wrap: anywhere;
  }

  .mount-head,
  .disk-head {
    display: flex;
    align-items: baseline;
    gap: var(--space-2);
    margin-bottom: var(--space-1);
  }

  .mountpoint {
    color: var(--color-text-muted);
    font-size: 0.8125rem;
  }

  .meta {
    display: flex;
    flex-wrap: wrap;
    gap: 0 var(--space-3);
    margin-top: var(--space-1);
    color: var(--color-text-muted);
    font-size: 0.75rem;
  }

  .size {
    margin-left: auto;
  }

  .model {
    font-size: 0.8125rem;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .rates {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: var(--space-1) var(--space-3);
    margin: 0 0 var(--space-1);
  }

  .rates div {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: var(--space-2);
    min-width: 0;
  }

  dt {
    color: var(--color-text-muted);
    font-size: 0.75rem;
  }

  dd {
    margin: 0;
    font-size: 0.875rem;
    white-space: nowrap;
  }

  .mono {
    font-family: var(--font-mono);
    font-size: 0.8125rem;
  }

  .num {
    font-variant-numeric: tabular-nums;
  }

  .note {
    margin: 0;
    font-size: 0.875rem;
  }

  .more {
    margin-top: var(--space-2);
    padding: var(--space-1) 0;
    border: 0;
    background: none;
    color: var(--color-accent);
    font: inherit;
    font-size: 0.8125rem;
    cursor: pointer;
  }

  .more:hover {
    text-decoration: underline;
  }
</style>
