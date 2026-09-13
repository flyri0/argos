import type { Table, UpdateSpec } from "dexie";

import { db } from "./db";
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
} from "./types";

// §2.2: "every user action ... is also appended to a local outbox table."
// A soft delete (`deleted_at` set) is enqueued as its own `"delete"` op with
// just the minimal delete shape §2.4 defines; everything else is an
// `"upsert"` carrying the row's full current state. This mirrors exactly
// what internal/api/sync.go's applyDelete/applyUpsert expect on the wire, so
// every write path (not just tableHelpers) funnels through it.
export async function enqueueRowMutation(
  mutationTable: MutationTable,
  row: SyncMeta,
  groupId: string | null = null,
): Promise<void> {
  if (row.deleted_at !== null) {
    await outbox.enqueue({
      table: mutationTable,
      op: "delete",
      group_id: groupId,
      row: {
        id: row.id,
        deleted_at: row.deleted_at,
        hlc_physical: row.hlc_physical,
        hlc_counter: row.hlc_counter,
        hlc_node_id: row.hlc_node_id,
      },
    });
    return;
  }
  await outbox.enqueue({ table: mutationTable, op: "upsert", group_id: groupId, row });
}

// Every syncable table needs the same three operations, so this factory
// avoids repeating identical create/get/list/update bodies six times. Each
// create/update is wrapped with its matching outbox entry in the same Dexie
// transaction, so a local write can never leave the outbox out of sync with
// the table it describes. There's deliberately no `remove` — CLAUDE.md
// forbids hard deletes; a delete is an update that sets `deleted_at` (§5.1).
function tableHelpers<T extends SyncMeta>(mutationTable: MutationTable, table: Table<T, string>) {
  return {
    create: (row: T) =>
      db.transaction("rw", table, db.outbox, async () => {
        await table.add(row);
        await enqueueRowMutation(mutationTable, row);
      }),
    get: (id: string) => table.get(id),
    list: () => table.toArray(),
    update: (id: string, changes: UpdateSpec<T>) =>
      db.transaction("rw", table, db.outbox, async () => {
        await table.update(id, changes);
        const updated = await table.get(id);
        if (updated) await enqueueRowMutation(mutationTable, updated);
      }),
  };
}

export const accounts = tableHelpers<Account>("accounts", db.accounts);
export const categoryGroups = tableHelpers<CategoryGroup>("category_groups", db.category_groups);
export const categories = tableHelpers<Category>("categories", db.categories);
export const payees = tableHelpers<Payee>("payees", db.payees);
export const transactions = tableHelpers<Transaction>("transactions", db.transactions);
export const budgetEntries = tableHelpers<BudgetEntry>("budget_entries", db.budget_entries);

// The outbox has a different shape (local auto-incrementing key, no HLC of
// its own) so it gets its own small helper set instead of the factory.
export const outbox = {
  enqueue: (entry: Omit<OutboxEntry, "id" | "synced">) =>
    db.outbox.add({ ...entry, synced: false }),
  listUnsynced: () => db.outbox.filter((entry) => !entry.synced).sortBy("id"),
  markSynced: (id: number) => db.outbox.update(id, { synced: true }),
};
