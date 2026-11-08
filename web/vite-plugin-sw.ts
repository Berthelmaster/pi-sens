import { createHash } from 'node:crypto'
import { readFile, readdir, writeFile } from 'node:fs/promises'
import path from 'node:path'
import type { Plugin } from 'vite'

/** The global in the worker that the build replaces with the file list. */
const PLACEHOLDER = '__PISENS_PRECACHE__'
const SW_FILE = 'sw.js'

/**
 * Builds the service worker entry into sw.js at the root of the output and
 * writes the list of files to precache into it.
 *
 * The worker is a classic script, so it must come out as one file without
 * imports: it may only import modules the app does not, or the bundler would
 * move them into a shared chunk. The build fails if that happens.
 *
 * The file list is every file in the output once it is written, public files
 * included, apart from dotfiles and sw.js itself. Its version is a hash of
 * their names and contents, so sw.js changes, and the browser installs the
 * new worker, whenever any of them does.
 */
export function serviceWorker(entry: string): Plugin {
  let outDir = ''
  return {
    name: 'pisens-service-worker',
    apply: 'build',
    configResolved(config) {
      outDir = path.resolve(config.root, config.build.outDir)
    },
    buildStart() {
      this.emitFile({ type: 'chunk', id: entry, fileName: SW_FILE })
    },
    generateBundle(_, bundle) {
      const sw = bundle[SW_FILE]
      if (sw?.type !== 'chunk') {
        this.error(`${SW_FILE} is missing from the bundle`)
      }
      if (sw.imports.length > 0 || sw.dynamicImports.length > 0) {
        this.error(
          `${SW_FILE} must not import other chunks, but imports ` +
            [...sw.imports, ...sw.dynamicImports].join(', '),
        )
      }
    },
    writeBundle: {
      sequential: true,
      order: 'post',
      async handler() {
        const files = await shellFiles(outDir)
        const hash = createHash('sha256')
        for (const file of files) {
          hash.update(file + '\0')
          hash.update(await readFile(path.join(outDir, file)))
        }
        const manifest = {
          version: hash.digest('hex').slice(0, 16),
          urls: files.map((f) => '/' + f),
        }

        const swPath = path.join(outDir, SW_FILE)
        const code = await readFile(swPath, 'utf8')
        const parts = code.split(PLACEHOLDER)
        if (parts.length !== 2) {
          this.error(
            `expected ${PLACEHOLDER} once in ${SW_FILE}, found it ${parts.length - 1} times`,
          )
        }
        await writeFile(swPath, parts.join(JSON.stringify(manifest)))
      },
    },
  }
}

/** The files under dir to precache, with / separators, sorted. */
async function shellFiles(dir: string): Promise<string[]> {
  const entries = await readdir(dir, { recursive: true, withFileTypes: true })
  return entries
    .filter((e) => e.isFile())
    .map((e) =>
      path.relative(dir, path.join(e.parentPath, e.name)).split(path.sep),
    )
    .filter((parts) => !parts.some((p) => p.startsWith('.')))
    .map((parts) => parts.join('/'))
    .filter((f) => f !== SW_FILE)
    .sort()
}
