# pi-sens

A small system monitor for the Raspberry Pi, shipped as a single Go binary with the web dashboard built in.

Install the latest release on a Pi with 64-bit Raspberry Pi OS as a systemd service:

```sh
curl -fsSL https://raw.githubusercontent.com/Berthelmaster/pi-sens/main/deploy/install.sh | sudo bash -s -- --version latest
```

Then open http://raspberrypi.local:8080/. See [Install on a Raspberry Pi](#install-on-a-raspberry-pi) for what that does and other ways to install.

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

The version reported by the binary (`pisens --version`, `/api/health`) comes from `git describe`. Override it with `make build VERSION=1.0.0`.

Start the server and check that it is up:

```sh
./bin/pisens
curl localhost:8080/api/health
# {"status":"ok","version":"..."}
```

Open http://localhost:8080/ for the dashboard.

The binary has no runtime dependencies. To run it on the Pi as a service, see [Install on a Raspberry Pi](#install-on-a-raspberry-pi).

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

| Flag                      | Environment variable           | Default           | Description                                               |
|---------------------------|--------------------------------|-------------------|-----------------------------------------------------------|
| `--listen`                | `PISENS_LISTEN`                | `:8080`           | HTTP listen address                                       |
| `--interval`              | `PISENS_INTERVAL`              | `2s`              | Sample interval (Go duration)                             |
| `--data-dir`              | `PISENS_DATA_DIR`              | `./data`          | Directory for persistent data                             |
| `--history-interval`      | `PISENS_HISTORY_INTERVAL`      | `10s`             | How often samples are saved to history                    |
| `--history-raw-retention` | `PISENS_HISTORY_RAW_RETENTION` | `24h`             | How long raw history samples are kept                     |
| `--history-1m-retention`  | `PISENS_HISTORY_1M_RETENTION`  | `168h` (7 days)   | How long 1 minute rollups are kept                        |
| `--history-15m-retention` | `PISENS_HISTORY_15M_RETENTION` | `2160h` (90 days) | How long 15 minute rollups are kept                       |
| `--alerts-file`           | `PISENS_ALERTS_FILE`           | (none)            | YAML file of alert rules that replaces the built-in rules |

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
| GET    | `/api/stream`  | Server-Sent Events: the latest snapshot on connect, then one per sample, plus the alerts on connect and whenever they change |
| GET    | `/api/alerts`  | Firing alerts and the last 50 resolved ones |
| GET    | `/api/series`  | Stored series keys, `?prefix=cpu.` for only some |
| GET    | `/api/history` | History of up to 10 series, see [History API](#history-api) |

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

### History API

`GET /api/series` lists every series key in the history database as `{"series": ["cpu.usage_pct", ...]}`, sorted. `?prefix=cpu.` returns only the keys starting with `cpu.`.

`GET /api/history?series=cpu.usage_pct,temp.cpu_c&from=<unix>&to=<unix>&points=<n>` returns the values of up to 10 series between `from` (inclusive) and `to` (exclusive), both unix times in seconds:

```json
{
  "resolution": "1m",
  "step": 360,
  "series": {
    "cpu.usage_pct": { "t": [1762473600, 1762473960], "avg": [12.4, 9.8], "min": [3.1, 2.7], "max": [41.0, 30.2] },
    "temp.cpu_c": { "t": [1762473600, 1762473960], "avg": [51.2, 50.9], "min": [50.1, 49.8], "max": [53.4, 52.7] }
  }
}
```

- `series` is a comma separated list and may also be repeated. A key without data in the range comes back with empty arrays.
- `to` defaults to now, `points` to 500 (2 to 5000 allowed).
- The range picks the table: raw samples up to 6 hours, 1 minute rollups up to 7 days, 15 minute rollups beyond. Rollup rows are only written once their bucket is complete, so the newest few minutes of a longer range are missing until then.
- The rows are grouped into buckets of `step` seconds, a multiple of the table's resolution, so every series has at most `points` points. `t` is the start of each bucket. Buckets start at multiples of `step` since the epoch, so a chart that refreshes keeps the same buckets, and the first one can start a little before `from`.
- `avg` is the average over the bucket, weighted by sample count, and `min`/`max` are the extremes. For raw samples with a `step` of 1 they are all the sample value.
- Bad input (no `series`, more than 10, `from` missing or not before `to`, `points` out of range) returns 400 with `{"error": "..."}`.

On a Raspberry Pi 5 with the default retention for 50 series (a 91 MB database), a 30 day query for 10 series takes 60–110 ms, measured with curl against the running binary.

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

## Install on a Raspberry Pi

`deploy/install.sh` installs pisens as a systemd service that starts on boot and restarts if it crashes. It works on Raspberry Pi OS (64-bit) and other systemd-based arm64 distributions.

The quickest way is the one-liner at the top, which downloads the newest release. To pin a version, pass it instead of `latest`, for example `--version v0.1.0`. With `--version` the script downloads `pisens_<version>_linux_<arch>.tar.gz` and `SHA256SUMS` from the GitHub release, checks the checksum and installs the binary, unit and settings example from the archive. `latest` skips pre-releases. You can also download and unpack a release archive yourself and run `sudo deploy/install.sh` inside it, which picks up the `pisens` binary next to the `deploy` directory.

To install your own build instead, build the arm64 binary, copy it and the `deploy` directory to the Pi, and run the script there as root:

```sh
make build-arm64
ssh pi@raspberrypi.local mkdir -p pisens
scp -r deploy bin/pisens-linux-arm64 pi@raspberrypi.local:pisens/
ssh pi@raspberrypi.local sudo pisens/deploy/install.sh pisens/pisens-linux-arm64
```

If you build on the Pi itself, `sudo deploy/install.sh` without an argument picks up `bin/pisens-linux-arm64` or `bin/pisens` from the repository.

The script:

- creates a `pisens` system user (no login shell, no home) in the `video` group
- installs the binary as `/usr/local/bin/pisens` and the unit as `/etc/systemd/system/pisens.service`
- creates `/etc/pisens/pisens.env` from `deploy/pisens.env.example` the first time and never overwrites it after that; `/etc/pisens/pisens.env.example` is refreshed on every run so new settings show up
- runs `systemctl daemon-reload` and `systemctl enable --now pisens`, then checks the service stayed up

The dashboard is then on http://raspberrypi.local:8080/. Check on the service with:

```sh
systemctl status pisens
journalctl -u pisens -f
curl localhost:8080/api/health
```

**Settings.** Edit `/etc/pisens/pisens.env` (every variable is listed there, commented out with its default) and run `sudo systemctl restart pisens`. The data directory is always `/var/lib/pisens`, which systemd creates for the service. For changes to the unit itself use `sudo systemctl edit pisens`, so an upgrade does not undo them.

**Upgrade.** Run the script again with the new binary or `--version`. It replaces the binary and the unit, keeps your settings and data, and restarts the service. Running it twice in a row is safe.

**Pi health data.** The throttling, voltage, clock and power sections come from `vcgencmd`, which needs access to `/dev/vcio` through the `video` group. The unit adds that group, so it works out of the box on Raspberry Pi OS. If those sections show as unavailable, check that `vcgencmd` is installed (package `raspi-utils`, or `libraspberrypi-bin` on older releases) and that this prints `throttled=0x0` or similar:

```sh
sudo -u pisens vcgencmd get_throttled
```

**Hardening.** The service runs with `ProtectSystem=strict`, `ProtectHome=yes`, `PrivateTmp=yes`, `NoNewPrivileges=yes` and no capabilities. It can read `/proc`, `/sys` and `/dev/vcio` but write only to `/var/lib/pisens`. Because of `ProtectHome=yes`, a separate `/home` partition does not show correct usage on the Storage widget.

**Uninstall.** `sudo deploy/install.sh --uninstall` stops and disables the service and removes the unit and binary. It keeps `/etc/pisens`, `/var/lib/pisens` and the `pisens` user; add `--purge` to remove those as well.

## CI

GitHub Actions (`.github/workflows/ci.yml`) runs on every push and pull request to `main`:

- Frontend: `npm ci`, `npm run check` and `npm run build` in `web/`
- Go: `go vet`, `golangci-lint` and `go test -race ./...`
- A static `linux/arm64` build with the frontend embedded, uploaded as the `pisens-linux-arm64` artifact on the run page

To match the lint step locally, install the golangci-lint version pinned in the workflow and run `make lint`.

## Releases

Pushing a tag that starts with `v` runs `.github/workflows/release.yml`, which builds the frontend, runs the Go tests and builds static `linux/arm64` and `linux/amd64` binaries that report the tag as their version. Each is packed as `pisens_<version>_linux_<arch>.tar.gz` (the tag without its leading `v`) together with `deploy/`, `README.md` and `LICENSE`. The workflow then creates a GitHub release with generated notes and attaches the archives and a `SHA256SUMS` file. A tag with a suffix, such as `v0.2.0-rc1`, becomes a pre-release.

```sh
git tag -a v0.1.0 -m v0.1.0
git push origin v0.1.0
```

To try the workflow without publishing anything, run it by hand from the Actions tab (Run workflow). That builds the same archives and checksums, named after `git describe`, and uploads them as the `release-dry-run` artifact instead of creating a release.
