# pi-sens

A small system monitor for the Raspberry Pi, shipped as a single Go binary with the web dashboard built in.

## Build and run

You need Go 1.23 or newer, Node 22 with npm, and `make`.

```sh
make build          # frontend + host binary in bin/pisens
make build-arm64    # frontend + static linux/arm64 binary for the Pi in bin/pisens-linux-arm64
make web            # only build the frontend into web/dist (npm ci + npm run build)
make web-check      # svelte-check, tsc and prettier --check on the frontend
make test           # go vet + go test
make run            # build and run; pass flags with ARGS='--listen :9090'
```

The frontend in `web/` is Svelte 5 + Vite + TypeScript. `make build` builds it into `web/dist` and the Go binary embeds that directory, so the binary serves the dashboard at `/`. A plain `go build ./...` also works without Node: it embeds whatever is in `web/dist`, which is just a placeholder until the frontend has been built, and `/` then answers 503.

The version reported by the binary comes from `git describe`. Override it with `make build VERSION=1.0.0`.

Start the server and check that it is up:

```sh
./bin/pisens
curl localhost:8080/api/health
# {"status":"ok","version":"..."}
```

Open http://localhost:8080/ for the dashboard.

To run on the Pi, copy `bin/pisens-linux-arm64` over and start it. It has no runtime dependencies.

### Frontend development

Run the backend and the Vite dev server side by side. Vite proxies `/api` to the backend on `localhost:8080` and reloads the page as you edit.

```sh
make run                    # terminal 1: backend on :8080
cd web && npm ci            # terminal 2, first time only
npm run dev                 # dev server on http://localhost:5173
```

The shell lives in `web/src/App.svelte` (header, nav and a small History API router in `src/lib/router.svelte.ts`) with pages in `src/pages/`. Each Overview card is its own component in `src/widgets/`; see `web/src/widgets/README.md` before adding one. Theme colours are CSS variables in `src/styles/theme.css`.

Before committing frontend changes run `npm run check` (svelte-check, tsc and Prettier) in `web/`. `npm run format` fixes formatting.

### Configuration

Every setting can be set with a flag or an environment variable. Flags win over environment variables.

| Flag         | Environment variable | Default  | Description                     |
|--------------|----------------------|----------|---------------------------------|
| `--listen`   | `PISENS_LISTEN`      | `:8080`  | HTTP listen address             |
| `--interval` | `PISENS_INTERVAL`    | `2s`     | Sample interval (Go duration)   |
| `--data-dir` | `PISENS_DATA_DIR`    | `./data` | Directory for persistent data   |

Run `./bin/pisens --help` to see them. The server shuts down cleanly on SIGINT or SIGTERM.

### API

| Method | Path           | Response                              |
|--------|----------------|---------------------------------------|
| GET    | `/api/health`  | `{"status":"ok","version":"<version>"}` |
| GET    | `/api/metrics` | Latest metrics snapshot (503 until the first sample) |
| GET    | `/api/stream`  | Server-Sent Events: the latest snapshot on connect, then one per sample |

Any other `GET` path serves the web UI. Files in `web/dist` are served as they are, and paths without a file extension fall back to `index.html` so client-side routes like `/history` work as deep links. Hashed files under `/assets/` are cached for a year; everything else is sent with `Cache-Control: no-cache`. Unknown `/api/...` paths return a JSON 404.

A snapshot looks like this. Each collector has its own section; a collector that fails shows `{"error": "..."}` instead and the others are unaffected. Series keys are `area.thing_unit`.

```json
{
  "timestamp": "2026-11-07T09:21:22.08Z",
  "collectors": { "uptime": { "seconds": 334893.67, "boot_time": "2026-11-03T12:19:48Z" } },
  "series": { "system.uptime_s": 334893.67 }
}
```

`/api/stream` sends each snapshot as an `event: snapshot` message with the snapshot JSON as `data`, plus a `: heartbeat` comment every 15 seconds so proxies keep the connection open. Watch it with:

```sh
curl -N localhost:8080/api/stream
```

### Adding a collector

Collectors live in `internal/collectors`, one file each, and register themselves from `init()`. See `internal/collectors/doc.go` for the conventions and `uptime.go` for a reference implementation.
