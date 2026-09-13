import { describe, expect, it } from "vitest";

import { activity, available, rollupCategory } from "./envelope";
import type { BudgetEntry, Transaction } from "./types";

function makeTransaction(overrides: Partial<Transaction> = {}): Transaction {
  return {
    id: crypto.randomUUID(),
    hlc_physical: 0,
    hlc_counter: 0,
    hlc_node_id: "node-1",
    deleted_at: null,
    account_id: "acc-1",
    category_id: "cat-1",
    payee_id: null,
    parent_id: null,
    date: "2026-03-01",
    amount: 0,
    cleared: false,
    notes: "",
    transfer_id: null,
    ...overrides,
  };
}

function makeBudgetEntry(overrides: Partial<BudgetEntry> = {}): BudgetEntry {
  return {
    id: crypto.randomUUID(),
    hlc_physical: 0,
    hlc_counter: 0,
    hlc_node_id: "node-1",
    deleted_at: null,
    category_id: "cat-1",
    month: "2026-03",
    budgeted: 0,
    ...overrides,
  };
}

describe("activity", () => {
  it("sums transaction amounts for the given category and month", () => {
    const total = activity(
      [
        makeTransaction({ date: "2026-03-05", amount: -1000 }),
        makeTransaction({ date: "2026-03-20", amount: -500 }),
      ],
      "cat-1",
      "2026-03",
    );
    expect(total).toBe(-1500);
  });

  it("is zero with no matching transactions", () => {
    expect(activity([], "cat-1", "2026-03")).toBe(0);
  });

  it("ignores other categories, other months, and soft-deleted rows", () => {
    const total = activity(
      [
        makeTransaction({ category_id: "cat-2", date: "2026-03-05", amount: -1000 }),
        makeTransaction({ category_id: "cat-1", date: "2026-04-05", amount: -1000 }),
        makeTransaction({
          category_id: "cat-1",
          date: "2026-03-05",
          amount: -1000,
          deleted_at: Date.now(),
        }),
      ],
      "cat-1",
      "2026-03",
    );
    expect(total).toBe(0);
  });
});

// Mirrors internal/budget/envelope_test.go's TestAvailable_* cases from
// Milestone 8 exactly (same inputs, same expected outputs), so the TS
// engine's rollover rule stays provably in sync with the Go one.
describe("available", () => {
  it("carries a positive rollover forward (TestAvailable_PositiveRollover)", () => {
    // Last month ended with 5000 left over; this month budgets 2000 more
    // and has 1000 of spending (activity is negative).
    expect(available(5000, 2000, -1000)).toBe(6000);
  });

  it("resets a negative rollover to zero instead of carrying it forward (TestAvailable_ResetsAfterOverspending)", () => {
    // Last month overspent by 3000; the negative must NOT carry forward.
    expect(available(-3000, 1000, -500)).toBe(500); // 0 (reset) + 1000 - 500
  });

  it("treats a category's first budgeted month as having no prior rollover (TestAvailable_FirstMonthBudgeted)", () => {
    // No prior month at all: caller passes previousAvailable as 0.
    expect(available(0, 1000, -200)).toBe(800);
  });
});

describe("rollupCategory", () => {
  it("computes budgeted/activity/available for a single month with no history", () => {
    const entries = [makeBudgetEntry({ month: "2026-03", budgeted: 5000 })];
    const transactions = [makeTransaction({ date: "2026-03-10", amount: -2000 })];

    expect(rollupCategory(entries, transactions, "cat-1", "2026-03")).toEqual({
      budgeted: 5000,
      activity: -2000,
      available: 3000,
    });
  });

  it("carries a positive available forward across months with no budget entry of their own", () => {
    const entries = [makeBudgetEntry({ month: "2026-01", budgeted: 10000 })];
    const transactions = [makeTransaction({ date: "2026-01-15", amount: -4000 })];

    // February and March have no budget entry and no activity at all; the
    // 6000 left over from January should still be sitting there in March.
    expect(rollupCategory(entries, transactions, "cat-1", "2026-03")).toEqual({
      budgeted: 0,
      activity: 0,
      available: 6000,
    });
  });

  it("resets an overspent month's rollover to zero going into the next month", () => {
    const entries = [
      makeBudgetEntry({ month: "2026-01", budgeted: 1000 }),
      makeBudgetEntry({ month: "2026-02", budgeted: 500 }),
    ];
    const transactions = [
      makeTransaction({ date: "2026-01-20", amount: -3000 }), // overspends January by 2000
    ];

    // January: 0 + 1000 - 3000 = -2000 (overspent)
    // February: reset to 0, then + 500 budgeted + 0 activity = 500
    expect(rollupCategory(entries, transactions, "cat-1", "2026-02")).toEqual({
      budgeted: 500,
      activity: 0,
      available: 500,
    });
  });

  it("ignores another category's entries and transactions", () => {
    const entries = [
      makeBudgetEntry({ category_id: "cat-1", month: "2026-03", budgeted: 1000 }),
      makeBudgetEntry({ category_id: "cat-2", month: "2026-03", budgeted: 9000 }),
    ];
    const transactions = [
      makeTransaction({ category_id: "cat-1", date: "2026-03-05", amount: -100 }),
      makeTransaction({ category_id: "cat-2", date: "2026-03-05", amount: -9999 }),
    ];

    expect(rollupCategory(entries, transactions, "cat-1", "2026-03")).toEqual({
      budgeted: 1000,
      activity: -100,
      available: 900,
    });
  });

  it("ignores budget entries and transactions after the target month", () => {
    const entries = [
      makeBudgetEntry({ month: "2026-03", budgeted: 1000 }),
      makeBudgetEntry({ month: "2026-04", budgeted: 999_999 }),
    ];
    const transactions = [makeTransaction({ date: "2026-04-01", amount: -999_999 })];

    expect(rollupCategory(entries, transactions, "cat-1", "2026-03")).toEqual({
      budgeted: 1000,
      activity: 0,
      available: 1000,
    });
  });

  it("ignores soft-deleted budget entries", () => {
    const entries = [
      makeBudgetEntry({ month: "2026-03", budgeted: 1000, deleted_at: Date.now() }),
    ];

    expect(rollupCategory(entries, [], "cat-1", "2026-03")).toEqual({
      budgeted: 0,
      activity: 0,
      available: 0,
    });
  });
});
