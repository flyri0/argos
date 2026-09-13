import { db } from "../db/db";
import { outbox } from "../db/helpers";
import { observeHlc } from "../db/hlc";
import type {
  Account,
  BudgetEntry,
  Category,
  CategoryGroup,
  MutationTable,
  OutboxEntry,
  Payee,
  SyncMeta,
  Transaction,
} from "../db/types";
import { getCursor, getSyncId, setCursor, setSyncId } from "./cursor";
import { setSchemaMismatch } from "./status";

// The schema_version this build of the client knows how to sync with
// (§2.3). Must be bumped in the same commit that bumps
// internal/db/server_meta.go's initialSchemaVersion for a breaking schema
// change, per CLAUDE.md's "any new error code is added in the same commit"
// spirit extended to this other living contract between client and server.
const EXPECTED_SCHEMA_VERSION = 1;

interface SyncMutationWire {
  table: MutationTable;
  op: OutboxEntry["op"];
  group_id: string | null;
  row: SyncMeta;
}

interface SyncResultWire {
  table: string;
  id: string;
  status: "applied" | "rejected_stale" | "rejected_invalid";
  error?: { code: string; message: string };
}

interface SyncChangeWire {
  table: MutationTable;
  row: SyncMeta;
}

interface SyncResponseWire {
  server_version: number;
  sync_id: string;
  schema_version: number;
  results: SyncResultWire[];
  changes: SyncChangeWire[];
}

// Splits a flat, ordered outbox listing into the same atomic units the
// server groups mutations into (§2.3/§2.4): a null group_id is its own
// unit; entries sharing a non-null group_id join the unit created at that
// group_id's first occurrence. Mirrors internal/api/sync.go's
// groupMutations exactly, so results[i] lines up with units[i].
function groupOutboxEntries(entries: OutboxEntry[]): OutboxEntry[][] {
  const units: OutboxEntry[][] = [];
  const unitIndexByGroup = new Map<string, number>();

  for (const entry of entries) {
    if (entry.group_id === null) {
      units.push([entry]);
      continue;
    }
    const existingIndex = unitIndexByGroup.get(entry.group_id);
    if (existingIndex !== undefined) {
      units[existingIndex].push(entry);
      continue;
    }
    unitIndexByGroup.set(entry.group_id, units.length);
    units.push([entry]);
  }

  return units;
}

// Upserts one pulled row into its matching local table (§2.3: the client
// applies every row the server sends back to its IndexedDB replica). A
// plain `put` regardless of whether the row is new or already exists
// locally — the row carries the server's full current state either way, the
// same "every write replaces the row's full current state" rule §2.4 uses
// for outgoing mutations.
async function applyChange(change: SyncChangeWire): Promise<void> {
  observeHlc(change.row);

  switch (change.table) {
    case "accounts":
      await db.accounts.put(change.row as Account);
      return;
    case "category_groups":
      await db.category_groups.put(change.row as CategoryGroup);
      return;
    case "categories":
      await db.categories.put(change.row as Category);
      return;
    case "payees":
      await db.payees.put(change.row as Payee);
      return;
    case "transactions":
      await db.transactions.put(change.row as Transaction);
      return;
    case "budget_entries":
      await db.budget_entries.put(change.row as BudgetEntry);
      return;
  }
}

// Marks every outbox entry in `units` synced according to its paired
// result: "applied" and "rejected_stale" are both terminal outcomes (§2.3 —
// losing a last-write-wins conflict is expected behavior, not something to
// retry), so those entries are done. "rejected_invalid" entries are left
// unsynced on purpose and are resent on the next sync cycle — the server
// applies mutations independently (§2.3), so a stuck invalid entry can
// never block any other entry from syncing, and silently dropping it would
// risk losing a real local edit.
async function markOutboxResults(
  units: OutboxEntry[][],
  results: SyncResultWire[],
): Promise<void> {
  for (let i = 0; i < units.length; i++) {
    const result = results[i];
    if (!result || result.status === "rejected_invalid") continue;

    for (const entry of units[i]) {
      if (entry.id !== undefined) await outbox.markSynced(entry.id);
    }
  }
}

let syncing = false;

// One drain-push-pull cycle of the sync worker (§2.2-§2.4): pushes every
// unsynced outbox entry to POST /sync, applies the pulled "changes" to the
// local Dexie replica, advances the "since" cursor, and marks pushed
// mutations synced per their reported result. A schema_version mismatch
// (§2.3) stops here without touching the outbox, changes, or cursor from
// that response — the rest of the app keeps working against the existing
// local data regardless.
export async function runSync(): Promise<void> {
  if (syncing) return;
  if (typeof navigator !== "undefined" && navigator.onLine === false) return;

  syncing = true;
  let needsResync = false;
  try {
    const pending = await outbox.listUnsynced();
    const units = groupOutboxEntries(pending);
    const mutations: SyncMutationWire[] = pending.map((entry) => ({
      table: entry.table,
      op: entry.op,
      group_id: entry.group_id,
      row: entry.row,
    }));

    let response: Response;
    try {
      response = await fetch("/sync", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ since: getCursor(), mutations }),
      });
    } catch {
      return; // offline or unreachable — retried on the next trigger
    }
    if (!response.ok) return;

    const body = (await response.json()) as SyncResponseWire;

    if (body.schema_version !== EXPECTED_SCHEMA_VERSION) {
      setSchemaMismatch(true);
      return;
    }
    setSchemaMismatch(false);

    // §2.3/§5.2: sync_id only changes on an explicit server-side reset (e.g.
    // a restore from backup), meaning this response's `changes` were
    // computed against a `since` cursor from a history we no longer share.
    // Unlike a schema mismatch, this is fully recoverable on our own: store
    // the new sync_id, reset the cursor to re-download from scratch, and
    // retry immediately rather than merging or waiting for a UI action.
    const storedSyncId = getSyncId();
    if (storedSyncId !== null && storedSyncId !== body.sync_id) {
      // Deliberately not a `return` here: a `return` inside this `try`
      // would still run `finally` below, but the function would then exit
      // immediately afterward — never reaching `if (needsResync)` past the
      // end of the try/finally. Falling through to the end of the try
      // block instead is what lets that retry actually fire.
      setSyncId(body.sync_id);
      setCursor(0);
      needsResync = true;
    } else {
      if (storedSyncId === null) {
        setSyncId(body.sync_id);
      }

      for (const change of body.changes) {
        await applyChange(change);
      }
      await markOutboxResults(units, body.results);
      setCursor(body.server_version);
    }
  } finally {
    syncing = false;
  }
  if (needsResync) await runSync();
}
