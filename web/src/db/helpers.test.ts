import { beforeEach, describe, expect, it } from "vitest";

import { db } from "./db";
import {
  accounts,
  budgetEntries,
  categories,
  categoryGroups,
  outbox,
  payees,
  transactions,
} from "./helpers";
import type {
  Account,
  BudgetEntry,
  Category,
  CategoryGroup,
  Payee,
  Transaction,
} from "./types";

// Every test runs against the same singleton `db` (fake-indexeddb backed),
// so clear all tables first to keep tests independent of each other.
beforeEach(async () => {
  await Promise.all(
    db.tables.map((table) => table.clear()),
  );
});

const baseSync = {
  hlc_physical: 1_700_000_000_000,
  hlc_counter: 0,
  hlc_node_id: "node-1",
  server_version: undefined,
  deleted_at: null,
};

function makeAccount(overrides: Partial<Account> = {}): Account {
  return {
    id: crypto.randomUUID(),
    ...baseSync,
    name: "Checking",
    type: "checking",
    on_budget: true,
    closed: false,
    currency: "USD",
    notes: null,
    ...overrides,
  };
}

describe("accounts (representative of the per-table CRUD shape)", () => {
  it("creates and reads a row back", async () => {
    const account = makeAccount();

    await accounts.create(account);
    const fetched = await accounts.get(account.id);

    expect(fetched).toEqual(account);
  });

  it("lists all rows", async () => {
    await accounts.create(makeAccount({ name: "Checking" }));
    await accounts.create(makeAccount({ name: "Savings" }));

    const all = await accounts.list();

    expect(all.map((a) => a.name).sort()).toEqual(["Checking", "Savings"]);
  });

  it("updates a row in place", async () => {
    const account = makeAccount({ closed: false });
    await accounts.create(account);

    await accounts.update(account.id, { closed: true });
    const fetched = await accounts.get(account.id);

    expect(fetched?.closed).toBe(true);
  });

  it("soft-deletes via update, never removing the row (CLAUDE.md: no hard deletes)", async () => {
    const account = makeAccount();
    await accounts.create(account);

    await accounts.update(account.id, { deleted_at: Date.now() });
    const fetched = await accounts.get(account.id);

    expect(fetched).toBeDefined();
    expect(fetched?.deleted_at).not.toBeNull();
  });
});

describe("remaining syncable tables expose the same create/get/list/update shape", () => {
  it("category_groups", async () => {
    const row: CategoryGroup = {
      id: crypto.randomUUID(),
      ...baseSync,
      name: "Bills",
      is_income: false,
      sort_order: 0,
    };
    await categoryGroups.create(row);
    expect(await categoryGroups.get(row.id)).toEqual(row);
  });

  it("categories", async () => {
    const row: Category = {
      id: crypto.randomUUID(),
      ...baseSync,
      group_id: crypto.randomUUID(),
      name: "Groceries",
      hidden: false,
      sort_order: 0,
      notes: null,
    };
    await categories.create(row);
    await categories.update(row.id, { hidden: true });
    expect((await categories.get(row.id))?.hidden).toBe(true);
  });

  it("payees", async () => {
    const row: Payee = { id: crypto.randomUUID(), ...baseSync, name: "Landlord" };
    await payees.create(row);
    expect(await payees.list()).toEqual([row]);
  });

  it("transactions", async () => {
    const row: Transaction = {
      id: crypto.randomUUID(),
      ...baseSync,
      account_id: crypto.randomUUID(),
      category_id: null,
      payee_id: null,
      parent_id: null,
      date: "2026-03-01",
      amount: -500,
      cleared: false,
      notes: "",
      transfer_id: null,
    };
    await transactions.create(row);
    await transactions.update(row.id, { cleared: true });
    expect((await transactions.get(row.id))?.cleared).toBe(true);
  });

  it("budget_entries", async () => {
    const row: BudgetEntry = {
      id: crypto.randomUUID(),
      ...baseSync,
      category_id: crypto.randomUUID(),
      month: "2026-03",
      budgeted: 10000,
    };
    await budgetEntries.create(row);
    expect(await budgetEntries.get(row.id)).toEqual(row);
  });
});

describe("outbox (§2.4 mutation shape + local sync bookkeeping)", () => {
  it("enqueues an entry as unsynced by default", async () => {
    const account = makeAccount();

    const id = await outbox.enqueue({
      table: "accounts",
      op: "upsert",
      group_id: null,
      row: account,
    });
    const entry = await db.outbox.get(id);

    expect(entry?.synced).toBe(false);
    expect(entry?.table).toBe("accounts");
    expect(entry?.row).toEqual(account);
  });

  it("listUnsynced returns only unsynced entries, oldest first", async () => {
    const a = makeAccount({ name: "A" });
    const b = makeAccount({ name: "B" });

    await outbox.enqueue({ table: "accounts", op: "upsert", group_id: null, row: a });
    const secondId = await outbox.enqueue({ table: "accounts", op: "upsert", group_id: null, row: b });
    await outbox.markSynced(secondId);

    const unsynced = await outbox.listUnsynced();

    expect(unsynced).toHaveLength(1);
    expect((unsynced[0].row as Account).name).toBe("A");
  });

  it("markSynced flips the flag without touching the row", async () => {
    const account = makeAccount();
    const id = await outbox.enqueue({ table: "accounts", op: "upsert", group_id: null, row: account });

    await outbox.markSynced(id);
    const entry = await db.outbox.get(id);

    expect(entry?.synced).toBe(true);
    expect(entry?.row).toEqual(account);
  });

  it("groups mutations sharing a group_id (e.g. the two sides of a transfer, §2.4)", async () => {
    const groupId = crypto.randomUUID();
    const left = makeAccount({ name: "From" });
    const right = makeAccount({ name: "To" });

    await outbox.enqueue({ table: "accounts", op: "upsert", group_id: groupId, row: left });
    await outbox.enqueue({ table: "accounts", op: "upsert", group_id: groupId, row: right });

    const grouped = await db.outbox.where("group_id").equals(groupId).toArray();

    expect(grouped).toHaveLength(2);
  });
});
