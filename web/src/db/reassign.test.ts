import { beforeEach, describe, expect, it } from "vitest";

import { budgetEntries, categories, payees, transactions } from "./helpers";
import { db } from "./db";
import { isCategoryInUse, isPayeeInUse, reassignCategory, reassignPayee } from "./reassign";
import type { BudgetEntry, Category, Payee, Transaction } from "./types";

beforeEach(async () => {
  await Promise.all(db.tables.map((table) => table.clear()));
});

const baseSync = {
  hlc_physical: 1_700_000_000_000,
  hlc_counter: 0,
  hlc_node_id: "node-1",
  server_version: undefined,
  deleted_at: null,
};

function makeCategory(overrides: Partial<Category> = {}): Category {
  return {
    id: crypto.randomUUID(),
    ...baseSync,
    group_id: crypto.randomUUID(),
    name: "Groceries",
    hidden: false,
    sort_order: 0,
    notes: null,
    ...overrides,
  };
}

function makePayee(overrides: Partial<Payee> = {}): Payee {
  return { id: crypto.randomUUID(), ...baseSync, name: "Landlord", ...overrides };
}

function makeTransaction(overrides: Partial<Transaction> = {}): Transaction {
  return {
    id: crypto.randomUUID(),
    ...baseSync,
    account_id: "acc-1",
    category_id: null,
    payee_id: null,
    parent_id: null,
    date: "2026-03-01",
    amount: -500,
    cleared: false,
    notes: "",
    transfer_id: null,
    ...overrides,
  };
}

function makeBudgetEntry(overrides: Partial<BudgetEntry> = {}): BudgetEntry {
  return {
    id: crypto.randomUUID(),
    ...baseSync,
    category_id: "cat-1",
    month: "2026-03",
    budgeted: 0,
    ...overrides,
  };
}

describe("isCategoryInUse", () => {
  it("is false with no transactions or budget entries", () => {
    expect(isCategoryInUse("cat-1", [], [])).toBe(false);
  });

  it("is true when a non-deleted transaction references it", () => {
    const t = makeTransaction({ category_id: "cat-1" });
    expect(isCategoryInUse("cat-1", [t], [])).toBe(true);
  });

  it("ignores soft-deleted transactions", () => {
    const t = makeTransaction({ category_id: "cat-1", deleted_at: Date.now() });
    expect(isCategoryInUse("cat-1", [t], [])).toBe(false);
  });

  it("is true when a budget_entries row exists regardless of amount (§5.4 coarse check)", () => {
    const entry = makeBudgetEntry({ category_id: "cat-1", budgeted: 0 });
    expect(isCategoryInUse("cat-1", [], [entry])).toBe(true);
  });

  it("ignores soft-deleted budget_entries rows", () => {
    const entry = makeBudgetEntry({ category_id: "cat-1", deleted_at: Date.now() });
    expect(isCategoryInUse("cat-1", [], [entry])).toBe(false);
  });
});

describe("isPayeeInUse", () => {
  it("is true only when a non-deleted transaction references it", () => {
    expect(isPayeeInUse("payee-1", [])).toBe(false);
    expect(
      isPayeeInUse("payee-1", [makeTransaction({ payee_id: "payee-1" })]),
    ).toBe(true);
    expect(
      isPayeeInUse("payee-1", [
        makeTransaction({ payee_id: "payee-1", deleted_at: Date.now() }),
      ]),
    ).toBe(false);
  });
});

describe("reassignCategory", () => {
  it("moves non-deleted transactions to the target and soft-deletes the source", async () => {
    const source = makeCategory({ name: "Old" });
    const target = makeCategory({ name: "New" });
    await categories.create(source);
    await categories.create(target);

    const live = makeTransaction({ category_id: source.id });
    const deleted = makeTransaction({
      category_id: source.id,
      deleted_at: Date.now(),
    });
    await transactions.create(live);
    await transactions.create(deleted);

    await reassignCategory(source.id, target.id);

    expect((await transactions.get(live.id))?.category_id).toBe(target.id);
    // a tombstone doesn't need reassigning; it's left pointing at the
    // now-deleted source rather than rewritten
    expect((await transactions.get(deleted.id))?.category_id).toBe(source.id);
    expect((await categories.get(source.id))?.deleted_at).not.toBeNull();
  });

  it("sums budgeted amounts when the target already has a row for that month", async () => {
    const source = makeCategory({ name: "Old" });
    const target = makeCategory({ name: "New" });
    await categories.create(source);
    await categories.create(target);

    const sourceEntry = makeBudgetEntry({
      category_id: source.id,
      month: "2026-03",
      budgeted: 5000,
    });
    const targetEntry = makeBudgetEntry({
      category_id: target.id,
      month: "2026-03",
      budgeted: 2000,
    });
    await budgetEntries.create(sourceEntry);
    await budgetEntries.create(targetEntry);

    await reassignCategory(source.id, target.id);

    const mergedTarget = await budgetEntries.get(targetEntry.id);
    expect(mergedTarget?.budgeted).toBe(7000);

    const removedSource = await budgetEntries.get(sourceEntry.id);
    expect(removedSource?.deleted_at).not.toBeNull();
  });

  it("re-points the source row when the target has no row for that month", async () => {
    const source = makeCategory({ name: "Old" });
    const target = makeCategory({ name: "New" });
    await categories.create(source);
    await categories.create(target);

    const sourceEntry = makeBudgetEntry({
      category_id: source.id,
      month: "2026-04",
      budgeted: 1500,
    });
    await budgetEntries.create(sourceEntry);

    await reassignCategory(source.id, target.id);

    const moved = await budgetEntries.get(sourceEntry.id);
    expect(moved?.category_id).toBe(target.id);
    expect(moved?.budgeted).toBe(1500);
    expect(moved?.deleted_at).toBeNull();
  });

  it("handles multiple months independently", async () => {
    const source = makeCategory({ name: "Old" });
    const target = makeCategory({ name: "New" });
    await categories.create(source);
    await categories.create(target);

    await budgetEntries.create(
      makeBudgetEntry({ category_id: source.id, month: "2026-01", budgeted: 100 }),
    );
    await budgetEntries.create(
      makeBudgetEntry({ category_id: target.id, month: "2026-01", budgeted: 900 }),
    );
    await budgetEntries.create(
      makeBudgetEntry({ category_id: source.id, month: "2026-02", budgeted: 300 }),
    );

    await reassignCategory(source.id, target.id);

    const all = await budgetEntries.list();
    const targetJan = all.find((e) => e.category_id === target.id && e.month === "2026-01");
    const targetFeb = all.find((e) => e.category_id === target.id && e.month === "2026-02");
    expect(targetJan?.budgeted).toBe(1000);
    expect(targetFeb?.budgeted).toBe(300);
  });
});

describe("reassignPayee", () => {
  it("moves non-deleted transactions to the target and soft-deletes the source", async () => {
    const source = makePayee({ name: "Old Landlord" });
    const target = makePayee({ name: "New Landlord" });
    await payees.create(source);
    await payees.create(target);

    const live = makeTransaction({ payee_id: source.id });
    const deleted = makeTransaction({ payee_id: source.id, deleted_at: Date.now() });
    await transactions.create(live);
    await transactions.create(deleted);

    await reassignPayee(source.id, target.id);

    expect((await transactions.get(live.id))?.payee_id).toBe(target.id);
    expect((await transactions.get(deleted.id))?.payee_id).toBe(source.id);
    expect((await payees.get(source.id))?.deleted_at).not.toBeNull();
  });
});
