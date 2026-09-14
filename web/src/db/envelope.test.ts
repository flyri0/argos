import { describe, expect, it } from "vitest";

import {
  activity,
  available,
  balanceThrough,
  onBudgetAccountIds,
  rollupCategory,
  toBudget,
} from "./envelope";
import type { Account, BudgetEntry, Category, CategoryGroup, Transaction } from "./types";

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

function makeAccount(overrides: Partial<Account> = {}): Account {
  return {
    id: crypto.randomUUID(),
    hlc_physical: 0,
    hlc_counter: 0,
    hlc_node_id: "node-1",
    deleted_at: null,
    name: "Checking",
    type: "checking",
    on_budget: true,
    closed: false,
    currency: "USD",
    notes: null,
    ...overrides,
  };
}

function makeGroup(overrides: Partial<CategoryGroup> = {}): CategoryGroup {
  return {
    id: crypto.randomUUID(),
    hlc_physical: 0,
    hlc_counter: 0,
    hlc_node_id: "node-1",
    deleted_at: null,
    name: "Bills",
    is_income: false,
    sort_order: 0,
    ...overrides,
  };
}

function makeCategory(overrides: Partial<Category> = {}): Category {
  return {
    id: crypto.randomUUID(),
    hlc_physical: 0,
    hlc_counter: 0,
    hlc_node_id: "node-1",
    deleted_at: null,
    group_id: "bills",
    name: "Groceries",
    hidden: false,
    sort_order: 0,
    notes: null,
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

const onBudget = new Set(["acc-1"]);

describe("activity", () => {
  it("sums transaction amounts for the given category and month", () => {
    const total = activity(
      [
        makeTransaction({ date: "2026-03-05", amount: -1000 }),
        makeTransaction({ date: "2026-03-20", amount: -500 }),
      ],
      onBudget,
      "cat-1",
      "2026-03",
    );
    expect(total).toBe(-1500);
  });

  it("is zero with no matching transactions", () => {
    expect(activity([], onBudget, "cat-1", "2026-03")).toBe(0);
  });

  it("ignores other categories, other months, off-budget accounts, and soft-deleted rows", () => {
    const total = activity(
      [
        makeTransaction({ category_id: "cat-2", date: "2026-03-05", amount: -1000 }),
        makeTransaction({ category_id: "cat-1", date: "2026-04-05", amount: -1000 }),
        makeTransaction({ account_id: "acc-off", date: "2026-03-05", amount: -1000 }),
        makeTransaction({
          category_id: "cat-1",
          date: "2026-03-05",
          amount: -1000,
          deleted_at: Date.now(),
        }),
      ],
      onBudget,
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

    expect(rollupCategory(entries, transactions, onBudget, "cat-1", "2026-03")).toEqual({
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
    expect(rollupCategory(entries, transactions, onBudget, "cat-1", "2026-03")).toEqual({
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
    expect(rollupCategory(entries, transactions, onBudget, "cat-1", "2026-02")).toEqual({
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

    expect(rollupCategory(entries, transactions, onBudget, "cat-1", "2026-03")).toEqual({
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

    expect(rollupCategory(entries, transactions, onBudget, "cat-1", "2026-03")).toEqual({
      budgeted: 1000,
      activity: 0,
      available: 1000,
    });
  });

  it("ignores soft-deleted budget entries", () => {
    const entries = [
      makeBudgetEntry({ month: "2026-03", budgeted: 1000, deleted_at: Date.now() }),
    ];

    expect(rollupCategory(entries, [], onBudget, "cat-1", "2026-03")).toEqual({
      budgeted: 0,
      activity: 0,
      available: 0,
    });
  });
});

describe("balanceThrough", () => {
  it("sums non-deleted transactions in the account dated on or before the month's last day", () => {
    const transactions = [
      makeTransaction({ date: "2026-02-28", amount: 1000 }),
      makeTransaction({ date: "2026-03-01", amount: 200 }),
      makeTransaction({ date: "2026-03-31", amount: 30 }),
      makeTransaction({ date: "2026-04-01", amount: 99_999 }),
      makeTransaction({ date: "2026-03-15", amount: 99_999, deleted_at: Date.now() }),
      makeTransaction({ account_id: "acc-2", date: "2026-03-15", amount: 99_999 }),
    ];

    expect(balanceThrough(transactions, "acc-1", "2026-03")).toBe(1230);
    expect(balanceThrough(transactions, "acc-1", "2026-01")).toBe(0);
  });
});

describe("onBudgetAccountIds", () => {
  it("includes only non-deleted on-budget accounts", () => {
    const ids = onBudgetAccountIds([
      makeAccount({ id: "on" }),
      makeAccount({ id: "off", on_budget: false }),
      makeAccount({ id: "deleted", deleted_at: Date.now() }),
    ]);
    expect([...ids]).toEqual(["on"]);
  });
});

describe("toBudget", () => {
  // Mirrors internal/api/budget_test.go's TestBudgetGet_IncludesToBudget.
  it("subtracts non-income available from on-budget balances", () => {
    const onBudgetAccount = makeAccount({ id: "acc-1", on_budget: true });
    const offBudgetAccount = makeAccount({ id: "acc-2", on_budget: false });
    const groups = [makeGroup({ id: "bills" })];
    const categories = [makeCategory({ id: "cat-1" })];
    const transactions = [
      makeTransaction({ account_id: "acc-1", date: "2026-01-15", amount: 100000 }),
      makeTransaction({ account_id: "acc-2", category_id: null, date: "2026-01-15", amount: 500000 }),
    ];
    const entries = [makeBudgetEntry({ month: "2026-01", budgeted: 20000 })];

    // 100000 balance - 120000 available (20000 budgeted + 100000 activity)
    expect(
      toBudget([onBudgetAccount, offBudgetAccount], groups, categories, transactions, entries, "2026-01"),
    ).toBe(-20000);
  });

  // Mirrors TestBudgetGet_ToBudgetExcludesFutureMonthsBudgeted.
  it("excludes budget entries assigned to months after the one being viewed", () => {
    const account = makeAccount({ id: "acc-1" });
    const groups = [makeGroup({ id: "bills" })];
    const categories = [makeCategory({ id: "cat-1" })];
    const transactions = [
      makeTransaction({ account_id: "acc-1", category_id: null, date: "2026-01-15", amount: 100000 }),
    ];
    const entries = [makeBudgetEntry({ month: "2026-02", budgeted: 30000 })];

    expect(toBudget([account], groups, categories, transactions, entries, "2026-01")).toBe(100000);
  });

  it("ignores deleted accounts and deleted budget entries", () => {
    const account = makeAccount({ id: "acc-1", deleted_at: Date.now() });
    const groups = [makeGroup({ id: "bills" })];
    const categories = [makeCategory({ id: "cat-1" })];
    const transactions = [
      makeTransaction({ account_id: "acc-1", date: "2026-01-15", amount: 100000 }),
    ];
    const entries = [
      makeBudgetEntry({ month: "2026-01", budgeted: 20000, deleted_at: Date.now() }),
    ];

    expect(toBudget([account], groups, categories, transactions, entries, "2026-01")).toBe(0);
  });
});

// Same cases and numbers as internal/budget/envelope_test.go's
// TestToBudgetInvariant, so both engines provably agree.
describe("toBudget invariant", () => {
  const checking = makeAccount({ id: "checking" });
  const investment = makeAccount({ id: "investment", on_budget: false });
  const bills = makeGroup({ id: "bills" });
  const incomeGroup = makeGroup({ id: "income", is_income: true });
  const groceries = makeCategory({ id: "groceries", group_id: "bills" });
  const salary = makeCategory({ id: "salary", group_id: "income", name: "Salary" });
  const paycheck = makeTransaction({
    account_id: "checking",
    category_id: null,
    date: "2026-09-01",
    amount: 1000,
  });
  const groceriesSep = makeBudgetEntry({ category_id: "groceries", month: "2026-09", budgeted: 100 });
  const spend = (amount: number, overrides: Partial<Transaction> = {}) =>
    makeTransaction({
      account_id: "checking",
      category_id: "groceries",
      date: "2026-09-10",
      amount,
      ...overrides,
    });

  const cases: {
    name: string;
    accounts: Account[];
    categories: Category[];
    entries: BudgetEntry[];
    transactions: Transaction[];
    month: string;
    wantToBudget: number;
    wantAvailable: Record<string, number>;
  }[] = [
    {
      name: "categorized spending under budget",
      accounts: [checking],
      categories: [groceries],
      entries: [groceriesSep],
      transactions: [paycheck, spend(-60)],
      month: "2026-09",
      wantToBudget: 900,
      wantAvailable: { groceries: 40 },
    },
    {
      name: "overspending in September",
      accounts: [checking],
      categories: [groceries],
      entries: [groceriesSep],
      transactions: [paycheck, spend(-150)],
      month: "2026-09",
      wantToBudget: 900,
      wantAvailable: { groceries: -50 },
    },
    {
      name: "October after September overspending",
      accounts: [checking],
      categories: [groceries],
      entries: [groceriesSep],
      transactions: [paycheck, spend(-150)],
      month: "2026-10",
      wantToBudget: 850,
      wantAvailable: { groceries: 0 },
    },
    {
      name: "inflow categorized into an income-group category",
      accounts: [checking],
      categories: [groceries, salary],
      entries: [groceriesSep],
      transactions: [{ ...paycheck, category_id: "salary" }, spend(-60)],
      month: "2026-09",
      wantToBudget: 900,
      wantAvailable: { groceries: 40, salary: 1000 },
    },
    {
      name: "categorized transaction in an off-budget account",
      accounts: [checking, investment],
      categories: [groceries],
      entries: [groceriesSep],
      transactions: [
        paycheck,
        spend(-60),
        makeTransaction({ account_id: "investment", category_id: null, date: "2026-09-01", amount: 5000 }),
        spend(-500, { account_id: "investment", date: "2026-09-12" }),
      ],
      month: "2026-09",
      wantToBudget: 900,
      wantAvailable: { groceries: 40 },
    },
    {
      name: "transaction dated after the month",
      accounts: [checking],
      categories: [groceries],
      entries: [groceriesSep],
      transactions: [
        paycheck,
        spend(-60),
        makeTransaction({ account_id: "checking", category_id: null, date: "2026-10-01", amount: 200 }),
        spend(-30, { date: "2026-10-02" }),
      ],
      month: "2026-09",
      wantToBudget: 900,
      wantAvailable: { groceries: 40 },
    },
    {
      name: "budget entry in a month after the one viewed",
      accounts: [checking],
      categories: [groceries],
      entries: [
        groceriesSep,
        makeBudgetEntry({ category_id: "groceries", month: "2026-10", budgeted: 300 }),
      ],
      transactions: [paycheck, spend(-60)],
      month: "2026-09",
      wantToBudget: 900,
      wantAvailable: { groceries: 40 },
    },
  ];

  it.each(cases)("$name", (c) => {
    const groups = [bills, incomeGroup];
    const result = toBudget(c.accounts, groups, c.categories, c.transactions, c.entries, c.month);
    expect(result).toBe(c.wantToBudget);

    const onBudgetIds = onBudgetAccountIds(c.accounts);
    let balanceSum = 0;
    for (const id of onBudgetIds) balanceSum += balanceThrough(c.transactions, id, c.month);

    let nonIncomeAvailable = 0;
    for (const category of c.categories) {
      const figures = rollupCategory(c.entries, c.transactions, onBudgetIds, category.id, c.month);
      if (category.id in c.wantAvailable) {
        expect(figures.available).toBe(c.wantAvailable[category.id]);
      }
      if (category.group_id !== incomeGroup.id) nonIncomeAvailable += figures.available;
    }

    expect(result + nonIncomeAvailable).toBe(balanceSum);
  });
});
