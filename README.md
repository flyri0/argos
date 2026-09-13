# Argos

A personal, local-first envelope-budgeting app, inspired by [YNAB](https://www.ynab.com/)
and [Actual Budget](https://actualbudget.org/). Argos ships as a single
self-contained Go binary that serves both an HTTP API and an embedded React
PWA, so you can self-host your own budget on a home server, NAS, or
Raspberry Pi — no cloud dependency, no external database, and no per-seat
subscription. The PWA works fully offline and syncs back to the server from
any device on your LAN (or just `localhost`) once connectivity returns.

For the full architecture, data model, sync protocol, and API reference,
see [`project_spec.md`](./project_spec.md) — it is the single source of
truth for how Argos is designed and what's in scope for the MVP.

## Features

- Envelope budgeting with correct rollover and overspending rules.
- Fully offline-capable PWA (IndexedDB + outbox), syncing via a Hybrid
  Logical Clock instead of raw timestamps, so a device with a wrong clock
  can't silently clobber other devices' data.
- Single portable binary: no CGO, no external services — just SQLite and
  an embedded frontend.
- Runs in desktop mode (system tray icon) or headless mode (systemd/
  launchd/Windows Service), from the same binary.
- Explicit device pairing for LAN access — no shared password, no
  unauthenticated devices.
- i18n scaffolding from day one (English is the only shipped locale for
  the MVP).

## For users: running Argos

### Prerequisites

- A 64-bit Linux, macOS, or Windows machine (a Raspberry Pi works fine).
- No other dependencies — the binary is self-contained.

### Getting the binary

Download the `argos` binary for your platform from the
[Releases](../../releases) page, or build it yourself (see
[Building from source](#building-from-source) below).

### Running it

Desktop use (shows a system tray icon):

```sh
./argos
```

Headless use, e.g. on a NAS or Raspberry Pi with no desktop environment:

```sh
./argos serve --headless
```

On first run, Argos prints a one-time **setup code** to stdout (and shows
it in the tray icon in desktop mode). Use this code to pair your first
device — open `http://localhost:8080` (or the LAN address, once you enable
LAN mode) in a browser and follow the pairing prompt. Every device after
the first is approved from an already-paired device.

By default Argos binds to `localhost` only. To make it reachable from
other devices on your network, switch to LAN mode from the tray menu (desktop
mode) or via config/environment variable (headless mode) — see below.

### Configuration

Both modes read the same config file (created on first run):

| OS | Path |
|---|---|
| Linux | `$XDG_CONFIG_HOME/argos/config.json` (usually `~/.config/argos/config.json`) |
| macOS / Windows | your per-user config directory, `argos/config.json` |

```json
{
  "bind_mode": "localhost",
  "port": 8080,
  "data_dir": "/path/to/data"
}
```

Each field can also be overridden via environment variable
(`ARGOS_BIND_MODE`, `ARGOS_PORT`, `ARGOS_DATA_DIR`) or CLI flag
(`--bind-mode`, `--port`, `--data-dir`), in increasing order of
precedence: flags > environment variables > config file > defaults.

`bind_mode` is either `"localhost"` (default) or `"lan"` — LAN mode is
never enabled silently.

### Logs

Argos writes a human-readable log file to `<data_dir>/logs/argos.log` —
startup/shutdown, every HTTP request (method, path, status, duration), and
detailed `/sync` push summaries. A fresh file starts on every launch and
whenever the active one passes 10MB; at most 5 log files are ever kept, the
oldest deleted automatically. See
[`project_spec.md` §3.4](./project_spec.md#34-logging) for the full
details of what's logged and why.

### Running as a system service

To have Argos start automatically on boot without a logged-in user or
desktop session:

```sh
./argos service install
```

This registers Argos as a systemd user unit (Linux), a launchd agent
(macOS), or a Windows Service (Windows), running `argos serve --headless`.
Manage it afterwards with your platform's normal service commands (e.g.
`systemctl status argos`). To remove it:

```sh
./argos service uninstall
```

## For developers: building from source

### Prerequisites

- Go 1.25+
- Node.js (for the frontend build) and npm

### Build

```sh
make build
```

This builds the frontend first (`web/` → `internal/webui/dist`, via Vite),
then compiles the Go binary with the frontend embedded, producing
`bin/argos` (`bin/argos.exe` on Windows).

```sh
make run
```

Builds and runs the resulting binary.

### Tests

```sh
make test        # both Go and web tests
make test-go      # go test ./...
make test-web     # cd web && npm test
```

### Formatting and linting

```sh
make fmt
make lint
```

### Repo layout

```
argos/
├── cmd/argos/        # entrypoint, embeds the built frontend
├── internal/
│   ├── api/          # HTTP handlers
│   ├── auth/         # device pairing, token issuance/validation
│   ├── budget/        # pure budgeting engine (rollover, overspending, availability)
│   ├── config/        # shared runtime config (bind mode, port, data dir)
│   ├── db/            # SQLite access layer, migrations
│   ├── logging/       # rotating log files under <data_dir>/logs
│   ├── sync/          # /sync endpoint logic, conflict resolution (HLC)
│   ├── tray/          # desktop mode: system tray integration
│   └── service/       # headless mode: service install/uninstall per OS
├── web/               # React PWA frontend (Dexie.js, react-i18next)
├── project_spec.md    # architecture, data model, API — the source of truth
└── CLAUDE.md          # agent/contributor instructions
```

`internal/budget` has no dependency on `internal/api` or the database
layer — it's pure, unit-tested budgeting logic.

Read [`project_spec.md`](./project_spec.md) before making architectural or
API changes — it defines scope for the MVP and should be treated as
authoritative.

## License

MIT — see [`LICENSE`](./LICENSE).
