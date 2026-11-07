# pi-sens

A small system monitor for the Raspberry Pi, shipped as a single Go binary.

## Build and run

You need Go 1.23 or newer and `make`.

```sh
make build          # host binary in bin/pisens
make build-arm64    # static linux/arm64 binary for the Pi in bin/pisens-linux-arm64
make test           # go vet + go test
make run            # build and run; pass flags with ARGS='--listen :9090'
```

The version reported by the binary comes from `git describe`. Override it with `make build VERSION=1.0.0`.

Start the server and check that it is up:

```sh
./bin/pisens
curl localhost:8080/api/health
# {"status":"ok","version":"..."}
```

To run on the Pi, copy `bin/pisens-linux-arm64` over and start it. It has no runtime dependencies.

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
