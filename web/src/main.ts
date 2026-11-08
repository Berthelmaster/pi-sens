import { mount } from 'svelte'
import './styles/theme.css'
import './app.css'
import App from './App.svelte'
import { registerServiceWorker } from './lib/serviceWorker'

const app = mount(App, {
  target: document.getElementById('app')!,
})

// Not under `npm run dev`: the dev server has no /sw.js, and a worker serving
// cached files would get in the way of hot reloading. After load, so caching
// the shell does not compete with the first paint.
if (import.meta.env.PROD) {
  window.addEventListener('load', () => registerServiceWorker())
}

export default app
