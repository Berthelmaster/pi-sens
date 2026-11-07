/// <reference types="vitest/config" />
import { svelte } from '@sveltejs/vite-plugin-svelte'
import { defineConfig, type Plugin } from 'vite'

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
  plugins: [svelte(), keepDist()],
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
    include: ['src/**/*.test.ts'],
    environment: 'node',
  },
})
