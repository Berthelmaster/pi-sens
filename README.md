# pi-sens

A small system monitor for the Raspberry Pi, shipped as a single Go binary with the web dashboard built in.

## Build and run

You need Go 1.23 or newer, Node 22 with npm, and `make`.

```sh
make build          # frontend + host binary in bin/pisens
make build-arm64    # frontend + static linux/arm64 binary for the Pi in bin/pisens-linux-arm64
make web            # only build the frontend into web/dist (npm ci + npm run build)
make web-check      # svelte-check, tsc, prettier --check and Vitest on the frontend
make test           # go vet + go test
make lint           # golangci-lint (v2, config in .golangci.yml)
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

Before committing frontend changes run `npm run check` (svelte-check, tsc, Prettier and the Vitest unit tests) in `web/`. `npm test` runs only the tests, `npm run format` fixes formatting.

### Configuration

Every setting can be set with a flag or an environment variable. Flags win over environment variables.

| Flag                      | Environment variable           | Default           | Description                            |
|---------------------------|--------------------------------|-------------------|----------------------------------------|
| `--listen`                | `PISENS_LISTEN`                | `:8080`           | HTTP listen address                    |
| `--interval`              | `PISENS_INTERVAL`              | `2s`              | Sample interval (Go duration)          |
| `--data-dir`              | `PISENS_DATA_DIR`              | `./data`          | Directory for persistent data          |
| `--history-interval`      | `PISENS_HISTORY_INTERVAL`      | `10s`             | How often samples are saved to history |
| `--history-raw-retention` | `PISENS_HISTORY_RAW_RETENTION` | `24h`             | How long raw history samples are kept  |
| `--history-1m-retention`  | `PISENS_HISTORY_1M_RETENTION`  | `168h` (7 days)   | How long 1 minute rollups are kept     |
| `--history-15m-retention` | `PISENS_HISTORY_15M_RETENTION` | `2160h` (90 days) | How long 15 minute rollups are kept    |

Run `./bin/pisens --help` to see them. The server shuts down cleanly on SIGINT or SIGTERM.

### History

Every snapshot's `series` values are kept in a SQLite database at `<data-dir>/pisens.db`, created on startup (package `internal/history`). The driver is `modernc.org/sqlite`, which is pure Go, so the binary still builds with `CGO_ENABLED=0`. To spare the SD card, snapshots are buffered in memory and written in one transaction per history interval, with WAL and `synchronous=NORMAL`. Whatever is still buffered is written on shutdown. New series keys are added to the database automatically, so collectors need no extra code. Schema changes go in as new entries at the end of `migrations` in `internal/history/store.go`.

Raw samples are downsampled into two rollup tables, `samples_1m` and `samples_15m`, with the average, minimum, maximum and sample count of every series per bucket. A background job runs every minute: it aggregates the buckets that have completed since its last run (raw into 1 minute, 1 minute into 15 minutes), then deletes rows past their retention, per series in batches of 500 so the writer never waits long for the lock. Its progress is stored in `rollup_state`, so a restart picks up where it left off and running it twice changes nothing. Samples that reach the database late (after a failed write was retried) get their buckets aggregated again. Rows the next level has not aggregated yet are never deleted, whatever the retention says. Durations are Go durations, so 7 days is `168h`.

#### Database size

The database stops growing once every table holds its full retention, because from then on the job deletes as many rows a minute as the writer adds and SQLite reuses the freed pages. With the defaults (2 second samples, 24h raw, 7 days of 1 minute and 90 days of 15 minute rollups) every series needs about:

| Table         | Rows per series | Size per series |
|---------------|-----------------|-----------------|
| `samples_raw` | 43,200          | ~1.2 MB         |
| `samples_1m`  | 10,080          | ~0.45 MB        |
| `samples_15m` | 8,640           | ~0.4 MB         |

The rollup sizes were measured with 50 series of random values. The raw figure is worked out from the same row layout (about 27 bytes a row against 45 for a rollup row). A Pi 5 has around 50 series (more with extra disks, mounts or network interfaces), which comes to roughly 100 MB, plus a WAL file of a few MB. Raw samples are the biggest part, so `--history-raw-retention` or a longer `--interval` is the knob to turn on a small SD card. Size scales linearly with each table's retention.

pisens does not run `PRAGMA incremental_vacuum`. With steady deletes and inserts the free pages are reused within minutes, so returning them to the filesystem would only make SQLite grow the file again, which costs SD card writes for nothing. It would also need `auto_vacuum=INCREMENTAL`, which an existing database only gets from a full `VACUUM`. If you lower a retention a lot and want the space back, stop pisens and run `sqlite3 data/pisens.db VACUUM`.

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
  "collectors": {
    "sysinfo": {
      "hostname": "pi5",
      "kernel": "6.12.47+rpt-rpi-2712",
      "arch": "aarch64",
      "os": "Debian GNU/Linux 13 (trixie)",
      "board": { "model": "Raspberry Pi 5 Model B Rev 1.0", "serial": "6c1f0a3e9b52d8e4", "revision": "d04170", "total_ram_bytes": 8454066176 },
      "boot_time": "2026-11-03T12:19:48Z",
      "uptime_s": 334893.67,
      "procs": 212,
      "threads": 575,
      "users": 1
    }
  },
  "series": { "system.uptime_s": 334893.67, "system.procs": 212 }
}
```

`/api/stream` sends each snapshot as an `event: snapshot` message with the snapshot JSON as `data`, plus a `: heartbeat` comment every 15 seconds so proxies keep the connection open. Watch it with:

```sh
curl -N localhost:8080/api/stream
```

### Adding a collector

Collectors live in `internal/collectors`, one file each, and register themselves from `init()`. See `internal/collectors/doc.go` for the conventions, `cpufreq.go` for a small reference implementation and `cpu.go` for one that keeps state between samples.

## CI

GitHub Actions (`.github/workflows/ci.yml`) runs on every push and pull request to `main`:

- Frontend: `npm ci`, `npm run check` and `npm run build` in `web/`
- Go: `go vet`, `golangci-lint` and `go test -race ./...`
- A static `linux/arm64` build with the frontend embedded, uploaded as the `pisens-linux-arm64` artifact on the run page

To match the lint step locally, install the golangci-lint version pinned in the workflow and run `make lint`.
