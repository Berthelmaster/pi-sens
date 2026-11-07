import { describe, expect, it } from 'vitest'
import {
  pickCpuSensor,
  sensorDisplayName,
  tempStatus,
  type TemperatureSensor,
} from './temperature'

function sensor(name: string, source: string): TemperatureSensor {
  return { name, source, type: name, temp_c: 50 }
}

describe('tempStatus', () => {
  it('bands at 60 and 75 °C', () => {
    expect(tempStatus(45)).toBe('ok')
    expect(tempStatus(59.9)).toBe('ok')
    expect(tempStatus(60)).toBe('warning')
    expect(tempStatus(74.9)).toBe('warning')
    expect(tempStatus(75)).toBe('critical')
    expect(tempStatus(86)).toBe('critical')
  })

  it('is unknown without a reading', () => {
    expect(tempStatus(undefined)).toBe('unknown')
    expect(tempStatus(NaN)).toBe('unknown')
  })
})

describe('pickCpuSensor', () => {
  it('prefers the sensor named cpu', () => {
    const sensors = [
      sensor('nvme', 'hwmon3/temp1'),
      sensor('acpitz', 'thermal_zone0'),
      sensor('cpu', 'thermal_zone1'),
    ]
    expect(pickCpuSensor(sensors)?.name).toBe('cpu')
  })

  it('falls back to the first thermal zone', () => {
    const sensors = [
      sensor('nvme', 'hwmon3/temp1'),
      sensor('soc', 'thermal_zone0'),
    ]
    expect(pickCpuSensor(sensors)?.name).toBe('soc')
  })

  it('finds nothing without a zone', () => {
    expect(pickCpuSensor([sensor('nvme', 'hwmon3/temp1')])).toBeUndefined()
    expect(pickCpuSensor([])).toBeUndefined()
  })
})

describe('sensorDisplayName', () => {
  it('names well-known sensors', () => {
    expect(sensorDisplayName('rp1')).toBe('RP1')
    expect(sensorDisplayName('nvme')).toBe('NVMe')
    expect(sensorDisplayName('nvme_2')).toBe('NVMe 2')
    expect(sensorDisplayName('nvme_sensor_1')).toBe('NVMe sensor 1')
  })

  it('spaces out other names', () => {
    expect(sensorDisplayName('x86_pkg_temp')).toBe('x86 pkg temp')
    expect(sensorDisplayName('acpitz')).toBe('acpitz')
  })
})
