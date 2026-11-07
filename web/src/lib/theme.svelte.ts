export type Theme = 'light' | 'dark'

// Keep in sync with the inline script in index.html, which applies the saved
// choice before the first paint so a light-mode user never sees a dark flash.
const STORAGE_KEY = 'pisens-theme'

// Outside a browser (vitest's node environment) there is no window or
// localStorage. The theme then starts dark and simply follows no OS preference.
const hasWindow = typeof window !== 'undefined'

const lightQuery = hasWindow
  ? window.matchMedia('(prefers-color-scheme: light)')
  : null

function readStored(): Theme | null {
  if (!hasWindow) return null
  try {
    const value = localStorage.getItem(STORAGE_KEY)
    return value === 'light' || value === 'dark' ? value : null
  } catch {
    // Storage can be unavailable (private mode, blocked cookies).
    return null
  }
}

let chosen = $state<Theme | null>(readStored())
let systemLight = $state(lightQuery?.matches ?? false)

lightQuery?.addEventListener('change', (e) => {
  systemLight = e.matches
})

/**
 * The active colour theme. Without a manual choice it follows the OS
 * preference and falls back to dark. Toggling stores an explicit choice in
 * localStorage and sets data-theme on <html>, which theme.css picks up.
 */
export const theme = {
  get current(): Theme {
    return chosen ?? (systemLight ? 'light' : 'dark')
  },

  toggle() {
    const next: Theme = this.current === 'dark' ? 'light' : 'dark'
    chosen = next
    if (!hasWindow) return
    document.documentElement.dataset.theme = next
    try {
      localStorage.setItem(STORAGE_KEY, next)
    } catch {
      // Still works for this page load, it just will not be remembered.
    }
  },
}
