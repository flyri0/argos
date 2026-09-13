import type { Table, UpdateSpec } from "dexie";

import { db } from "./db";
import type {
  Account,
  BudgetEntry,
  Category,
  CategoryGroup,
  OutboxEntry,
  Payee,
  SyncMeta,
  Transaction,
} from "./types";

// Every syncable table needs the same three operations, so this factory
// avoids repeating identical create/get/list/update bodies six times.
// There's deliberately no `remove` — CLAUDE.md forbids hard deletes; a
// delete is an update that sets `deleted_at` (§5.1).
function tableHelpers<T extends SyncMeta>(table: Table<T, string>) {
  return {
    create: (row: T) => table.add(row),
    get: (id: string) => table.get(id),
    list: () => table.toArray(),
    update: (id: string, changes: UpdateSpec<T>) => table.update(id, changes),
  };
}

export const accounts = tableHelpers<Account>(db.accounts);
export const categoryGroups = tableHelpers<CategoryGroup>(db.category_groups);
export const categories = tableHelpers<Category>(db.categories);
export const payees = tableHelpers<Payee>(db.payees);
export const transactions = tableHelpers<Transaction>(db.transactions);
export const budgetEntries = tableHelpers<BudgetEntry>(db.budget_entries);

// The outbox has a different shape (local auto-incrementing key, no HLC of
// its own) so it gets its own small helper set instead of the factory.
export const outbox = {
  enqueue: (entry: Omit<OutboxEntry, "id" | "synced">) =>
    db.outbox.add({ ...entry, synced: false }),
  listUnsynced: () => db.outbox.filter((entry) => !entry.synced).sortBy("id"),
  markSynced: (id: number) => db.outbox.update(id, { synced: true }),
};
