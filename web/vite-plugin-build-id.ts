import { createHash } from 'node:crypto'
import type { Plugin } from 'vite'

/**
 * Name of the meta tag in the built index.html that holds the build ID. The
 * app reads it to know which build it is (src/lib/update.ts), and the Go
 * server reads it from the embedded index.html to report the build it serves
 * in /api/health (internal/server/build.go).
 */
export const BUILD_META = 'pisens-build'

/**
 * Stamps index.html with an ID for this build of the frontend: a hash of the
 * page and every file the bundle emits, names and contents. It only changes
 * when the frontend does, and the same sources always give the same ID, so a
 * page and the server it talks to disagree exactly when a different frontend
 * has been deployed since the page loaded. Files copied from public/ as they
 * are (icons, manifest) are left out: a change to them alone is no reason to
 * reload.
 *
 * The ID lives in the HTML rather than in the JavaScript, which could not
 * contain a hash of itself.
 */
export function buildId(): Plugin {
  return {
    name: 'pisens-build-id',
    apply: 'build',
    transformIndexHtml: {
      order: 'post',
      handler(html, ctx) {
        const hash = createHash('sha256')
        hash.update(html)
        const files = Object.values(ctx.bundle ?? {})
          .filter((f) => !f.fileName.endsWith('.html'))
          .sort((a, b) => (a.fileName < b.fileName ? -1 : 1))
        for (const file of files) {
          hash.update('\0' + file.fileName + '\0')
          hash.update(file.type === 'chunk' ? file.code : file.source)
        }
        return [
          {
            tag: 'meta',
            attrs: {
              name: BUILD_META,
              content: hash.digest('hex').slice(0, 16),
            },
            injectTo: 'head',
          },
        ]
      },
    },
  }
}
