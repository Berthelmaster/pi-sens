// Pure helpers for StorageWidget, kept out of the component so tests can
// import them without loading the chart components, which need a browser.

import type { CardStatus } from '../components/Card.svelte'
import { gaugeStatus } from '../components/Gauge.svelte'
import { formatBytes, PLACEHOLDER } from '../lib/format'
import type { SeriesHistory } from '../lib/live'
import type { FilesystemMount } from '../types/storage'

/** A filesystem turns the card to warning at this used percentage. */
export const FS_WARN_PCT = 80
/** A filesystem turns the card to critical at this used percentage. */
export const FS_CRITICAL_PCT = 90
/** Filesystems shown before the rest are folded behind a button. */
export const VISIBLE_MOUNTS = 5

export function isNumber(v: unknown): v is number {
  return typeof v === 'number' && Number.isFinite(v)
}

/** A byte rate, e.g. 1536 -> "1.5 KiB/s". */
export function formatRate(bps: number | null | undefined): string {
  return isNumber(bps) ? `${formatBytes(bps)}/s` : PLACEHOLDER
}

/** Operations per second, with one decimal below 10: 2.5, 14, 1200. */
export function formatIops(iops: number | null | undefined): string {
  if (!isNumber(iops)) return PLACEHOLDER
  return iops < 10 ? iops.toFixed(1) : String(Math.round(iops))
}

/** The health of one filesystem from how full it is. */
export function mountStatus(m: FilesystemMount): CardStatus {
  return gaugeStatus(m.used_pct, FS_WARN_PCT, FS_CRITICAL_PCT)
}

const rank: Record<CardStatus, number> = {
  unknown: 0,
  ok: 1,
  warning: 2,
  critical: 3,
}

/**
 * The card status for a list of filesystems: the fullest one decides.
 * Mounts without usage (statfs failed) are skipped; undefined when none has
 * any, so the card stays neutral.
 */
export function storageStatus(
  mounts: FilesystemMount[],
): CardStatus | undefined {
  let worst: CardStatus | undefined
  for (const m of mounts) {
    const s = mountStatus(m)
    if (s === 'unknown') continue
    if (worst === undefined || rank[s] > rank[worst]) worst = s
  }
  return worst
}

/**
 * Adds two series histories sample by sample, e.g. read and write bytes
 * per second into total throughput. Only times present in both are kept;
 * the diskio collector always writes the two together.
 */
export function sumHistories(
  a: SeriesHistory,
  b: SeriesHistory,
): SeriesHistory {
  const byTime = new Map<number, number>()
  a[0].forEach((t, i) => byTime.set(t, a[1][i]))
  const times: number[] = []
  const values: number[] = []
  b[0].forEach((t, i) => {
    const v = byTime.get(t)
    if (v !== undefined) {
      times.push(t)
      values.push(v + b[1][i])
    }
  })
  return [times, values]
}

/** "3 more", "3 more (1 critical)", for the button that unfolds mounts. */
export function moreLabel(hidden: FilesystemMount[]): string {
  const counts = { warning: 0, critical: 0 }
  for (const m of hidden) {
    const s = mountStatus(m)
    if (s === 'warning' || s === 'critical') counts[s]++
  }
  const notes = [
    counts.critical > 0 ? `${counts.critical} critical` : '',
    counts.warning > 0 ? `${counts.warning} warning` : '',
  ].filter(Boolean)
  const more = `${hidden.length} more`
  return notes.length > 0 ? `${more} (${notes.join(', ')})` : more
}
