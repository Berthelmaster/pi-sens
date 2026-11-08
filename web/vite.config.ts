/// <reference types="vitest/config" />
import { svelte } from '@sveltejs/vite-plugin-svelte'
import { svelteTesting } from '@testing-library/svelte/vite'
import { defineConfig, type Plugin } from 'vite'
import { buildId } from './vite-plugin-build-id.ts'
import { serviceWorker } from './vite-plugin-sw.ts'

// The Go binary embeds web/dist, so the directory has to exist before the
// frontend is built. Vite empties outDir on every build; put the placeholder
// back so the committed web/dist/.gitkeep never shows up as deleted.
function keepDist(): Plugin {
  return {
    name: 'pisens-keep-dist',
    apply: 'build',
    generateBundle() {
      this.emitFile({ type: 'asset', fileName: '.gitkeep', source: '' })
    },
  }
}

// https://vite.dev/config/
export default defineConfig({
  plugins: [svelte(), keepDist(), buildId(), serviceWorker('src/sw/sw.ts')],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  server: {
    // Run the Go backend on :8080 (make run) and the API calls go through.
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
  test: {
    // Plain *.test.ts files test logic and run in node. Component tests are
    // named *.svelte.test.ts and run in jsdom with Svelte's browser build, so
    // components can be mounted with @testing-library/svelte.
    projects: [
      {
        extends: true,
        test: {
          name: 'node',
          include: ['src/**/*.test.ts'],
          exclude: ['src/**/*.svelte.test.ts'],
          environment: 'node',
        },
      },
      {
        extends: true,
        plugins: [svelteTesting()],
        test: {
          name: 'dom',
          include: ['src/**/*.svelte.test.ts'],
          environment: 'jsdom',
        },
      },
    ],
  },
})
