# Argos — Agent Instructions

Argos: local-first, self-hosted envelope-budgeting app. Single Go binary
serving an HTTP API and an embedded React PWA, syncing to IndexedDB clients
over LAN or localhost.

**Before any task**: read `project_spec.md` fully. It is the single source
of truth for architecture, data model, API surface, and scope. Don't add
endpoints, columns, or behavior not described there — if something is
ambiguous or missing, stop and ask instead of guessing a default.

## Stack

- Server: Go, `net/http` or `chi`, `modernc.org/sqlite` (pure Go, no CGO),
  frontend embedded via `embed.FS`.
- Client: React, Dexie.js (IndexedDB), `react-i18next` (English is the only
  locale for MVP, but the i18n structure must exist from the start).
- Sync: Hybrid Logical Clock (`hlc_physical`, `hlc_counter`, `hlc_node_id`)
  + `server_version` cursor. Never use raw wall-clock timestamps for
  conflict resolution.

## Non-negotiable rules

- No hard deletes. Every delete sets `deleted_at` (§5.1).
- Account balance is never a stored column — always computed by summing
  transactions (§5.3).
- Server-facing strings are English-only, machine-readable error codes
  (§4). Never return pre-translated text from the server.
- Category/payee deletion requires `reassign_to` when in use (§5.4) —
  never leave a dangling reference.
- Conventional Commits: `feat:`, `fix:`, `chore:`, `docs:`, `refactor:`,
  `test:`. One commit per completed milestone, not per file.
- Comments explain *why*, not *what*, only where reasoning isn't obvious
  from the code. Don't restate logic in prose. English only.
- Implement only what the current prompt asks, even if a "next step"
  seems obvious — it will come as its own prompt.
- If `project_spec.md` doesn't answer a question, stop and ask. Never
  invent a default silently.

## Testing

The budgeting engine (rollover, overspending, `available`, §5.3) and HLC
comparison (§2.3) are the most error-prone parts and require unit tests.

## Repo layout

See `project_spec.md` §9. Code in `internal/budget` has zero dependency
on `internal/api` or the DB layer — pure functions, testable in isolation.
