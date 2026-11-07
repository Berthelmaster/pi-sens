import { describe, expect, it } from 'vitest'
import { theme } from './theme.svelte'

describe('theme outside a browser', () => {
  it('lets components that use the theme be imported', async () => {
    // These read theme.current, which used to touch window on import.
    await expect(
      import('../components/Sparkline.svelte'),
    ).resolves.toBeDefined()
    await expect(
      import('../components/TimeSeriesChart.svelte'),
    ).resolves.toBeDefined()
    await expect(
      import('../components/ThemeToggle.svelte'),
    ).resolves.toBeDefined()
    await expect(
      import('../widgets/TemperatureWidget.svelte'),
    ).resolves.toBeDefined()
  })

  it('falls back to dark and can still be toggled', () => {
    expect(theme.current).toBe('dark')
    theme.toggle()
    expect(theme.current).toBe('light')
    theme.toggle()
    expect(theme.current).toBe('dark')
  })
})
