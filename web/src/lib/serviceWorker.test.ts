import { describe, expect, it, vi } from 'vitest'
import { registerServiceWorker, servedOffline } from './serviceWorker'

function host(isSecureContext: boolean, register = vi.fn(async () => ({}))) {
  return {
    isSecureContext,
    navigator: {
      serviceWorker: { register } as unknown as ServiceWorkerContainer,
    },
  }
}

describe('registerServiceWorker', () => {
  it('registers /sw.js in a secure context', () => {
    const h = host(true)
    expect(registerServiceWorker(h)).toBe(true)
    expect(h.navigator.serviceWorker.register).toHaveBeenCalledWith('/sw.js')
  })

  it('does nothing over plain http on the LAN', () => {
    const h = host(false)
    expect(registerServiceWorker(h)).toBe(false)
    expect(h.navigator.serviceWorker.register).not.toHaveBeenCalled()
  })

  it('does nothing without service worker support', () => {
    expect(
      registerServiceWorker({ isSecureContext: true, navigator: {} }),
    ).toBe(false)
  })

  it('only warns when registration fails', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const error = vi.spyOn(console, 'error')
    const h = host(
      true,
      vi.fn(async () => {
        throw new Error('redirected')
      }),
    )
    registerServiceWorker(h)
    await vi.waitFor(() => expect(warn).toHaveBeenCalled())
    expect(error).not.toHaveBeenCalled()
    warn.mockRestore()
    error.mockRestore()
  })
})

describe('servedOffline', () => {
  it('spots the marker the worker adds', () => {
    const doc = (found: boolean) => ({
      querySelector: vi.fn(() => (found ? ({} as Element) : null)),
    })
    const marked = doc(true)
    expect(servedOffline(marked)).toBe(true)
    expect(marked.querySelector).toHaveBeenCalledWith(
      'meta[name="pisens-offline"]',
    )
    expect(servedOffline(doc(false))).toBe(false)
  })
})
