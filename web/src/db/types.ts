// Row shapes mirror the server tables in project_spec.md §5.2. Column
// names and nullability match the server exactly so a synced row needs no
// translation between the wire format (§2.4) and the local replica.

// Sync metadata present on every syncable table (§5.1). `server_version` is
// absent (undefined) on a row created locally that hasn't been acknowledged
// by the server yet.
export interface SyncMeta {
  id: string;
  hlc_physical: number;
  hlc_counter: number;
  hlc_node_id: string;
  server_version?: number;
  deleted_at: number | null;
}

export type AccountType =
  | "checking"
  | "savings"
  | "credit"
  | "cash"
  | "investment"
  | "other";

export interface Account extends SyncMeta {
  name: string;
  type: AccountType;
  on_budget: boolean;
  closed: boolean;
  currency: string;
  notes: string | null;
}

export interface CategoryGroup extends SyncMeta {
  name: string;
  is_income: boolean;
  sort_order: number;
}

export interface Category extends SyncMeta {
  group_id: string;
  name: string;
  hidden: boolean;
  sort_order: number;
  notes: string | null;
}

export interface Payee extends SyncMeta {
  name: string;
}

export interface Transaction extends SyncMeta {
  account_id: string;
  category_id: string | null;
  payee_id: string | null;
  parent_id: string | null;
  date: string;
  amount: number;
  cleared: boolean;
  notes: string;
  transfer_id: string | null;
}

export interface BudgetEntry extends SyncMeta {
  category_id: string;
  month: string;
  budgeted: number;
}

// §2.4 mutation entry shape, plus the local-only bookkeeping fields needed
// to drive the outbox drain (an auto-incrementing key so entries apply in
// the order they were queued, and a flag for what's already been pushed).
export type MutationTable =
  | "accounts"
  | "category_groups"
  | "categories"
  | "payees"
  | "transactions"
  | "budget_entries";

export type MutationOp = "upsert" | "delete";

export interface OutboxEntry {
  id?: number;
  table: MutationTable;
  op: MutationOp;
  group_id: string | null;
  row: SyncMeta;
  synced: boolean;
}
