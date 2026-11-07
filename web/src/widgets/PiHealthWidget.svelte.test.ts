import { render, screen } from '@testing-library/svelte'
import { tick } from 'svelte'
import type { Writable } from 'svelte/store'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { snapshot as mockedSnapshot } from '../lib/live'
import type {
  PowerSection,
  RtcSection,
  ThrottleSection,
} from '../types/pihealth'
import type { Snapshot } from '../types/snapshot'
import PiHealthWidget from './PiHealthWidget.svelte'

// uPlot reads matchMedia as soon as it loads, which jsdom does not have.
vi.hoisted(() => {
  window.matchMedia = () =>
    ({
      matches: false,
      addEventListener() {},
      removeEventListener() {},
    }) as unknown as MediaQueryList
})

// Empty histories keep the sparkline out, so uPlot never needs a canvas.
vi.mock('../lib/live', async () => {
  const { readable, writable } = await import('svelte/store')
  return {
    snapshot: writable(null),
    seriesHistory: () => readable([[], []]),
  }
})

const live = mockedSnapshot as Writable<Snapshot | null>

const flags = {
  under_voltage: false,
  arm_freq_capped: false,
  throttled: false,
  soft_temp_limit: false,
  under_voltage_occurred: true,
  arm_freq_capped_occurred: false,
  throttled_occurred: false,
  soft_temp_limit_occurred: false,
}

const power: PowerSection = {
  available: true,
  volts: { core: 0.72, sdram_c: 0.6, sdram_i: 0.6, sdram_p: 1.1 },
  clocks_mhz: { arm: 2400, core: 910, v3d: 960 },
}

// A local time today, so the widget shows it without a date.
const today = new Date()
today.setHours(14, 2, 0, 0)

function show(throttle: ThrottleSection, rtc?: RtcSection) {
  live.set({
    timestamp: '2026-11-07T14:05:00Z',
    collectors: rtc ? { throttle, power, rtc } : { throttle, power },
    series: {},
  })
  return tick()
}

/** The text of the supply row whose label starts with name. */
function row(name: string): string {
  const dt = screen.getByText(name)
  return dt.closest('div')!.textContent!.replace(/\s+/g, ' ').trim()
}

describe('PiHealthWidget power supply', () => {
  beforeEach(() => live.set(null))

  it('shows a 5 A supply and the under-voltage events', async () => {
    render(PiHealthWidget)
    await show({
      available: true,
      raw: '0x10000',
      flags,
      psu_max_current_ma: 5000,
      psu_ok: true,
      usb_max_current_enable: true,
      undervoltage_events: 3,
      last_undervoltage: today.toISOString(),
    })

    expect(row('Power supply:')).toBe('Power supply: 5 A ✓')
    expect(row('Under-voltage:')).toBe('Under-voltage: 3 times, last 14:02')
  })

  it('warns about a 3 A supply limiting USB current', async () => {
    render(PiHealthWidget)
    await show({
      available: true,
      raw: '0x0',
      flags: { ...flags, under_voltage_occurred: false },
      psu_max_current_ma: 3000,
      psu_ok: false,
      usb_max_current_enable: false,
      undervoltage_events: 0,
    })

    expect(row('Power supply:')).toBe('Power supply: 3 A – USB current limited')
    expect(row('Under-voltage:')).toBe(
      'Under-voltage: none since pisens started',
    )
  })

  it('leaves the supply out on boards without a rating', async () => {
    render(PiHealthWidget)
    await show({
      available: true,
      raw: '0x0',
      flags: { ...flags, under_voltage_occurred: false },
      undervoltage_events: 0,
    })

    expect(screen.queryByText('Power supply:')).toBeNull()
    expect(screen.getByText('Under-voltage:')).toBeTruthy()
  })
})

describe('PiHealthWidget RTC battery', () => {
  beforeEach(() => live.set(null))

  const clear: ThrottleSection = {
    available: true,
    raw: '0x0',
    flags: { ...flags, under_voltage_occurred: false },
  }

  /** The card's status, from the data-status attribute of the Card. */
  function cardStatus(): string | null | undefined {
    return document.querySelector('[data-status]')?.getAttribute('data-status')
  }

  it('shows the voltage and that it is charging', async () => {
    render(PiHealthWidget)
    await show(clear, {
      present: true,
      rtc: 'rtc0',
      battery: true,
      battery_v: 3.021,
      charging: true,
      charging_voltage_v: 3,
    })

    expect(row('RTC battery:')).toBe('RTC battery: 3.02 V, charging')
    expect(screen.getByText('charging').getAttribute('title')).toBe(
      'Trickle charging to 3.00 V',
    )
    expect(screen.getByText('3.02 V').closest('dd')!.classList).not.toContain(
      'low',
    )
    expect(cardStatus()).toBe('ok')
  })

  it('shows a low battery in amber', async () => {
    render(PiHealthWidget)
    await show(clear, {
      present: true,
      rtc: 'rtc0',
      battery: true,
      battery_v: 2.65,
      charging: false,
    })

    expect(row('RTC battery:')).toBe('RTC battery: 2.65 V')
    expect(screen.getByText('2.65 V').closest('dd')!.classList).toContain('low')
    expect(cardStatus()).toBe('warning')
  })

  it('shows nothing without a battery or an RTC', async () => {
    render(PiHealthWidget)
    await show(clear, { present: true, rtc: 'rtc0', battery: false })
    expect(screen.queryByText('RTC battery:')).toBeNull()

    await show(clear, { present: false, battery: false })
    expect(screen.queryByText('RTC battery:')).toBeNull()

    await show(clear)
    expect(screen.queryByText('RTC battery:')).toBeNull()
  })

  it('still shows the battery when vcgencmd is unavailable', async () => {
    render(PiHealthWidget)
    await show(
      { available: false },
      {
        present: true,
        battery: true,
        battery_v: 3.1,
      },
    )

    expect(screen.getByText(/vcgencmd unavailable/)).toBeTruthy()
    expect(row('RTC battery:')).toBe('RTC battery: 3.10 V')
  })
})
