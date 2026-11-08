import { mount } from 'svelte'
import './styles/theme.css'
import './app.css'
import App from './App.svelte'
import { registerServiceWorker } from './lib/serviceWorker'
import { startUpdateWatcher } from './lib/updatePrompt.svelte'

const app = mount(App, {
  target: document.getElementById('app')!,
})

// Not under `npm run dev`: the dev server has no /sw.js, and a worker serving
// cached files would get in the way of hot reloading. After load, so caching
// the shell does not compete with the first paint. The update watcher offers a
// reload once a newer version is deployed, through the worker when there is
// one.
if (import.meta.env.PROD) {
  window.addEventListener('load', () => {
    void startUpdateWatcher(registerServiceWorker())
  })
}

export default app
