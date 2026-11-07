import type { CollectorError, Snapshot } from './snapshot'

/**
 * The `fan` collector's section of the snapshot (fanData in
 * internal/collectors/fan.go). Without a cooler only `present: false` is
 * sent; with one, readings the backend could not get are left out.
 */
export interface FanSection {
  present: boolean
  /** hwmon chip the readings come from, e.g. "hwmon3". */
  hwmon?: string
  /** Chip name, "pwmfan" on a Pi 5. */
  chip?: string
  rpm?: number
  /** Raw duty cycle, 0 to 255. */
  pwm?: number
  /** The duty cycle in percent. */
  pwm_pct?: number
  /** Thermal cooling device driving the fan, e.g. "cooling_device0". */
  cooling_device?: string
  /** Fan level the thermal governor picked, 0 (off) to max_level. */
  level?: number
  max_level?: number
}

/** Series keys the fan collector reports while a fan is present. */
export const FAN_RPM_SERIES = 'fan.rpm'
export const FAN_PWM_SERIES = 'fan.pwm_pct'

/** The fan section of a snapshot, or undefined when it has none. */
export function fanSection(
  snap: Snapshot | null,
): FanSection | CollectorError | undefined {
  const section = snap?.collectors.fan
  if (typeof section !== 'object' || section === null) return undefined
  return section as FanSection | CollectorError
}
