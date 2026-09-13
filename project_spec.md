# Argos — MVP Project Spec

## 1. Overview

**Argos** is a personal, local-first envelope-budgeting application inspired by [YNAB](https://www.ynab.com/) and [Actual Budget](https://actualbudget.org/). It ships as a **single self-contained binary** that runs an HTTP API and serves a **PWA frontend**, accessible over `localhost` or the local network. The PWA supports full offline editing and syncs changes back to the server when connectivity is available.

### Goals

- Envelope budgeting with correct rollover semantics (the core mechanic that makes this useful).
- Local-first: the app is fully usable offline, on any device on the network, with no cloud dependency.
- Single, portable binary — no external database service, no complex install.
- Lightweight enough to self-host on a Raspberry Pi or similar low-power hardware.
- Internationalized from day one, with English as the primary/default language.

### Non-goals (for the MVP)

- Automatic bank sync (Plaid/SimpleFIN/GoCardless/Pluggy-style integrations).
- Multi-user concurrent editing with full CRDT-based conflict resolution.
- End-to-end encryption of synced data.
- Native mobile apps (iOS/Android store builds) — the installable PWA covers this need.
- Multi-currency support.
- Custom, user-built dashboards and reports.
- Automated or scheduled backups. A manual export endpoint may exist, but no backup mechanism ships in the MVP — see the planning document for its V1 placement.

## 2. Architecture

```
┌───────────────────────────────────┐        ┌────────────────────────────────────┐
│        Single Go binary            │        │     PWA (any device on the LAN)     │
│  ┌───────────────────────────────┐ │        │  ┌────────────────────────────────┐ │
│  │  HTTP server (API + embedded  │◄┼────────┼─►│  Service worker (app shell      │ │
│  │  static frontend)             │ │  LAN / │  │  cache, installable)            │ │
│  ├───────────────────────────────┤ │local-  │  ├────────────────────────────────┤ │
│  │  SQLite (source of truth)     │ │  host  │  │  IndexedDB (local replica)      │ │
│  ├───────────────────────────────┤ │        │  ├────────────────────────────────┤ │
│  │  /sync endpoint                │ │        │  │  Outbox (pending mutations)     │ │
│  └───────────────────────────────┘ │        │  └────────────────────────────────┘ │
└───────────────────────────────────┘        └────────────────────────────────────┘
```

### 2.1 Server

- **Language**: Go.
- **HTTP**: standard `net/http` or a lightweight router (e.g. `chi`).
- **Database**: SQLite via `modernc.org/sqlite` (pure Go, no CGO — keeps cross-compilation simple).
- **Frontend delivery**: the built frontend (static HTML/JS/CSS) is embedded into the binary via Go's `embed.FS` and served directly. `go build` produces one artifact that contains everything.
- **Persistence model**: the server's SQLite file is the single source of truth for the budget data.

### 2.2 Client (PWA)

- **Framework**: React.
- **Local persistence**: IndexedDB via Dexie.js. Chosen over an in-browser SQLite/WASM build for simplicity — personal finance data volumes don't need relational query power on the client.
- **Offline-first writes**: every user action (create/edit transaction, budget assignment, etc.) writes to IndexedDB first and is immediately reflected in the UI. The write is also appended to a local **outbox** table.
- **Installability**: `manifest.json` + a service worker (e.g. via Workbox) caches the app shell so the PWA can be installed and opened with no network at all.

### 2.3 Sync protocol

- The client's outbox is drained to the server's `/sync` endpoint whenever connectivity is detected.
- Every syncable row carries a **Hybrid Logical Clock (HLC)** timestamp instead of a plain wall-clock time, and `server_version` (assigned by the server on write, monotonically increasing). See §5.2 for the exact columns.
- **Why HLC instead of a raw timestamp**: a plain client-set `updated_at` is vulnerable to a device with a wrong system clock — it could win every conflict indefinitely and silently overwrite newer, correct data from other devices, with no error surfaced anywhere. Actual Budget solves this with a Hybrid Logical Clock (its `@actual-app/crdt` package), and Argos adopts the same mechanism: each HLC value is a triple `(physical_ms, counter, node_id)`. A device advances its clock to `max(its own physical time, physical time seen in any message it has received)`, incrementing the counter whenever the physical component doesn't move forward. This guarantees monotonicity per device and preserves causal order — if device A's write caused device B's later write, B's timestamp is guaranteed greater — even when the two devices' wall clocks disagree.
- As a further safeguard beyond Actual's own design, the server rejects (rather than silently accepts) any incoming HLC whose physical component is further than a small bound (e.g. a few minutes) ahead of the server's own clock, surfacing a clear "check this device's clock" error instead of letting a badly-drifted device poison the dataset.
- Conflict resolution: **last-write-wins per row**, keyed by the HLC triple (compare `physical_ms`, then `counter`, then `node_id` as a final deterministic tiebreak). This is a deliberate simplification over full per-field CRDT merge (which Actual also layers on top of its HLC) — appropriate because Argos targets a single user across a handful of personal devices, where true concurrent edits to the same row are rare. Per-field merge is listed as a possible future enhancement, not an MVP requirement.
- Deletes are never physical on the server: a delete sets `deleted_at` and bumps `server_version` like any other write, so the tombstone can propagate to other devices on their next sync instead of silently disappearing. Clients purge fully-synced tombstones locally once acknowledged.
- The server responds to a sync push with any row whose `server_version` is greater than the client's last-seen cursor, which the client applies to its local IndexedDB replica.
- Each device stores a copy of the server's `sync_id` (see §5.2, `server_meta`). If the server's `sync_id` ever changes — a restore from backup, a manual reset — the client detects the mismatch and re-downloads the full dataset instead of attempting to merge against a server history it no longer shares.
- **Partial failure within a push**: a single `/sync` push can carry mutations accumulated over days offline, touching many unrelated rows. Failing the entire push because one mutation is invalid (e.g. it references a category deleted by another device in the meantime) would block every other valid change in that batch from reaching the server — bad odds the longer a device has been offline. So the server applies mutations **independently by default** and reports success/failure per mutation, not per batch. The exception is mutations that are only meaningful together — the two sides of a transfer (linked by `transfer_id`), or a category/payee delete alongside its `reassign_to` move (§5.4) — the client submits these as a **group**, and the server commits or rejects each group atomically, while still treating different groups in the same push independently of each other. See §2.4 for the exact wire format.
- **Schema version mismatch**: if the client's expected `schema_version` doesn't match what the server reports, the client stops pushing/pulling and shows a persistent "update required" notice — but keeps working fully offline against its local IndexedDB copy in the meantime, since a stale sync connection is not a reason to lock someone out of their own data. This mirrors Actual Budget's own "Update Required" sync notification, which pauses syncing rather than the app itself until the client is updated.

### 2.4 Sync wire format

This is the exact, binding shape of the `/sync` exchange — implementations must follow it precisely rather than inventing an equivalent one, since both the Go server and the TypeScript client have to agree on it independently.

**Request:**

```json
{
  "since": 0,
  "mutations": [
    {
      "table": "transactions",
      "op": "upsert",
      "group_id": null,
      "row": { "id": "...", "account_id": "...", "amount": -500, "hlc_physical": 0, "hlc_counter": 0, "hlc_node_id": "..." }
    },
    {
      "table": "transactions",
      "op": "delete",
      "group_id": null,
      "row": { "id": "...", "deleted_at": 0, "hlc_physical": 0, "hlc_counter": 0, "hlc_node_id": "..." }
    }
  ]
}
```

- `since`: the client's last-seen `server_version` cursor (`0` on a device's first sync).
- `mutations`: a flat list, in any order. Each entry's `op` is `"upsert"` (covers both create and update — every write replaces the row's full current state, there is no separate "create" shape) or `"delete"` (still carries a fresh HLC triple like any other write, and sets `deleted_at` in `row`).
- `group_id`: `null` for standalone mutations. Mutations sharing the same non-null `group_id` string within one request are applied atomically as a group (§2.3) — currently used for the two sides of a transfer, and for a category/payee delete bundled with its `reassign_to` move.

**Response:**

```json
{
  "server_version": 42,
  "sync_id": "...",
  "schema_version": 1,
  "results": [
    { "table": "transactions", "id": "...", "status": "applied" }
  ],
  "changes": [
    { "table": "transactions", "row": { "...": "..." } }
  ]
}
```

- `server_version`: the new high-water mark after this push; the client stores it as its next `since` cursor.
- `sync_id` / `schema_version`: copied from `server_meta` (§5.2) on every response, so the client can detect a reset or a schema mismatch on every sync, not just at startup.
- `results`: exactly one entry per submitted mutation (or one per group, for grouped mutations — reported once under the group's first member), `status` one of `"applied"`, `"rejected_stale"` (an existing row had a greater-or-equal HLC — expected behavior, not an error, §2.3), or `"rejected_invalid"` (accompanied by an `error` object shaped per §7.2).
- `changes`: every row, across all syncable tables, with `server_version` greater than the request's `since` — this is the pull half of the same round-trip.

## 3. Binary lifecycle & system integration

A background HTTP server that a user can only start by double-clicking, with no visible status and no clean way to stop it, is not acceptable — it must always be clear whether Argos is running, what mode it's bound to (localhost-only vs LAN), and how to stop it. Argos supports two run modes from the same binary:

### 3.1 Desktop mode (default when launched from a GUI)

- Runs with a **system tray / menu bar icon** (e.g. via a cross-platform Go systray library).
- The tray menu shows, at a glance:
  - Current status (running/stopped)
  - Current bind mode (`localhost only` or `LAN`) and port
  - **Open in browser** shortcut
  - Toggle between `localhost only` and `LAN` (requires a restart of the listener, done in-process)
  - **Start automatically on login** toggle
  - **Quit** — stops the HTTP server and exits cleanly (not just closing a window)
- No terminal window, no silent background process with no way to interact with it.

### 3.2 Service / headless mode

For running on a home server, NAS, or Raspberry Pi without a desktop environment:

- A CLI flag (e.g. `argos serve --headless`) runs the server with no tray dependency.
- Configuration (bind address, port, data directory) is provided via a config file and/or environment variables, not just tray toggles.
- A subcommand (e.g. `argos service install`) registers Argos as a system service — `systemd` unit on Linux, `launchd` agent on macOS, Windows Service on Windows — so it **starts with the system** without requiring a logged-in user or a tray.
- Standard service commands apply for start/stop/status (`systemctl status argos`, etc.), giving a clear way to check whether it's running and to stop it outside of a GUI.

### 3.3 Shared behavior

- Both modes read the same config file — fields `bind_mode` (`"localhost"` or `"lan"`), `port`, and `data_dir` — so switching a machine from desktop use to headless service use doesn't require reconfiguration.
- The bind mode is always explicit and visible (never silently listening on `0.0.0.0` without the user having chosen LAN mode) — localhost-only is the default until the user opts into LAN.

## 4. Internationalization (i18n)

i18n is part of the MVP, not something bolted on later.

- **Default/primary language**: English. All strings are authored in English first.
- **Frontend**: a standard i18n library (e.g. `react-i18next` or `i18next` directly) with locale files under `web/src/locales/<lang>/`, keyed by namespace (e.g. `common`, `budget`, `accounts`).
- **Server**: English-only. The API returns stable, machine-readable error codes (§7.2) alongside a plain English message for logs/debugging — it never returns pre-translated text. Localization of anything user-facing is entirely the frontend's responsibility: the client maps error codes to a friendly, translated message using its own locale files. This keeps the server simple and avoids duplicating translation logic on both sides.
- **Locale detection**: default to the browser/OS locale on first run, with a manual override in settings.
- Adding a new language should only require adding a new locale file, with no code changes to the components themselves.

## 5. Data model (MVP)

SQLite has no native `UUID`, `BOOLEAN`, or `DATE` type — the column types used throughout this section are descriptive, not literal SQL. The actual physical mapping: `uuid` → `TEXT` (canonical lowercase hyphenated form, e.g. `550e8400-e29b-41d4-a716-446655440000`); `boolean` → `INTEGER` (`0`/`1`); `date` and the `month` field on `budget_entries` → `TEXT`, respectively `YYYY-MM-DD` and zero-padded `YYYY-MM` (e.g. `2026-03`). `text` and `integer` map directly to SQLite's own storage classes.

### 5.1 Sync metadata (present on every syncable table below)

Offline-first editing and later feature growth both come down to the same requirement: rows must be safely creatable on any device without a round-trip, mergeable without ambiguity, and deletable without breaking sync. Every table listed in §5.2 includes these columns in addition to its own business columns:

```
id             uuid     primary key   -- client-generated (e.g. UUIDv4), never server-assigned.
                                       -- Lets the client create records offline with a permanent id
                                       -- immediately, with no placeholder/remap step once synced.
hlc_physical   integer  not null      -- Hybrid Logical Clock: physical time component, unix ms.
hlc_counter    integer  not null      -- HLC: logical counter, increments when physical time hasn't
                                       -- moved forward since the device's last write or last-seen
                                       -- message. Together with hlc_physical and hlc_node_id, this
                                       -- drives last-write-wins conflict resolution (see §2.3) without
                                       -- trusting any single device's wall clock.
hlc_node_id    uuid     not null      -- the device (see `devices`, below) that produced this HLC value;
                                       -- final deterministic tiebreak when physical+counter are equal.
                                       -- No foreign key to `devices` is declared: a row's writer must
                                       -- always supply all three HLC fields on every write, even for
                                       -- CRUD endpoints built before device pairing (§6) or /sync (§2.3)
                                       -- exist. There is no server-side fallback/placeholder value —
                                       -- the write path is the same before and after pairing lands.
server_version integer                -- assigned by the server on every write, strictly increasing
                                       -- across the whole database (not per-row). Used as the sync
                                       -- cursor: "give me everything after version N".
deleted_at     integer  null          -- soft-delete tombstone. Rows are never hard-deleted; the API's
                                       -- DELETE endpoints set this instead, so the deletion is itself
                                       -- a change that can propagate to other devices on next sync.
```

### 5.2 Tables

```
accounts
  name          text
  type          text            -- one of: checking, savings, credit, cash, investment, other
                                 -- (§7.1); any other value is rejected with 400 INVALID_ACCOUNT_TYPE
  on_budget     boolean         -- on-budget accounts affect the budget; off-budget only track balance
  closed        boolean         -- a closed account rejects new transactions, see §7.1
                                 -- Note: there is deliberately no `balance` column here. Storing a
                                 -- mutable running balance on the account row would make it a shared
                                 -- piece of state that two offline devices could clobber via row-level
                                 -- last-write-wins (§2.3) even when their actual transactions never
                                 -- conflicted. Balance is always computed by summing transactions —
                                 -- see §5.3 — which is slower per read but can never drift or lose money.
  currency      text            -- ISO 4217 code, default from app settings. Unused by MVP business
                                 -- logic (single-currency only) but present now so adding real
                                 -- multi-currency support later (V2) is additive, not a migration.
  notes         text     null   -- reserved for V2 (markdown account notes); unused in MVP UI

category_groups
  name          text
  is_income     boolean         -- exactly one income group exists and cannot be deleted
  sort_order    integer         -- caller-managed; see §7.1 — the server never renumbers this

categories
  group_id      uuid references category_groups
  name          text
  hidden        boolean
  sort_order    integer         -- caller-managed, same rule as category_groups.sort_order
  notes         text     null   -- reserved for V2 (markdown category notes); unused in MVP UI

payees
  name          text

transactions
  account_id    uuid references accounts
  category_id   uuid references categories (nullable for off-budget accounts, transfers, and splits)
  payee_id      uuid references payees (nullable)
  parent_id     uuid references transactions(id), nullable
                                 -- reserved for V1 split transactions: a split is modeled as child
                                 -- transactions sharing a parent, each with its own category_id and
                                 -- a slice of the total amount. Always null in the MVP, where every
                                 -- transaction is its own top-level row — but the column exists now
                                 -- so splits don't require an amount-shape migration later.
  date          date
  amount        integer         -- in minor currency units, negative = outflow
  cleared       boolean         -- defaults to false on creation
  notes         text
  transfer_id   uuid     null   -- links the two sides of an inter-account transfer; null for
                                 -- every non-transfer transaction (the common case)

budget_entries
  category_id   uuid references categories
  month         text            -- "YYYY-MM", zero-padded
  budgeted      integer         -- amount assigned this month, in minor currency units.
                                 -- Unique constraint on (category_id, month). Setting this to 0 via
                                 -- PUT /api/budget/:month/:category_id deletes the row instead of
                                 -- storing a zero (a no-op if no row exists yet) — §5.4 relies on
                                 -- "does any row exist" as its in-use signal, and a zero row left
                                 -- lingering forever would make that signal meaningless.

server_meta                     -- single row, not synced to clients as a normal record
  sync_id        uuid           -- generated once, on first run. Changes only on an explicit reset
                                 -- (e.g. restoring from backup). Clients compare their cached copy
                                 -- against this value before merging (see §2.3) — a mismatch means
                                 -- "don't merge, re-download everything."
  schema_version integer        -- bumped on breaking schema changes, so a client can detect it's
                                 -- talking to a server it doesn't know how to sync with yet.

devices                          -- see §6, Authentication & device pairing. Not synced to clients.
  id             uuid primary key
  name           text           -- user-editable label, e.g. "Ana's phone"; defaults to "First device"
                                 -- for the bootstrap device (§6.1) or "Unnamed device" for any other
                                 -- (§6.2); renamed via PATCH /api/devices/:id (§7.3)
  token_hash     text           -- hash of the long-lived pairing token issued to this device
  approved_at    integer
  last_seen_at   integer  null
  revoked_at     integer  null
```

Tables intentionally **not** in the MVP but designed for without any planned schema conflict: `rules`, `schedules`, `tags` + a `transaction_tags` join table, and `category_goals`. Each is additive — a new table referencing existing ids — and none of them require changing an MVP table's shape.

### 5.3 Derived values (computed, not stored as raw truth)

- `balance` for an account = sum of `amount` across all non-deleted transactions in that account. Never stored; always computed. For performance on large histories, an index on `transactions(account_id, deleted_at)` keeps this a cheap aggregate query, and the server may cache the result in memory — but the cache is a read optimization only, never the value written to disk or synced.
- `activity` for a category/month = sum of transaction amounts in that category for that month.
- `available` for a category/month = `available` from the previous month (if positive; reset to 0 if the previous month ended negative, per the overspending rule) + `budgeted` (this month) + `activity` (this month).
- `to_budget` for a month = total account balances of on-budget accounts, minus everything already budgeted across all months to date.

The exact rollover and overspending rules are the most important — and most error-prone — piece of business logic in the app. They should live in a pure, well-tested module shared conceptually between client and server (even if implemented twice in Go and TypeScript for the MVP).

### 5.4 Deleting categories and payees with existing transactions

Mirrors Actual Budget's own behavior, since it's a well-tested UX for this exact problem: a category or payee can be deleted outright only if it has never been used (no transactions reference it, and — for categories — it holds no leftover balance). If it *has* been used, the delete request must include a `reassign_to` target (another category or payee id); the server moves every referencing transaction, and any leftover category balance, to the target *in the same transaction* before soft-deleting the source. This means a deleted category/payee never leaves a dangling reference behind — historical transactions always point at a category/payee that still exists.

**Defining "leftover balance" without the rollover engine**: the true `available` figure (§5.3) depends on the budgeting engine, which is its own carefully-tested module — using it as the gate for a data-loss-preventing check would tie category deletion to a piece of logic that may not exist yet, or that could itself have a bug at exactly the wrong moment. Instead, a category counts as having a leftover balance if it has **any row at all in `budget_entries`**, for any month, regardless of the amount. This is deliberately coarse: it will occasionally require a reassignment for a category whose true available is actually zero (a harmless extra click), but it can never do the opposite — skip reassignment for a category that still carries a positive rollover from an earlier month where nothing was budgeted this month. Losing money silently is the one failure mode this check must never have; asking for an unnecessary reassignment is an acceptable cost for that guarantee. On reassignment, every `budget_entries` row for the source category moves to `reassign_to`: if the target already has a row for that month, sum the `budgeted` amounts into it and remove the source row; otherwise, simply re-point the source row to the target category. See §5.2's note on `budget_entries` for the companion rule that keeps zero-value rows from accumulating and making this signal meaningless over time.

## 6. Authentication & device pairing

LAN mode means any device on the network can *reach* the server — Argos must not let that also mean any device can *use* it unattended. The MVP requires explicit, one-time approval per device rather than a shared password (which is one more secret to manage and share) or no auth at all (which trusts every device on the network by default, including someone else's phone on the same Wi-Fi).

### 6.1 Bootstrap: pairing the first device

The normal flow below (§6.2) assumes an already-trusted device exists to approve the new one — which is fine after setup, but doesn't help on the very first run, especially on a **headless install** (NAS, Raspberry Pi with no monitor) where there's no local browser session to fall back on.

- On first startup — detected by an empty `devices` table — the server generates a **setup code** and prints it to stdout/the log file. In desktop mode, the tray icon also shows it directly. Because this code no longer expires on a timer, it must resist brute-forcing on its own: it's a random token of at least 8 alphanumeric characters (≈41+ bits of entropy), not a short human-typed PIN — the previous 15-minute window was doing real security work by limiting how many guesses were possible, and removing it means the token itself now has to carry that weight.
- The code has **no time-based expiry**. It stays valid indefinitely — whether the user picks up their phone to pair it five seconds or five days after installing — until the moment a device actually presents it.
- This is a dedicated endpoint, `POST /api/pairing/bootstrap` (§7.3), distinct from the per-device request/approve flow in §6.2. The first device that presents the correct code to it is auto-approved and becomes the first trusted device, recorded with the name `"First device"` (renamable later via `PATCH /api/devices/:id`) — no prior approval needed, since none can exist yet. That single use is what closes the window, not a clock: the code is consumed on first use and cannot pair a second device. Once `devices` is non-empty, this endpoint always responds `410 Gone` — bootstrap is permanently over for that installation, and every device after the first must go through normal approval (§6.2) from an already-trusted device.
- Before anyone has paired, if the operator suspects the printed code was exposed, a CLI command (e.g. `argos setup regenerate-code`) invalidates it and prints a new one. This command only does anything meaningful while `devices` is still empty — once the first device has paired, bootstrap is over and there's no code left to regenerate.
- This same mechanism covers desktop installs too — a user who never opens `localhost` locally and only ever accesses Argos from their phone over LAN still has a working bootstrap path, on their own schedule.

### 6.2 Subsequent devices

- **First contact**: a device hitting the API without a valid token gets a `403 PAIRING_REQUIRED`. The frontend shows a "waiting for approval" screen with a pairing code.
- **The pairing code is short-lived**: 10 minutes from issuance, unlike the bootstrap code in §6.1 — it exists for a live, human-in-the-loop confirmation, not as a durable secret, so a short expiry is both safe and appropriate here.
- **Approval**: the *already-trusted* device (or the desktop tray app) shows a prompt — "New device requesting access: code `4821`, approve?" — and the user confirms there. Approval mints a long-lived token for the new device and records it in `devices` (§5.2) with a default name of `"Unnamed device"`.
- **Every request after pairing** carries that device's token; the server checks it against `devices.token_hash` and rejects revoked or unknown tokens.
- **Localhost is implicitly trusted**: a request originating from `127.0.0.1` on the same machine running the server does not need pairing — you always have direct access to your own server without a chicken-and-egg approval step.
- **Managing devices**: the tray app (desktop mode) and a settings page (PWA) both list paired devices with last-seen time, allow renaming a device (`PATCH /api/devices/:id`), and allow revoking any of them (`DELETE /api/devices/:id`), which immediately invalidates that device's token.
- Out of scope for the MVP: per-device permission levels (read-only vs full access), and pairing over the open internet (this whole scheme assumes a trusted LAN as the transport).

### 6.3 Rate limiting on pairing attempts

Both the bootstrap setup code (§6.1) and the per-request pairing code (§6.2) are secrets guessed over the network, so both endpoints (`/api/pairing/bootstrap` and `/api/pairing/approve`) enforce a per-source lockout with concrete numbers, not just "some" rate limiting: after **5 consecutive failed attempts** from the same source IP, that IP is locked out for **1 minute**; each further failed attempt while still within a lockout period doubles the lockout duration (2, 4, 8… minutes), capped at **30 minutes**. The failure count for a source resets after one successful attempt, or after **24 hours** with no failed attempts from it. This is what actually makes the setup code's lack of time-based expiry safe — entropy plus a bounded guessing rate together make brute-forcing impractical, where either alone would not be enough.

## 7. API conventions and endpoints

### 7.1 Conventions

- **Success responses** return the resource directly as JSON — an object for a single resource, an array for a list. No wrapper envelope.
- **Error responses** are shaped `{"error": {"code": "SOME_CODE", "message": "plain English, for logs/debugging only"}}`. The frontend never shows `message` to the user (§4) — it maps `code` to a localized string. See §7.2 for the canonical list of codes.
- **HTTP status codes**: `200` for a successful `GET`/`PATCH`/`PUT`; `201` for a successful `POST` that creates a resource; `204` (empty body) for a successful `DELETE`; `400` for a malformed request or an invalid field value; `403` for a pairing/auth failure; `404` for an unknown id; `409` for a domain-level conflict (in-use-needs-reassign, closed account, clock skew); `429` for a rate-limited pairing attempt.
- **No pagination** in the MVP. List endpoints (`GET /api/accounts`, `/api/categories`, `/api/payees`, `/api/transactions`) return the full result set; pagination is deferred to V1 if real-world data volumes require it.
- `accounts.type` accepts exactly: `checking`, `savings`, `credit`, `cash`, `investment`, `other`. Any other value is `400 INVALID_ACCOUNT_TYPE`.
- A closed account (`accounts.closed = true`) rejects new transactions: `POST /api/transactions` against a closed account returns `409 ACCOUNT_CLOSED`.
- `GET /api/transactions` requires `account_id` as a query parameter — there is no global, all-accounts transaction feed endpoint in the MVP.
- `sort_order` (on `category_groups` and `categories`) is supplied and fully managed by the caller. The server does not enforce uniqueness or auto-renumber siblings; if two rows share a value, their relative order is unspecified but stable.

### 7.2 Error codes

A living list — any new machine-readable error code introduced in code must be added here in the same commit that introduces it (see `CLAUDE.md`).

| Code | HTTP status | Meaning |
|---|---|---|
| `INVALID_ACCOUNT_TYPE` | 400 | `accounts.type` isn't one of the accepted values (§7.1) |
| `ACCOUNT_CLOSED` | 409 | attempted to add a transaction to a closed account |
| `CATEGORY_IN_USE_NEEDS_REASSIGN` | 409 | category delete requested without `reassign_to`, but it's in use (§5.4) |
| `PAYEE_IN_USE_NEEDS_REASSIGN` | 409 | payee delete requested without `reassign_to`, but it's in use (§5.4) |
| `PAYEE_NOT_FOUND` | 404 | no payee with the given `:id` |
| `PAYEE_EXISTS` | 409 | `POST /api/payees` supplied an `id` already in use |
| `REASSIGN_TARGET_NOT_FOUND` | 404 | `reassign_to` doesn't name an existing, non-deleted category/payee (§5.4) |
| `TRANSACTION_NOT_FOUND` | 404 | no transaction with the given `:id` |
| `TRANSACTION_EXISTS` | 409 | `POST /api/transactions` supplied an `id` (or `transfer_transaction_id`) already in use |
| `PAIRING_REQUIRED` | 403 | request has no valid, non-revoked device token (§6.2) |
| `PAIRING_RATE_LIMITED` | 429 | too many failed pairing attempts from this source (§6.3) |
| `BOOTSTRAP_CLOSED` | 410 | `POST /api/pairing/bootstrap` called after the first device has already paired (§6.1) |
| `CLOCK_SKEW_TOO_LARGE` | 409 | an incoming HLC's physical time is too far ahead of the server's (§2.3) |
| `SYNC_MUTATION_INVALID` | n/a — nested in a `/sync` result, not a top-level status (§2.4) | a `/sync` mutation's row is structurally invalid or references a row that doesn't exist |

### 7.3 Endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/accounts` | List accounts |
| `POST` | `/api/accounts` | Create account |
| `PATCH` | `/api/accounts/:id` | Update/close account |
| `GET` | `/api/categories` | List category groups + categories |
| `POST` | `/api/categories` | Create category |
| `PATCH` | `/api/categories/:id` | Update/hide category, or soft-delete with `reassign_to` (see §5.4) |
| `GET` | `/api/payees` | List payees |
| `POST` | `/api/payees` | Create payee |
| `PATCH` | `/api/payees/:id` | Rename payee, or soft-delete with `reassign_to` (see §5.4) |
| `GET` | `/api/transactions?account_id=` | List transactions for an account |
| `POST` | `/api/transactions` | Create transaction |
| `PATCH` | `/api/transactions/:id` | Update transaction |
| `DELETE` | `/api/transactions/:id` | Delete transaction (soft delete — sets `deleted_at`, see §5.1) |
| `GET` | `/api/budget/:month` | Get budget entries + computed availability for a month |
| `PUT` | `/api/budget/:month/:category_id` | Set budgeted amount (deletes the row if set to 0, see §5.2) |
| `POST` | `/api/pairing/bootstrap` | First device claims the printed setup code (§6.1); `410 Gone` once `devices` is non-empty |
| `POST` | `/api/pairing/request` | Unpaired device requests access, receives a pairing code |
| `POST` | `/api/pairing/approve` | Trusted device approves a pending pairing code, mints a token |
| `GET` | `/api/devices` | List paired devices |
| `PATCH` | `/api/devices/:id` | Rename a paired device |
| `DELETE` | `/api/devices/:id` | Revoke a device's access |
| `POST` | `/sync` | Push local outbox mutations, pull remote changes since cursor (see §2.4) |
| `GET` | `/health` | Health check (no auth required) |

## 8. MVP feature checklist

- [ ] Accounts: create, edit, close (on-budget / off-budget); balance always computed from transactions, never stored; `type` validated against the fixed enum (§7.1)
- [ ] Transactions: create, edit, delete, mark cleared, transfers between accounts; rejected on closed accounts
- [ ] Categories: groups + categories, single non-deletable income group, reassignment flow required to delete a category/payee that's in use
- [ ] Payees: create, rename, soft-delete with reassignment (§5.4)
- [ ] Budgeting: assign amounts per category/month, correct rollover, correct overspending rule, "Available to Budget" calculation, zero-amount PUT deletes the row
- [ ] Monthly budget grid UI (budgeted / activity / available)
- [ ] Account register UI
- [ ] API conventions applied consistently: response/error envelope, HTTP status codes, error code table (§7.1–§7.2)
- [ ] Go binary with embedded frontend (`embed.FS`), single-command build
- [ ] SQLite persistence on the server
- [ ] PWA: installable, offline-capable via IndexedDB + outbox + `/sync`
- [ ] Sync conflict resolution via Hybrid Logical Clock (physical + counter + node id), not raw client timestamps
- [ ] Sync push: exact wire format from §2.4, per-mutation success/failure reporting, explicit atomic groups for transfers and category/payee reassignment
- [ ] Schema version mismatch: sync pauses with an "update required" notice, local offline use keeps working
- [ ] Desktop mode: tray icon with status, bind-mode toggle, start-on-login, clean quit
- [ ] Headless mode: CLI flags, config file, service installation (systemd/launchd/Windows Service)
- [ ] i18n scaffolding in place (English as default locale; server emits English-only error codes/messages, frontend owns all translation)
- [ ] Device pairing: first-device bootstrap via high-entropy setup code (no expiry, rate-limited), approval flow for subsequent devices, rename, token issuance, device list + revocation, localhost bypass

## 9. Suggested repo structure

```
argos/
├── cmd/
│   └── argos/              # main.go — entrypoint, embeds the built frontend
├── internal/
│   ├── api/                # HTTP handlers
│   ├── auth/                # device pairing, token issuance/validation
│   ├── budget/              # pure budgeting engine (rollover, overspending, availability)
│   ├── db/                  # SQLite access layer, migrations
│   ├── sync/                # /sync endpoint logic, conflict resolution
│   ├── tray/                # desktop mode: system tray integration
│   └── service/             # headless mode: service install/uninstall per OS
├── web/
│   ├── src/
│   └── locales/             # i18n locale files, English as default
├── go.mod
└── README.md
```

## 10. License

MIT.
