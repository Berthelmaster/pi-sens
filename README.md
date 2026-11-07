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

| Flag            | Environment variable | Default  | Description                                               |
|-----------------|----------------------|----------|-----------------------------------------------------------|
| `--listen`      | `PISENS_LISTEN`      | `:8080`  | HTTP listen address                                       |
| `--interval`    | `PISENS_INTERVAL`    | `2s`     | Sample interval (Go duration)                             |
| `--data-dir`    | `PISENS_DATA_DIR`    | `./data` | Directory for persistent data                             |
| `--alerts-file` | `PISENS_ALERTS_FILE` | (none)   | YAML file of alert rules that replaces the built-in rules |

Run `./bin/pisens --help` to see them. The server shuts down cleanly on SIGINT or SIGTERM.

### API

| Method | Path           | Response                              |
|--------|----------------|---------------------------------------|
| GET    | `/api/health`  | `{"status":"ok","version":"<version>"}` |
| GET    | `/api/metrics` | Latest metrics snapshot (503 until the first sample) |
| GET    | `/api/stream`  | Server-Sent Events: the latest snapshot on connect, then one per sample, plus the alerts on connect and whenever they change |
| GET    | `/api/alerts`  | Firing alerts and the last 50 resolved ones |

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

`/api/stream` sends each snapshot as an `event: snapshot` message with the snapshot JSON as `data`, plus a `: heartbeat` comment every 15 seconds so proxies keep the connection open. It also sends an `event: alerts` message with the same JSON as `/api/alerts`, once on connect and then every time the set of firing or resolved alerts changes. Watch it with:

```sh
curl -N localhost:8080/api/stream
```

### Alerts

pisens checks alert rules against every sample. A rule compares one series, or every series matching a glob, with a threshold. When the comparison holds, the alert for that series is pending; once it has held for the rule's `for` duration it fires, and it resolves as soon as the comparison stops holding. A series that disappears for three samples in a row also ends its alert, so an unmounted disk does not stay firing forever, while a single failed collection does not end one.

Without a rules file these built-in rules apply:

| Name                | Rule                          | Severity |
|---------------------|-------------------------------|----------|
| `cpu-temp-high`     | `temp.cpu_c > 75` for 1m      | warning  |
| `cpu-temp-critical` | `temp.cpu_c > 82` for 30s     | critical |
| `undervoltage`      | `pi.undervoltage > 0`         | critical |
| `throttled`         | `pi.throttled > 0` for 30s    | warning  |
| `disk-full`         | `fs.*.used_pct > 90`          | warning  |
| `memory-high`       | `mem.used_pct > 90` for 2m    | warning  |
| `services-failed`   | `services.failed > 0`         | warning  |

To use your own rules, point `--alerts-file` (or `PISENS_ALERTS_FILE`) at a YAML file. Its rules replace the built-in ones completely, so copy any you want to keep:

```yaml
rules:
  - name: cpu-temp-high       # unique name
    series: temp.cpu_c        # series key from /api/metrics
    op: ">"                   # ">" or "<"; quote it, a bare > means something else in YAML
    threshold: 75
    for: 1m                   # optional Go duration, e.g. 30s, 2m, 1h30m; default 0 fires on the first sample
    severity: warning         # warning or critical
  - name: disk-full
    series: "fs.*.used_pct"   # a glob: one alert per matching series
    op: ">"
    threshold: 90
    severity: warning
  - name: core-voltage-low
    series: pi.core_v
    op: "<"
    threshold: 0.8
    for: 10s
    severity: critical
```

`series` is an exact key or a glob where `*` matches any run of characters, `?` one character and `[...]` a character class. Unknown fields, missing fields, bad values and duplicate names stop pisens at startup with an error naming the rule and line, so a typo cannot quietly turn a check off. Use `rules: []` to turn alerts off.

`GET /api/alerts` returns:

```json
{
  "version": 4,
  "firing": [
    {
      "rule": "cpu-temp-critical",
      "series": "temp.cpu_c",
      "severity": "critical",
      "op": ">",
      "threshold": 82,
      "for_s": 30,
      "state": "firing",
      "value": 83.4,
      "pending_at": "2026-11-07T09:20:12Z",
      "firing_at": "2026-11-07T09:20:42Z"
    }
  ],
  "resolved": []
}
```

`firing` lists critical alerts first, then the rest by when they fired. `resolved` holds the last 50 resolved alerts, newest first, each with a `resolved_at`. `value` is the series value at the alert's last state change, and `version` goes up every time the set changes. Pending alerts are not listed.

### Adding a collector

Collectors live in `internal/collectors`, one file each, and register themselves from `init()`. See `internal/collectors/doc.go` for the conventions, `cpufreq.go` for a small reference implementation and `cpu.go` for one that keeps state between samples.

## CI

GitHub Actions (`.github/workflows/ci.yml`) runs on every push and pull request to `main`:

- Frontend: `npm ci`, `npm run check` and `npm run build` in `web/`
- Go: `go vet`, `golangci-lint` and `go test -race ./...`
- A static `linux/arm64` build with the frontend embedded, uploaded as the `pisens-linux-arm64` artifact on the run page

To match the lint step locally, install the golangci-lint version pinned in the workflow and run `make lint`.
