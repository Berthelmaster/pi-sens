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
make lint           # golangci-lint (v2, config in .golangci.yml), including gofmt
make fmt            # format the Go code with gofmt through golangci-lint
make shellcheck     # shellcheck on the deploy scripts and their tests
make test-deploy    # test deploy/deploy.sh with stub systemctl, useradd, sudo and curl
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
| `--notify-webhook`        | `PISENS_NOTIFY_WEBHOOK`        | (none)            | URL that alert notifications are POSTed to as JSON        |
| `--notify-ntfy`           | `PISENS_NOTIFY_NTFY`           | (none)            | ntfy topic URL for alert notifications                    |
| `--notify-cooldown`       | `PISENS_NOTIFY_COOLDOWN`       | `15m`             | Per-alert notification cooldown, `0` for none             |
| `--watch-services`        | `PISENS_WATCH_SERVICES`        | (none)            | Comma separated systemd services to report the state of, e.g. `ssh,pisens` |
| `--process-cmdline`       | `PISENS_PROCESS_CMDLINE`       | `redacted`        | How much of the top processes' command lines to show: `off`, `redacted` or `full`, see [Process command lines](#process-command-lines) |
| `--net-skip`              | `PISENS_NET_SKIP`              | `lo,veth*,docker*,br-*` | Comma separated network interfaces to leave out, as names or prefixes ending in `*`, e.g. add `cni*,virbr*,tailscale0`; `--net-skip=` reports every interface |
| `--energy-price`          | `PISENS_ENERGY_PRICE`          | (none)            | Electricity price per kWh; adds the cost to the [energy](#energy) section |
| `--energy-currency`       | `PISENS_ENERGY_CURRENCY`       | (none)            | Currency shown with the energy cost, e.g. `DKK` |
| `--netq`                  | `PISENS_NETQ`                  | `on`              | Measure [network quality](#network-quality) by pinging the gateway and internet hosts; `off` turns it off |
| `--netq-targets`          | `PISENS_NETQ_TARGETS`          | `1.1.1.1,8.8.8.8` | Comma separated internet hosts to ping besides the default gateway, IPv4 addresses or host names; `--netq-targets=` pings only the gateway |
| `--netq-interval`         | `PISENS_NETQ_INTERVAL`         | `10s`             | Time between two rounds of network quality pings |
| `--netq-dns-name`         | `PISENS_NETQ_DNS_NAME`         | `example.com`     | Host name to look up every network quality round to time the [DNS resolver](#dns); `--netq-dns-name=` skips the lookups |
| `--prometheus`            | `PISENS_PROMETHEUS`            | `true`            | Serve [Prometheus metrics](#prometheus) at `/metrics`; `--prometheus=false` turns it off |
| `--auth-user`             | `PISENS_AUTH_USER`             | (none)            | User name for HTTP basic auth, see [Authentication](#authentication) |
| `--auth-password-hash`    | `PISENS_AUTH_PASSWORD_HASH`    | (none)            | bcrypt hash of the basic auth password, from `pisens hash-password` |

Run `./bin/pisens --help` to see them. The server shuts down cleanly on SIGINT or SIGTERM.

#### Process command lines

The `processes` section lists the full command line of each top process, and command lines often carry secrets such as database passwords or API tokens. Since the API is open to anyone on the network unless [authentication](#authentication) is on, `--process-cmdline` decides how much of them is shown:

- `redacted` (the default) replaces the values of arguments that commonly hold secrets with `***`: the argument after `--password`, `--token`, `--api-key` or `--secret` (also with one dash, `_` instead of `-` or a prefix such as `--db-password`), `-p<password>` as mysql takes it, any `key=value` whose key contains `pass`, `token`, `secret` or `key` (`--password=<value>`, `PGPASSWORD=<value>`, `?apikey=<value>`), and the password in a URL (`postgres://user:***@db/shop`). It catches the common forms, not every secret: a password passed as a bare argument is still shown.
- `off` shows only the process name; the command line is not read at all.
- `full` shows the command line unchanged.

### Energy

On a Pi 5 the `energy` section adds up how much energy the Pi uses, from the PMIC total power that the `power` section reports (`pi.power_w`). Every reading is integrated over the real time since the previous one (the trapezoid rule). If more than 2 minutes (or three sample intervals, if that is longer) pass between two readings, because pisens was stopped or the PMIC did not answer, that stretch adds nothing rather than a guess. Days and months start at local midnight.

```json
"energy": {
  "available": true, "power_w": 3.21,
  "today_kwh": 0.0412, "yesterday_kwh": 0.0771, "month_kwh": 0.5208, "total_kwh": 3.1904,
  "since": "2026-09-14T08:02:11Z",
  "today_cost": 0.1009, "month_cost": 1.276, "currency": "DKK"
}
```

`since` is the first reading ever. The cost fields only appear when `--energy-price` (or `PISENS_ENERGY_PRICE`) is set, as the price per kWh; `--energy-currency` is shown next to it. The series `energy.today_kwh` and `energy.month_kwh` go to the history like any other. The totals are kept in `<data-dir>/energy.json`, written at most once a minute and on shutdown, so they survive restarts and deploys; a restart within 2 minutes even counts the time pisens was down. On a Pi without PMIC readings (a Pi 4 or older, or not a Pi) the section is `{"available": false}`.

### Power supply

Under-voltage is the most common cause of a flaky Pi, so the `throttle` section also says how often it happened and whether the supply is up to the job:

```json
"throttle": {
  "available": true, "raw": "0x50000", "flags": { ... },
  "psu_max_current_ma": 3000, "psu_ok": false, "usb_max_current_enable": false,
  "undervoltage_events": 3, "last_undervoltage": "2026-11-07T14:02:00Z"
}
```

`psu_max_current_ma` is the current the supply offers, which a Pi 5 negotiates over USB-PD and the firmware puts in the device tree (`/proc/device-tree/chosen/power/max_current`). `psu_ok` is true at 5000 mA, what the official 27 W supply offers. With less, the firmware limits the USB ports to 600 mA in total unless `usb_max_current_enable=1` is set in `config.txt`, which `usb_max_current_enable` reports. Boards before the Pi 5 leave these fields out.

`undervoltage_events` counts how often the "under-voltage now" flag went from clear to set since pisens started (a flag that is already set at startup counts as one), and `last_undervoltage` is when the latest one began. The series `power.undervoltage_events` holds the count and `power.undervoltage_events_5m` the events of the last 5 minutes, which the built-in `undervoltage-recent` alert watches. pisens checks the flag once per sample, so a dip that starts and ends between two samples is not counted; the "since boot" flag still shows it.

The PMIC sees what it supplies to the board (SoC, RAM, wireless, HDMI and so on), not USB devices or its own losses, so the totals are an estimate that stays below what a meter at the wall would show.

### RTC battery

The Pi 5 has a real-time clock with an optional backup battery. When the battery runs flat the clock is wrong after a power cut until NTP syncs, and so are the history timestamps of that stretch. The `rtc` section reports it:

```json
"rtc": {
  "present": true, "rtc": "rtc0", "name": "rpi-rtc soc@107c000000:rpi_rtc",
  "battery": true, "battery_v": 3.021, "charging": true, "charging_voltage_v": 3
}
```

The readings come from `battery_voltage` and `charging_voltage` of the first `/sys/class/rtc/rtc<N>` that has a `battery_voltage`. `charging` is true while trickle charging is on (`dtparam=rtc_bbat_vchg=3000000` in `config.txt`, only for a rechargeable cell) and `charging_voltage_v` is what it charges to. The series `power.rtc_battery_v` goes to the history and drives the built-in `rtc-battery-low` alert. The Pi health card shows the voltage, in amber below 2.7 V. Without a battery the RTC reads 0 V and the section is `{"present": true, "rtc": "rtc0", "battery": false}`; without an RTC that reports its battery (anything before the Pi 5, or not a Pi) it is `{"present": false, "battery": false}`. Neither has a series.

### Network quality

The `netquality` section shows when the network or the internet was flaky. Every 10 seconds (`--netq-interval`) a background goroutine sends a burst of 5 pings, 200 ms apart, to the default gateway (the lowest metric default route in `/proc/net/route`, read again every round) and to each of `--netq-targets`, all targets at once. A ping that gets no answer within a second is lost. The sampler only reads the latest round, so a slow network never holds up a sample.

```json
"netquality": {
  "available": true, "status": "good", "interval_s": 10, "probed_at": "2026-11-07T20:22:54Z",
  "targets": [
    {"name": "gateway", "key": "gateway", "kind": "gateway", "address": "192.168.1.1", "method": "icmp",
     "latency_ms": 0.63, "jitter_ms": 0.12, "loss_pct": 0, "last_ok": "2026-11-07T20:22:54Z"},
    {"name": "1.1.1.1", "key": "1_1_1_1", "kind": "internet", "address": "1.1.1.1", "method": "icmp",
     "latency_ms": 19.31, "jitter_ms": 3.69, "loss_pct": 0, "last_ok": "2026-11-07T20:22:54Z"}
  ],
  "dns": {"name": "example.com", "resolver": "192.168.1.1:53", "ok": true, "lookup_ms": 14.82,
          "failures": 0, "last_ok": "2026-11-07T20:22:54Z"}
}
```

- `latency_ms` is the average round trip time of the answered pings and `jitter_ms` the average difference between two consecutive ones; both are `null` when too few came back. `loss_pct` is the share of the 5 pings that got no answer.
- `last_ok` is the last round in which the target answered at all. `error` says why nothing came back when that is known, such as `no default route` or a host name that did not resolve.
- `status` is `down` when every internet target lost every ping, `degraded` when any target lost a ping or averaged over 150 ms or the DNS lookup failed, and `good` otherwise. It is `pending` until the first round is done.
- The series are `netq.<key>.latency_ms` (left out when nothing answered) and `netq.<key>.loss_pct`, so `netq.gateway.loss_pct` and `netq.1_1_1_1.latency_ms` go to the history and to Prometheus like any other.
- Two more series sum up the internet targets for the [alerts](#alerts): `netq.internet_loss_pct` is the lowest loss of any internet target, so it is 100 only when every one of them lost every ping, and `netq.internet_latency_ms` is the average latency of the internet targets that answered (left out when none did). Both are left out when `--netq-targets` is empty.

The pings are unprivileged ICMP echo requests, which Linux allows for the groups in the `net.ipv4.ping_group_range` sysctl. systemd opens that range to every group, so the service needs no extra capabilities. Where the kernel refuses them, pisens logs it once and times a TCP connect instead: port 443 for internet targets, and 53 then 80 for the gateway. A refused connection still took one round trip, so it counts as an answer. `method` says which one a target was measured with (`icmp` or `tcp`). Only IPv4 is probed. `--netq=off` (or `PISENS_NETQ=off`) stops the pings, and the section is then `{"available": false}`.

#### DNS

Many "the internet is slow" moments are really DNS, so every round also looks up `--netq-dns-name` (default `example.com`), at the same time as the pings. pisens reads the first `nameserver` from `/etc/resolv.conf` every round (127.0.0.1 when there is none, as glibc does), sends it one A query over UDP port 53 and times the answer. The name is looked up as written, without the `search` domains. Nothing is cached on the pisens side, so every round sends a real query; the resolver may answer from its own cache, as it would for any other program on the Pi. When `resolv.conf` points at a local caching resolver (systemd-resolved's `127.0.0.53`, dnsmasq), `lookup_ms` is mostly that cache.

- `ok` is whether the name resolved to an IPv4 address. A lookup fails when no answer comes within 2 seconds, when the answer is an error (`NXDOMAIN`, `SERVFAIL`, `REFUSED` and so on, shown in `error`), or when it holds no IPv4 address.
- `lookup_ms` is the time from the query to the answer, `null` when the lookup failed. `resolver` is the nameserver that was asked, `last_ok` the last round the lookup worked and `failures` the failed lookups since pisens started.
- A failed lookup makes `status` `degraded` even when every ping comes back.
- The series are `netq.dns.lookup_ms` (left out when the lookup failed), `netq.dns.failures` and `netq.dns.failing`, which is 1 when the latest lookup failed and 0 when it worked. No target gets the key `dns`; a target with that name becomes `dns_2`.

### History

Every snapshot's `series` values are kept in a SQLite database at `<data-dir>/pisens.db`, created on startup (package `internal/history`). The driver is `modernc.org/sqlite`, which is pure Go, so the binary still builds with `CGO_ENABLED=0`. To spare the SD card, snapshots are buffered in memory and written in one transaction per history interval, with WAL and `synchronous=NORMAL`. Whatever is still buffered is written on shutdown. New series keys are added to the database automatically, so collectors need no extra code. Schema changes go in as new entries at the end of `migrations` in `internal/history/store.go`.

Raw samples are downsampled into two rollup tables, `samples_1m` and `samples_15m`, with the average, minimum, maximum and sample count of every series per bucket. A background job runs every minute: it aggregates the buckets that have completed since its last run (raw into 1 minute, 1 minute into 15 minutes), then deletes rows past their retention, per series in batches of 500 so the writer never waits long for the lock. Its progress is stored in `rollup_state`, so a restart picks up where it left off and running it twice changes nothing. Samples that reach the database late (after a failed write was retried) get their buckets aggregated again. Rows the next level has not aggregated yet are never deleted, whatever the retention says. Durations are Go durations, so 7 days is `168h`.

#### Database size

The database stops growing once every table holds its full retention, because from then on the job deletes as many rows a minute as the writer adds and SQLite reuses the freed pages. With the defaults (2 second samples, 24h raw, 7 days of 1 minute and 90 days of 15 minute rollups) every series needs about:

| Table         | Rows per series | Size per series |
|---------------|-----------------|-----------------|
| `samples_raw` | 43,200          | ~1.0 MB         |
| `samples_1m`  | 10,080          | ~0.45 MB        |
| `samples_15m` | 8,640           | ~0.4 MB         |

All three were measured with 50 series of random values, about 22 bytes a raw row against 45 for a rollup row. Series whose values are whole numbers take a little less, because SQLite stores those as integers. `PISENS_MEASURE_DB=1 go test -run TestMeasureRawSize -v ./internal/history` reruns the raw measurement: it writes 24 hours of samples the way pisens does, keeps the downsampler running for a few more hours and reads the table size from `dbstat`. A Pi 5 has around 50 series (more with extra disks, mounts or network interfaces), which comes to roughly 90 MB, plus a WAL file of a few MB. Raw samples are the biggest part, so `--history-raw-retention` or a longer `--interval` is the knob to turn on a small SD card. Size scales linearly with each table's retention.

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
| GET    | `/api/energy/daily` | Energy used per day, `?days=30` by default, see [History API](#history-api) |
| GET    | `/metrics`     | Latest series in the Prometheus text format, see [Prometheus](#prometheus) (404 with `--prometheus=false`) |

Any other `GET` path serves the web UI. Files in `web/dist` are served as they are, and paths without a file extension fall back to `index.html` so client-side routes like `/history` work as deep links. Hashed files under `/assets/` are cached for a year; everything else is sent with `Cache-Control: no-cache`. Every file carries a strong `ETag` (a hash of its contents), so browsers revalidate `index.html` with a cheap `304 Not Modified` until a new build changes it. Unknown `/api/...` paths return a JSON 404.

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
      "board": { "model": "Raspberry Pi 5 Model B Rev 1.0", "serial": "6c1f0a3e9b52d8e4", "revision": "d04170", "total_ram_bytes": 8454066176,
                 "type": "5", "processor": "BCM2712", "manufacturer": "Sony UK", "fitted_ram_bytes": 8589934592, "pcb_revision": "1.0" },
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
- The range picks the table: raw samples up to 6 hours, 1 minute rollups up to 7 days, 15 minute rollups beyond. If `from` is older than that table's retention (`PISENS_HISTORY_RAW_RETENTION`, `PISENS_HISTORY_1M_RETENTION`), the next coarser table is read instead, so a short range in the past still has data. Rollup rows are only written once their bucket is complete, so the newest few minutes of a longer range are missing until then.
- The rows are grouped into buckets of `step` seconds, a multiple of the table's resolution, so every series has at most `points` points. `t` is the start of each bucket. Buckets start at multiples of `step` since the epoch, so a chart that refreshes keeps the same buckets, and the first one can start a little before `from`.
- `avg` is the average over the bucket, weighted by sample count, and `min`/`max` are the extremes. For raw samples with a `step` of 1 they are all the sample value.
- Bad input (no `series`, more than 10, `from` missing or not before `to`, `points` out of range) returns 400 with `{"error": "..."}`.

`GET /api/energy/daily?days=30` returns the energy the Pi used on each of the last `days` local calendar days (1 to 366, default 30), today included and counting:

```json
{
  "days": [
    { "date": "2026-11-06", "start": 1762383600, "kwh": 0.1187, "cost": 0.2908 },
    { "date": "2026-11-07", "start": 1762470000, "kwh": 0.0412, "cost": 0.1009 }
  ],
  "price": 2.45,
  "currency": "DKK"
}
```

- A day's `kwh` is the highest `energy.today_kwh` stored for it, which is where the counter stood when the day ended. `start` is the day's local midnight in unix seconds.
- Every day in the range is listed, oldest first. A day with nothing stored (pisens was off, or the Pi has no PMIC readings) has `"kwh": null` and no cost.
- `cost`, `price` and `currency` only appear when `--energy-price` is set.
- It reads the 15 minute rollups where they are done and the finer tables for the newest minutes, so today's value is current. Days older than `PISENS_HISTORY_15M_RETENTION` (90 days by default) are null.

On a Raspberry Pi 5 with the default retention for 50 series (a 91 MB database), a 30 day query for 10 series takes 60–110 ms, measured with curl against the running binary.

The **History** page in the dashboard charts this API. Pick 1h, 6h, 24h, 7d, 30d or 90d, or a custom from/to; ranges up to 24h refresh every 30 seconds. The default charts (CPU, temperature and fan, memory, network, disk I/O, Pi power and voltage) only show the series this Pi has, and where a point stands for several samples a shaded band shows their min and max. Below them, **Energy per day** shows the last 30 days as bars from `/api/energy/daily`, whatever range is picked; hovering a day shows its kWh and, with a price set, its cost, and today's bar is lighter while it still counts. The custom chart plots any stored series together, up to 8. The range and the custom series are kept in the URL, e.g. `/history?range=7d&series=cpu.usage_pct,temp.cpu_c` or `/history?from=1762473600&to=1762560000`, so a link shows the same charts.

### Alerts

pisens checks alert rules against every sample. A rule compares one series, or every series matching a glob, with a threshold. When the comparison holds, the alert for that series is pending; once it has held for the rule's `for` duration it fires, and it resolves as soon as the comparison stops holding. A series that disappears for three samples in a row also ends its alert, so an unmounted disk does not stay firing forever, while a single failed collection does not end one.

Without a rules file these built-in rules apply:

| Name                  | Rule                                    | Severity |
|-----------------------|-----------------------------------------|----------|
| `cpu-temp-high`       | `temp.cpu_c > 75` for 1m                | warning  |
| `cpu-temp-critical`   | `temp.cpu_c > 82` for 30s               | critical |
| `undervoltage`        | `pi.undervoltage > 0`                   | critical |
| `undervoltage-recent` | `power.undervoltage_events_5m > 0`      | warning  |
| `rtc-battery-low`     | `power.rtc_battery_v < 2.6` for 10m     | warning  |
| `throttled`           | `pi.throttled > 0` for 30s              | warning  |
| `disk-full`           | `fs.*.used_pct > 90`                    | warning  |
| `memory-high`         | `mem.used_pct > 90` for 2m              | warning  |
| `services-failed`     | `services.failed > 0` for 1m            | warning  |
| `internet-down`       | `netq.internet_loss_pct > 99` for 1m    | critical |
| `internet-slow`       | `netq.internet_latency_ms > 150` for 5m | warning  |
| `dns-failing`         | `netq.dns.failing > 0` for 2m           | warning  |

`undervoltage-recent` fires for 5 minutes after every under-voltage event (see [Power supply](#power-supply)), so a dip that is over before the next sample still shows up.

`rtc-battery-low` fires when the RTC backup battery (see [RTC battery](#rtc-battery)) has been below 2.6 V for 10 minutes. Without a battery fitted there is no `power.rtc_battery_v` series, so it never fires.

`services-failed` fires when a systemd service has been in the failed state for a minute; the **Services** section shows which one. On a machine without systemctl there is no `services.failed` series, so it never fires.

`internet-down`, `internet-slow` and `dns-failing` watch the [network quality](#network-quality) probes. `internet-down` fires when every internet target has lost every ping (100 % loss; the rules only know `>` and `<`, hence `> 99`) for a minute, `internet-slow` when the internet targets that answer have averaged over 150 ms for 5 minutes, and `dns-failing` when every DNS lookup has failed for 2 minutes. While the internet is down nothing answers, so `internet-slow` stays quiet and `internet-down` speaks for both; `dns-failing` usually fires too, unless the resolver answers from its cache. With `--netq=off`, before the first probe round, or without internet targets (`--netq-targets=`) the series are not there and the internet rules never fire; `--netq-dns-name=` does the same for `dns-failing`.

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

The dashboard shows firing alerts in a banner under the header, critical ones in red and warnings in amber, and updates it live from the stream. The **Alerts** entry in the nav carries a count of the firing alerts and opens the ten most recent resolved ones. While a critical alert fires, the browser tab title starts with `(!)`. To try it, start pisens with a rules file whose threshold the Pi already crosses, for example `system.procs > 1`.

### Notifications

pisens can push alerts to a webhook, to [ntfy](https://ntfy.sh) or to both when they fire and when they resolve. Pending alerts are not sent. Set one or both of:

- `--notify-webhook` (or `PISENS_NOTIFY_WEBHOOK`): every notification is POSTed to this URL as JSON:

  ```json
  {"alert":"cpu-temp-high","series":"temp.cpu_c","severity":"warning","op":">","value":78.5,"threshold":75,"state":"firing","hostname":"pi5","time":"2026-11-07T12:01:00Z"}
  ```

  `alert` is the rule name, `state` is `firing` or `resolved`, `value` is the series value when the alert fired or resolved and `time` is when that happened.
- `--notify-ntfy` (or `PISENS_NOTIFY_NTFY`): a topic URL such as `https://ntfy.sh/my-pi-alerts`. The title reads `pi5: cpu-temp-high firing`, critical alerts are sent with urgent priority, warnings with high priority and resolves with the default one. For a protected topic put the credentials in the URL: `https://user:pass@ntfy.example.com/topic`.

To keep a flapping alert from flooding your phone, each alert sends at most one firing notification per `--notify-cooldown` (default `15m`, `0` turns it off). A firing held back by the cooldown is sent when the cooldown ends if the alert is still firing, and a resolve is only sent for an alert whose firing was sent. Failed deliveries are retried up to five times with growing pauses; client errors such as `404` are not retried. Notifications are sent in the background, so a slow or unreachable endpoint never holds up the alerts or the dashboard.

An `internet-down` notification usually cannot get out, since a webhook or ntfy on the internet is just as unreachable as the ping targets. The retries above give up after about half a minute and the firing is dropped; there is no queue that waits for the connection to come back. What does arrive is the `resolved` notification, sent once the internet is back, so you learn about the outage afterwards. The dashboard shows the alert either way. A webhook on the local network gets the firing as usual.

At startup pisens logs which notifiers are enabled, with only the scheme and host of each URL, since webhook URLs and ntfy topics often work as passwords. Try it with:

```sh
./bin/pisens --notify-ntfy https://ntfy.sh/some-long-random-topic
```

### Prometheus

`GET /metrics` serves the latest snapshot's `series` in the Prometheus text format, so an existing Prometheus and Grafana can scrape the Pi. It is on by default; turn it off with `--prometheus=false` or `PISENS_PROMETHEUS=false`. A scrape config:

```yaml
scrape_configs:
  - job_name: pisens
    static_configs:
      - targets: ["raspberrypi.local:8080"]
```

Every metric is a gauge named `pisens_...` with a `HELP` line. Keys that name a core, interface, disk, mount or sensor become a label:

| Series key | Prometheus metric |
|------------|-------------------|
| `cpu.core3.usage_pct` | `pisens_cpu_usage_percent{core="3"}` |
| `cpu.usage_pct` | `pisens_cpu_total_usage_percent` |
| `net.eth0.rx_bps` | `pisens_net_rx_bytes_per_second{iface="eth0"}` |
| `net.total.rx_bps` | `pisens_net_total_rx_bytes_per_second` |
| `disk.sda.read_bps` | `pisens_disk_read_bytes_per_second{device="sda"}` |
| `fs.root.used_pct` | `pisens_filesystem_used_percent{mount="root"}` |
| `temp.cpu_c` | `pisens_temperature_celsius{sensor="cpu"}` |
| `load.1` | `pisens_load1` |

The full table is `rules` in `internal/promtext/promtext.go`. Values keep the units the collectors report (percent, MHz), named in the suffix. Totals and averages get their own metric rather than a `core="all"` style label, so `sum()` and `avg()` over a labelled metric stay correct. A key without a rule is still exported under a sanitised name: `gpu.mem_bytes` becomes `pisens_gpu_mem_bytes`. `pisens_last_sample_timestamp_seconds` tells you when the values were sampled. Until the first sample `/metrics` answers 503, which Prometheus records as a failed scrape.

With [basic auth](#authentication) on, `/metrics` needs the credentials like every other route. Give Prometheus the plain password, not the hash:

```yaml
scrape_configs:
  - job_name: pisens
    basic_auth:
      username: admin
      password_file: /etc/prometheus/pisens_password
    static_configs:
      - targets: ["raspberrypi.local:8080"]
```

### Authentication

The dashboard and API are open to anyone who can reach the port, which is fine on a trusted home network. To require a login, create a bcrypt hash of a password and give pisens both a user name and the hash:

```sh
./bin/pisens hash-password                  # asks for the password twice, without echoing it
printf '%s\n' 'my password' | ./bin/pisens hash-password   # or read it from a pipe
# $2a$10$...

./bin/pisens --auth-user admin --auth-password-hash '$2a$10$...'
```

The hash contains `$` signs, so keep it in single quotes, in the shell and in `/etc/pisens/pisens.env` alike. `hash-password` drops one trailing newline from piped input and keeps everything else, spaces included. bcrypt only uses the first 72 bytes of a password, so longer ones are refused.

With both set, every request needs HTTP basic auth except `GET /api/health`, which stays open for uptime checks. Wrong or missing credentials get a `401` with a `WWW-Authenticate: Basic realm="pisens"` challenge, and wrong ones are logged with the client address. pisens refuses to start if only one of the two settings is given, or if the hash is not a bcrypt hash, so a typo never leaves the dashboard open by accident. Without either setting nothing changes.

In the browser you get a login prompt on the first page load. The browser then sends the credentials with every request to the same origin, including the dashboard's live `EventSource` stream, so nothing else needs setting up. `curl` takes them with `-u`:

```sh
curl -u admin localhost:8080/api/metrics
curl -N -u admin localhost:8080/api/stream
```

Basic auth sends the password with every request, encoded but not encrypted, so anyone who can watch the traffic can read it. That is acceptable on your own LAN. To reach pisens from anywhere else, put it behind a reverse proxy that terminates TLS (Caddy, nginx or Traefik), or use a VPN such as WireGuard or Tailscale, and bind pisens to `127.0.0.1:8080` so only the proxy can reach it. If the proxy handles the login itself, leave pisens' auth off. For a Cloudflare Tunnel see [Remote access with Cloudflare Tunnel](#remote-access-with-cloudflare-tunnel).

### Adding a collector

Collectors live in `internal/collectors`, one file each, and register themselves from `init()`. See `internal/collectors/doc.go` for the conventions, `cpufreq.go` for a small reference implementation and `cpu.go` for one that keeps state between samples. New series keys show up on `/metrics` under a sanitised name; add a rule to `internal/promtext/promtext.go` for a proper name and labels.

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
- runs `systemctl daemon-reload` if the unit changed and `systemctl enable --now pisens`, then checks the service stayed up

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

## Remote access with Cloudflare Tunnel

A [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/) makes the dashboard reachable from anywhere over HTTPS without opening a port on your router. pisens needs no settings for it.

1. Install `cloudflared` and create a tunnel as Cloudflare describes, either on the Pi or on another machine on the same network.
2. Add a public hostname to the tunnel, such as `pi.example.com`, with service type `HTTP` and URL `http://localhost:8080`. If `cloudflared` runs on another machine, use the Pi's address instead, such as `http://192.168.1.20:8080`.
3. **Strongly recommended:** protect the hostname with a [Cloudflare Access](https://developers.cloudflare.com/cloudflare-one/applications/configure-apps/self-hosted-apps/) application before you use it. pisens has no login of its own by default, so without Access anyone who finds the hostname can see your Pi, including the command lines of its top processes (see [Process command lines](#process-command-lines)). In Zero Trust go to Access → Applications → Add an application → Self-hosted, enter the same hostname and add a policy that allows only you, for example the action Allow with an Include rule of Emails set to your own address. The One-time PIN login method then emails you a code when you sign in, and needs no other identity provider.

If `cloudflared` runs on the Pi, you can also set `PISENS_LISTEN=127.0.0.1:8080` so the dashboard is only reachable through the tunnel and not from the rest of your network.

**Basic auth.** Behind Access you do not need pisens' own [basic auth](#authentication): Access already checks who you are before a request reaches the Pi, and a second login prompt only gets in the way. Basic auth is for exposing pisens directly on a LAN.

**The app.** The tunnel serves the dashboard over HTTPS, which browsers require before they let you install it as an app (PWA), and the installed app uses the same Access login as a browser tab. When the Access session runs out (24 hours by default, set per application), the live stream stops and the connection status goes to "Reconnecting" and then "Offline". Reload the page to sign in again.

**How pisens helps the tunnel along.** The live stream at `/api/stream` is sent with `Cache-Control: no-cache, no-transform` and `X-Accel-Buffering: no` and flushed after every event, so Cloudflare passes events through one by one instead of compressing or buffering them. A heartbeat comment goes out every 15 seconds, well inside Cloudflare's 100 second idle timeout. The hashed files under `/assets/` are sent with `Cache-Control: public, max-age=31536000, immutable`, so Cloudflare and the browser can keep them, while `index.html` and the other files at the top level are `no-cache` and a new release shows up on the next load.

**Check it works.** Open the hostname, sign in through Access and leave the dashboard open for a few minutes. The connection status in the header should stay "Live" the whole time and the numbers should keep updating every couple of seconds. If it flips to "Reconnecting" about every 100 seconds, something between you and the Pi is buffering the stream. From a terminal, `curl -N` with an Access [service token](https://developers.cloudflare.com/cloudflare-one/identity/service-tokens/), which needs a Service Auth policy on the application (or temporarily without Access) should print a `snapshot` event every sample interval rather than in bursts:

```sh
curl -N https://pi.example.com/api/stream \
  -H "CF-Access-Client-Id: <id>" -H "CF-Access-Client-Secret: <secret>"
```

## Automatic deployment

`.github/workflows/deploy.yml` installs and updates pisens on the Pi through a GitHub Actions self-hosted runner on the Pi itself. When CI passes for a push to `main`, the deploy job checks out that commit, downloads the run's `pisens-linux-arm64` artifact (nothing is built on the Pi), checks it was built from the pushed commit and runs `deploy/deploy.sh` from the checkout. The script runs itself with `sudo -n`, reuses the functions of `install.sh` and can run any number of times:

- creates the `pisens` user (in the `video` group), `/etc/pisens` and `/var/lib/pisens` if they are missing
- creates `/etc/pisens/pisens.env` from the example only if it does not exist, so your settings are never overwritten
- installs `pisens.service` from the repository, runs `daemon-reload` only when it changed and enables the service
- keeps the current binary as `/usr/local/bin/pisens.prev`, renames the new one into place and restarts the service
- polls `/api/health` for up to 30 seconds until it reports the new version; if it does not, puts the previous binary (and unit) back, restarts and fails, so the run goes red
- prints what it did (`installed v1.2.0` or `updated v1.1.0 → v1.2.0`), the version and the commit, which also end up in the run's summary

Red CI on `main` deploys nothing, and pull requests never reach the Pi: the workflow has no pull request trigger and the job only accepts CI runs of pushes to `main` in this repository. Deploys run one at a time and are never cancelled half way, and a CI run that finishes after a newer commit already passed is skipped. To redeploy the newest successful build of `main`, run the workflow by hand from the Actions tab (Run workflow).

### Setup

1. On the Pi, add a self-hosted runner the standard way: in the repository go to Settings → Actions → Runners → New self-hosted runner, pick Linux and ARM64, and run the commands it shows as your normal user (the one you log in with), accepting the defaults in `./config.sh`. See GitHub's [Adding self-hosted runners](https://docs.github.com/en/actions/hosting-your-own-runners/managing-self-hosted-runners/adding-self-hosted-runners).
2. Run it as a service, so it survives a reboot ([Configuring the self-hosted runner application as a service](https://docs.github.com/en/actions/hosting-your-own-runners/managing-self-hosted-runners/configuring-the-self-hosted-runner-application-as-a-service)):

   ```sh
   cd ~/actions-runner
   sudo ./svc.sh install
   sudo ./svc.sh start
   ```

3. Push to `main`. The first deploy installs pisens; every later one updates it.

The runner needs no custom label (the job asks for `self-hosted`, `linux` and `ARM64`), and nothing has to be installed on the Pi beforehand. The `install.sh` steps in [Install on a Raspberry Pi](#install-on-a-raspberry-pi) still work for a Pi without a runner, and a Pi set up that way is simply updated by the first deploy.

**Note.** The runner user needs passwordless sudo, because the deploy installs a system service. The first user on Raspberry Pi OS and on Ubuntu cloud images has it; if `sudo -n true` fails for the runner user, the deploy stops before changing anything and prints how to set it up (a line like `USER ALL=(ALL) NOPASSWD: ALL` in `/etc/sudoers.d/`). Anything that runs on that runner runs as root on the Pi, so deploys only run for pushes to `main`, and anyone with write access to the repository can reach the Pi. If the repository is public, keep Settings → Actions → General → "Approval for running fork pull request workflows from contributors" at "Require approval for all outside collaborators".

To go back to the previous binary by hand, run `deploy/deploy.sh /usr/local/bin/pisens.prev` from a checkout on the Pi. Remove the runner with `sudo ./svc.sh uninstall` and `./config.sh remove` in `~/actions-runner`.

## CI

GitHub Actions (`.github/workflows/ci.yml`) runs on every push and pull request to `main`:

- Frontend: `npm ci`, `npm run check` and `npm run build` in `web/`
- Go: `go vet`, `golangci-lint` (which fails on Go code that is not gofmt-formatted) and `go test -race` on every package outside `web/node_modules` (`make -s go-packages` prints the list)
- Shell scripts: `shellcheck` on `deploy/`, and the `deploy/deploy.sh` tests (`deploy/tests/`) with stub `systemctl`, `useradd`, `sudo` and health endpoint
- A static `linux/arm64` build with the frontend embedded, uploaded as the `pisens-linux-arm64` artifact on the run page

A new push to a pull request cancels that pull request's run in progress. Runs on `main` are never cancelled, so two quick merges each get their own result and artifact.

To match the lint steps locally, install the golangci-lint version pinned in the workflow and run `make lint` (`make fmt` fixes formatting), and install shellcheck (`apt install shellcheck`) and run `make shellcheck`. `make test-deploy` runs the `deploy.sh` tests; they need neither root nor systemd.

## Releases

Pushing a tag that starts with `v` runs `.github/workflows/release.yml`, which builds the frontend, runs the Go tests and builds static `linux/arm64` and `linux/amd64` binaries that report the tag as their version. Each is packed as `pisens_<version>_linux_<arch>.tar.gz` (the tag without its leading `v`) together with `deploy/`, `README.md` and `LICENSE`. The workflow then creates a GitHub release with generated notes and attaches the archives and a `SHA256SUMS` file. A tag with a suffix, such as `v0.2.0-rc1`, becomes a pre-release.

```sh
git tag -a v0.1.0 -m v0.1.0
git push origin v0.1.0
```

To try the workflow without publishing anything, run it by hand from the Actions tab (Run workflow). That builds the same archives and checksums, named after `git describe`, and uploads them as the `release-dry-run` artifact instead of creating a release.
