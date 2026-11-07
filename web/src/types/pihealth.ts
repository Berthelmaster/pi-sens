import { num, PLACEHOLDER } from '../lib/format'
import type { CollectorError, Snapshot } from './snapshot'

/**
 * The `throttle` collector's section of the snapshot (throttleData in
 * internal/collectors/throttle.go). Without a usable vcgencmd only
 * `available: false` is sent.
 */
export interface ThrottleSection {
  available: boolean
  /** The get_throttled value as lowercase hex, e.g. "0x50005". */
  raw?: string
  flags?: ThrottleFlags
  /** The supply's current rating in mA (Pi 5 only). */
  psu_max_current_ma?: number
  /** Whether the supply offers the 5000 mA a Pi 5 needs (Pi 5 only). */
  psu_ok?: boolean
  /**
   * Whether `usb_max_current_enable=1` lifts the USB current limit of a
   * supply below 5 A. Missing if the firmware does not say.
   */
  usb_max_current_enable?: boolean
  /** Under-voltage events since pisens started. */
  undervoltage_events?: number
  /** When the latest under-voltage event began, RFC 3339. */
  last_undervoltage?: string
}

/** Bits 0-3 of get_throttled are the current state, 16-19 "since boot". */
export interface ThrottleFlags {
  under_voltage: boolean
  arm_freq_capped: boolean
  throttled: boolean
  soft_temp_limit: boolean
  under_voltage_occurred: boolean
  arm_freq_capped_occurred: boolean
  throttled_occurred: boolean
  soft_temp_limit_occurred: boolean
}

/**
 * The `power` collector's section (powerData in
 * internal/collectors/power.go). Without a usable vcgencmd only
 * `available: false` is sent; `pmic` is only there on a Pi 5.
 */
export interface PowerSection {
  available: boolean
  volts?: PowerVolts
  clocks_mhz?: PowerClocks
  pmic?: PowerPmic
}

export interface PowerVolts {
  core: number
  sdram_c: number
  sdram_i: number
  sdram_p: number
}

export interface PowerClocks {
  arm: number
  core: number
  /** The GPU (VideoCore 3D) clock. */
  v3d: number
}

export interface PowerPmic {
  /** The 5 V input supply; missing if the PMIC does not report it. */
  ext5v_v?: number
  /**
   * Sum of the PMIC rails. USB devices and the PMIC's own losses are not
   * included, so the board draws somewhat more than this from the supply.
   */
  total_w: number
  rails: PowerRail[]
}

export interface PowerRail {
  name: string
  volts: number
  amps: number
  watts: number
}

/** Series key of the estimated power draw (Pi 5 only). */
export const POWER_SERIES = 'pi.power_w'

/** Below this the 5 V supply is too weak for a Pi 5 under load. */
export const EXT5V_WARN_V = 4.8

function section(snap: Snapshot | null, name: string): object | undefined {
  const s = snap?.collectors[name]
  return typeof s === 'object' && s !== null ? s : undefined
}

/** The throttle section of a snapshot, or undefined when it has none. */
export function throttleSection(
  snap: Snapshot | null,
): ThrottleSection | CollectorError | undefined {
  return section(snap, 'throttle') as
    ThrottleSection | CollectorError | undefined
}

/** The power section of a snapshot, or undefined when it has none. */
export function powerSection(
  snap: Snapshot | null,
): PowerSection | CollectorError | undefined {
  return section(snap, 'power') as PowerSection | CollectorError | undefined
}

export type ThrottleState = 'active' | 'occurred'

export interface ThrottleBadge {
  key: 'under_voltage' | 'arm_freq_capped' | 'throttled' | 'soft_temp_limit'
  label: string
  /** Set right now, or only at some point since boot. */
  state: ThrottleState
  /** Longer explanation for a tooltip. */
  description: string
}

const throttleKinds: readonly Omit<ThrottleBadge, 'state'>[] = [
  {
    key: 'under_voltage',
    label: 'Under-voltage',
    description: 'The supply voltage dropped below what the board needs',
  },
  {
    key: 'arm_freq_capped',
    label: 'Frequency capped',
    description: 'The firmware capped the ARM clock',
  },
  {
    key: 'throttled',
    label: 'Throttled',
    description: 'The firmware throttled the CPU',
  },
  {
    key: 'soft_temp_limit',
    label: 'Soft temperature limit',
    description: 'The soft temperature limit is lowering the clocks',
  },
]

/**
 * One badge per flag that is set, in a fixed order. A flag that is set now
 * is `active` even though its "since boot" bit is set too; one that is only
 * set since boot is `occurred`. Empty when all is well.
 */
export function throttleBadges(
  flags: Partial<ThrottleFlags> | undefined,
): ThrottleBadge[] {
  if (!flags) return []
  const badges: ThrottleBadge[] = []
  for (const kind of throttleKinds) {
    if (flags[kind.key] === true) {
      badges.push({ ...kind, state: 'active' })
    } else if (flags[`${kind.key}_occurred`] === true) {
      badges.push({ ...kind, state: 'occurred' })
    }
  }
  return badges
}

/** Whether the supply voltage is low enough to warn about. */
export function supplyLow(volts: number | undefined): boolean {
  return volts !== undefined && Number.isFinite(volts) && volts < EXT5V_WARN_V
}

/**
 * The Pi health card status: critical while any flag is set now, warning
 * when one was set since boot or the supply is low, ok when the flags are
 * known and clear, unknown without flags.
 */
export function piHealthStatus(
  flags: Partial<ThrottleFlags> | undefined,
  ext5v: number | undefined,
): 'ok' | 'warning' | 'critical' | 'unknown' {
  const badges = throttleBadges(flags)
  if (badges.some((b) => b.state === 'active')) return 'critical'
  if (badges.length > 0 || supplyLow(ext5v)) return 'warning'
  return flags ? 'ok' : 'unknown'
}

export interface PsuSummary {
  /** The rating, e.g. "5 A". */
  rating: string
  ok: boolean
  /** Why a supply below 5 A matters, or undefined when it is fine. */
  note?: string
}

/**
 * The supply rating for display, or undefined when the board does not
 * report it (anything before the Pi 5).
 */
export function psuSummary(
  throttle: Partial<ThrottleSection> | undefined,
): PsuSummary | undefined {
  const ma = num(throttle?.psu_max_current_ma)
  if (ma === undefined) return undefined
  // Trust the backend's verdict, but work it out if it is missing.
  const ok =
    typeof throttle?.psu_ok === 'boolean' ? throttle.psu_ok : ma >= 5000
  const rating = `${Number((ma / 1000).toFixed(2))} A`
  if (ok) return { rating, ok }
  return {
    rating,
    ok,
    note:
      throttle?.usb_max_current_enable === true
        ? 'USB limit lifted in config.txt'
        : 'USB current limited',
  }
}

/**
 * Describes the under-voltage events since pisens started, e.g. "3 times,
 * last 14:02". The time is local and gets a date when it is not today.
 * Undefined when the count is unknown.
 */
export function undervoltageSummary(
  throttle: Partial<ThrottleSection> | undefined,
  now: Date = new Date(),
): string | undefined {
  const count = num(throttle?.undervoltage_events)
  if (count === undefined) return undefined
  if (count === 0) return 'none since pisens started'
  const times = count === 1 ? 'once' : `${count} times`
  const last = throttle?.last_undervoltage
    ? new Date(throttle.last_undervoltage)
    : undefined
  if (!last || Number.isNaN(last.getTime())) return times
  return `${times}, last ${formatEventTime(last, now)}`
}

const clockFormat = new Intl.DateTimeFormat('en-GB', {
  hour: '2-digit',
  minute: '2-digit',
})
const dateFormat = new Intl.DateTimeFormat('en-GB', {
  day: 'numeric',
  month: 'short',
})

/** "14:02" for a time today, "5 Nov 14:02" for an earlier day. */
function formatEventTime(t: Date, now: Date): string {
  const clock = clockFormat.format(t)
  return t.toDateString() === now.toDateString()
    ? clock
    : `${dateFormat.format(t)} ${clock}`
}

/** Formats a voltage, e.g. 5.0812 -> "5.08 V", 0.72 -> "0.720 V". */
export function formatVolts(volts: number | undefined): string {
  if (volts === undefined || !Number.isFinite(volts)) return PLACEHOLDER
  // Core voltages sit below 1 V and move in millivolt steps.
  return `${volts.toFixed(Math.abs(volts) < 1 ? 3 : 2)} V`
}

/** Formats a power draw, e.g. 3.14159 -> "3.14 W". */
export function formatWatts(watts: number | undefined): string {
  if (watts === undefined || !Number.isFinite(watts)) return PLACEHOLDER
  return `${watts.toFixed(2)} W`
}
