import Dexie, { type Table } from "dexie";

import type {
  Account,
  BudgetEntry,
  Category,
  CategoryGroup,
  OutboxEntry,
  Payee,
  Transaction,
} from "./types";

// The local IndexedDB replica (§2.2). Each syncable table mirrors its
// server counterpart (§5.2) one-to-one, plus the outbox (§2.4) queuing
// mutations for the next /sync push. `server_meta` and `devices` are
// explicitly excluded (§5.2: "Not synced to clients").
//
// Every row's `id` is client-generated (§5.1) rather than auto-assigned, so
// each table is typed with a plain string key and no separate "insert"
// shape — unlike `outbox`, whose key is a local auto-incrementing number.
export class ArgosDB extends Dexie {
  accounts!: Table<Account, string, Account>;
  category_groups!: Table<CategoryGroup, string, CategoryGroup>;
  categories!: Table<Category, string, Category>;
  payees!: Table<Payee, string, Payee>;
  transactions!: Table<Transaction, string, Transaction>;
  budget_entries!: Table<BudgetEntry, string, BudgetEntry>;
  outbox!: Table<OutboxEntry, number, OutboxEntry>;

  constructor(name = "argos") {
    super(name);
    this.version(1).stores({
      accounts: "&id, deleted_at, server_version",
      category_groups: "&id, deleted_at, server_version",
      categories: "&id, group_id, deleted_at, server_version",
      payees: "&id, deleted_at, server_version",
      transactions:
        "&id, account_id, category_id, payee_id, transfer_id, deleted_at, server_version",
      // budget_entries has a unique (category_id, month) constraint on the
      // server (§5.2); [category_id+month] mirrors that for local lookups.
      budget_entries:
        "&id, category_id, month, [category_id+month], deleted_at, server_version",
      // `synced` isn't indexed: IndexedDB doesn't accept booleans as valid
      // key values, so listUnsynced() filters instead of querying an index.
      outbox: "++id, table, group_id",
    });
  }
}

export const db = new ArgosDB();
