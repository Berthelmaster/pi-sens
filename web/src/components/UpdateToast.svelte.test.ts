import { fireEvent, render, screen } from '@testing-library/svelte'
import { tick } from 'svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { watchForUpdates } from '../lib/update'
import { startUpdateWatcher, updatePrompt } from '../lib/updatePrompt.svelte'
import UpdateToast from './UpdateToast.svelte'

// No live stream in these tests: only the connection state is read.
vi.mock('../lib/live', async () => {
  const { writable } = await import('svelte/store')
  return { connection: writable({ state: 'connecting' }) }
})

// The watcher is tested in lib/update.test.ts; here it is a stand-in whose
// reload the toast's button should reach.
const watcher = { check: vi.fn(), reload: vi.fn(), stop: vi.fn() }
vi.mock('../lib/update', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/update')>()),
  watchForUpdates: vi.fn(() => watcher),
}))

beforeEach(async () => {
  vi.clearAllMocks()
  updatePrompt.ready = false
  await startUpdateWatcher(null)
})

function setReady(ready: boolean) {
  const onReady = vi.mocked(watchForUpdates).mock.calls[0][3]
  onReady(ready)
}

describe('UpdateToast', () => {
  it('announces a new version in a live region and offers a reload', async () => {
    render(UpdateToast)
    const region = screen.getByRole('status')
    expect(region.textContent?.trim()).toBe('')
    expect(screen.queryByRole('button')).toBeNull()

    setReady(true)
    await tick()
    expect(region.textContent).toContain('A new version of pisens is ready')

    const reload = screen.getByRole('button', { name: 'Reload' })
    reload.focus()
    expect(document.activeElement).toBe(reload)
    await fireEvent.click(reload)
    expect(watcher.reload).toHaveBeenCalledTimes(1)
    const busy = screen.getByRole('button', { name: 'Reloading…' })
    expect((busy as HTMLButtonElement).disabled).toBe(true)
  })

  it('can be dismissed', async () => {
    render(UpdateToast)
    setReady(true)
    await tick()
    await fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }))
    expect(screen.queryByRole('button', { name: 'Reload' })).toBeNull()
    expect(watcher.reload).not.toHaveBeenCalled()
  })
})
