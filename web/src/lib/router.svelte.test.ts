import { render, screen } from '@testing-library/svelte'
import { tick } from 'svelte'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import History from '../pages/History.svelte'
import { followLink, navigate, router } from './router.svelte'

// History asks the API for the stored series; no keys keeps the page free of
// charts and further requests.
vi.mock('../api', () => ({
  getSeriesKeys: vi.fn(async () => []),
  getHistory: vi.fn(),
}))

// uPlot, loaded with History's charts, reads matchMedia on import and jsdom
// has none.
vi.hoisted(() => {
  window.matchMedia ??= (query: string) =>
    ({
      matches: false,
      media: query,
      addEventListener() {},
      removeEventListener() {},
    }) as unknown as MediaQueryList
})

/** An in-app link like the ones in the nav, with followLink attached. */
function link(href: string): HTMLAnchorElement {
  const a = document.createElement('a')
  a.href = href
  a.addEventListener('click', followLink)
  document.body.append(a)
  return a
}

/**
 * Clicks a link and returns whether the browser was left to follow it. The
 * browser's own navigation is cancelled afterwards, which jsdom can't do.
 */
function click(a: HTMLAnchorElement, init: MouseEventInit = {}): boolean {
  let followed = false
  window.addEventListener(
    'click',
    (e) => {
      followed = !e.defaultPrevented
      e.preventDefault()
    },
    { once: true },
  )
  a.dispatchEvent(
    new MouseEvent('click', { bubbles: true, cancelable: true, ...init }),
  )
  return followed
}

beforeEach(() => {
  window.scrollTo = vi.fn()
  navigate('/')
})

afterEach(() => {
  document.body.innerHTML = ''
})

describe('followLink', () => {
  it('keeps the query string and History reads it', async () => {
    const pushState = vi.spyOn(history, 'pushState')
    const followed = click(link('/history?range=7d&series=temp.cpu_c'))

    expect(followed).toBe(false)
    expect(pushState).toHaveBeenLastCalledWith(
      null,
      '',
      '/history?range=7d&series=temp.cpu_c',
    )
    expect(location.pathname).toBe('/history')
    expect(location.search).toBe('?range=7d&series=temp.cpu_c')
    expect(router.path).toBe('/history')

    render(History)
    const pressed = screen
      .getAllByRole('button', { pressed: true })
      .map((b) => b.textContent?.trim())
    expect(pressed).toEqual(['7d'])
    // History writes its state back to the URL; the series survived the read.
    await tick()
    expect(location.search).toBe('?range=7d&series=temp.cpu_c')
    pushState.mockRestore()
  })

  it('follows nav links and sets the current route', () => {
    const before = router.visit
    expect(click(link('/history'))).toBe(false)
    expect(router.path).toBe('/history')
    expect(location.search).toBe('')

    expect(click(link('/'))).toBe(false)
    expect(router.path).toBe('/')
    expect(router.visit).toBe(before + 2)
  })

  it('treats a new query on the same path as a new visit', () => {
    click(link('/history?range=24h'))
    const before = router.visit

    click(link('/history?range=24h'))
    expect(router.visit).toBe(before)

    click(link('/history/?range=7d'))
    expect(router.path).toBe('/history')
    expect(location.search).toBe('?range=7d')
    expect(router.visit).toBe(before + 1)
  })

  it('leaves modified clicks and other origins to the browser', () => {
    expect(click(link('/history?range=7d'), { ctrlKey: true })).toBe(true)
    expect(click(link('https://example.com/history'))).toBe(true)
    expect(router.path).toBe('/')
  })
})
