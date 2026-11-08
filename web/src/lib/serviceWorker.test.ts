import { describe, expect, it, vi } from 'vitest'
import { registerServiceWorker, servedOffline } from './serviceWorker'

const registration = {} as ServiceWorkerRegistration

function host(
  isSecureContext: boolean,
  register = vi.fn(async () => registration),
) {
  return {
    isSecureContext,
    navigator: {
      serviceWorker: { register } as unknown as ServiceWorkerContainer,
    },
  }
}

describe('registerServiceWorker', () => {
  it('registers /sw.js in a secure context', async () => {
    const h = host(true)
    expect(await registerServiceWorker(h)).toBe(registration)
    expect(h.navigator.serviceWorker.register).toHaveBeenCalledWith('/sw.js')
  })

  it('does nothing over plain http on the LAN', () => {
    const h = host(false)
    expect(registerServiceWorker(h)).toBeNull()
    expect(h.navigator.serviceWorker.register).not.toHaveBeenCalled()
  })

  it('does nothing without service worker support', () => {
    expect(
      registerServiceWorker({ isSecureContext: true, navigator: {} }),
    ).toBeNull()
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
    expect(await registerServiceWorker(h)).toBeNull()
    expect(warn).toHaveBeenCalled()
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
