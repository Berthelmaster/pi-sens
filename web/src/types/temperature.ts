import type { CollectorError, Snapshot } from './snapshot'

/**
 * The `temperature` collector's section of the snapshot (temperatureData in
 * internal/collectors/temperature.go). `sensors` is empty on a machine
 * without thermal zones or hwmon chips.
 */
export interface TemperatureSection {
  sensors: TemperatureSensor[]
}

export interface TemperatureSensor {
  /**
   * Short, stable name, also used in the series key `temp.<name>_c`: "cpu",
   * "rp1", "nvme", "nvme_2". The SoC zone is always "cpu" on a Pi.
   */
  name: string
  /** Where the reading comes from: "thermal_zone0" or "hwmon3/temp1". */
  source: string
  /** Zone type ("cpu-thermal") or hwmon chip name ("nvme", "rp1_adc"). */
  type: string
  /** hwmon channel label, e.g. "Composite"; missing for thermal zones. */
  label?: string
  temp_c: number
  /** A thermal zone's trip points in kernel order; missing for hwmon. */
  trips?: TemperatureTrip[]
}

export interface TemperatureTrip {
  type: 'critical' | 'hot' | 'passive' | 'active' | string
  temp_c: number
}

/** Series key of a sensor's temperature in °C. */
export function temperatureSeriesKey(name: string): string {
  return `temp.${name}_c`
}

/** The temperature section of a snapshot, or undefined when it has none. */
export function temperatureSection(
  snap: Snapshot | null,
): TemperatureSection | CollectorError | undefined {
  const section = snap?.collectors.temperature
  if (typeof section !== 'object' || section === null) return undefined
  return section as TemperatureSection | CollectorError
}

/** CPU temperature bands: green below WARN, amber below CRITICAL, red above. */
export const TEMP_WARN_C = 60
export const TEMP_CRITICAL_C = 75
/** The Pi 5 firmware starts to soft throttle the CPU here. */
export const PI_THROTTLE_C = 85

/** The colour band of a CPU temperature, as a Card status. */
export function tempStatus(
  celsius: number | null | undefined,
): 'ok' | 'warning' | 'critical' | 'unknown' {
  if (celsius == null || !Number.isFinite(celsius)) return 'unknown'
  if (celsius >= TEMP_CRITICAL_C) return 'critical'
  if (celsius >= TEMP_WARN_C) return 'warning'
  return 'ok'
}

/**
 * The sensor to show as the CPU temperature: the one named "cpu" (the SoC
 * zone on a Pi), or else the first thermal zone, which is the SoC on most
 * other boards. undefined when there is neither.
 */
export function pickCpuSensor(
  sensors: readonly TemperatureSensor[],
): TemperatureSensor | undefined {
  return (
    sensors.find((s) => s.name === 'cpu') ??
    sensors.find((s) => s.source?.startsWith('thermal_zone'))
  )
}

const nameWords: Record<string, string> = {
  cpu: 'CPU',
  gpu: 'GPU',
  soc: 'SoC',
  rp1: 'RP1',
  nvme: 'NVMe',
  ssd: 'SSD',
  pmic: 'PMIC',
}

/**
 * A readable name for a sensor from its series name: "nvme" -> "NVMe",
 * "nvme_2" -> "NVMe 2", "rp1" -> "RP1", "x86_pkg_temp" -> "x86 pkg temp".
 */
export function sensorDisplayName(name: string): string {
  const words = name.split('_').filter((w) => w !== '')
  if (words.length === 0) return name
  return words.map((w) => nameWords[w] ?? w).join(' ')
}
