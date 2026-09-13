import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";

import {
  budgetEntries,
  categories,
  categoryGroups,
  db,
  transactions,
} from "../../db";
import type { BudgetEntry, Category, CategoryGroup, Transaction } from "../../db";
import { BudgetScreen } from "./BudgetScreen";

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

const month = new Date().toISOString().slice(0, 7);

function makeGroup(overrides: Partial<CategoryGroup> = {}): CategoryGroup {
  return {
    id: crypto.randomUUID(),
    ...baseSync,
    name: "Bills",
    is_income: false,
    sort_order: 0,
    ...overrides,
  };
}

function makeCategory(overrides: Partial<Category> = {}): Category {
  return {
    id: crypto.randomUUID(),
    ...baseSync,
    group_id: crypto.randomUUID(),
    name: "Rent",
    hidden: false,
    sort_order: 0,
    notes: null,
    ...overrides,
  };
}

function makeBudgetEntry(overrides: Partial<BudgetEntry> = {}): BudgetEntry {
  return {
    id: crypto.randomUUID(),
    ...baseSync,
    category_id: "cat-1",
    month,
    budgeted: 0,
    ...overrides,
  };
}

function makeTransaction(overrides: Partial<Transaction> = {}): Transaction {
  return {
    id: crypto.randomUUID(),
    ...baseSync,
    account_id: "acc-1",
    category_id: "cat-1",
    payee_id: null,
    parent_id: null,
    date: `${month}-05`,
    amount: 0,
    cleared: false,
    notes: "",
    transfer_id: null,
    ...overrides,
  };
}

describe("BudgetScreen", () => {
  it("shows the empty state when there are no categories", async () => {
    render(<BudgetScreen />);
    expect(await screen.findByText("No categories yet.")).toBeInTheDocument();
  });

  it("lists a category with zeroed figures by default", async () => {
    const group = makeGroup();
    const category = makeCategory({ group_id: group.id, name: "Rent" });
    await categoryGroups.create(group);
    await categories.create(category);

    render(<BudgetScreen />);

    const row = (await screen.findByText("Rent")).closest("tr");
    expect(row).not.toBeNull();
    expect(within(row!).getByLabelText("Budgeted for Rent")).toHaveValue("0.00");
    expect(within(row!).getAllByText("$0.00")).toHaveLength(2);
  });

  it("edits the budgeted amount, persisting it and recomputing available", async () => {
    const group = makeGroup();
    const category = makeCategory({ group_id: group.id, name: "Rent" });
    await categoryGroups.create(group);
    await categories.create(category);

    const user = userEvent.setup();
    render(<BudgetScreen />);

    const input = await screen.findByLabelText("Budgeted for Rent");
    await user.clear(input);
    await user.type(input, "500");
    await user.tab();

    await waitFor(async () => {
      const entries = await db.budget_entries.toArray();
      expect(entries).toHaveLength(1);
      expect(entries[0]).toMatchObject({
        category_id: category.id,
        month,
        budgeted: 50000,
      });
    });

    const row = (await screen.findByText("Rent")).closest("tr")!;
    expect(await within(row).findByText("$500.00")).toBeInTheDocument();
  });

  it("rolls a positive available forward into the next month", async () => {
    const group = makeGroup();
    const category = makeCategory({ group_id: group.id, name: "Rent" });
    await categoryGroups.create(group);
    await categories.create(category);
    await budgetEntries.create(
      makeBudgetEntry({ category_id: category.id, month, budgeted: 10000 }),
    );
    await transactions.create(
      makeTransaction({ category_id: category.id, date: `${month}-05`, amount: -4000 }),
    );

    const user = userEvent.setup();
    render(<BudgetScreen />);

    const row = (await screen.findByText("Rent")).closest("tr")!;
    expect(await within(row).findByText("$60.00")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Next month" }));

    const nextRow = (await screen.findByText("Rent")).closest("tr")!;
    expect(await within(nextRow).findByText("$60.00")).toBeInTheDocument();
    expect(within(nextRow).getByLabelText("Budgeted for Rent")).toHaveValue("0.00");
  });

  it("shows a hidden badge for hidden categories", async () => {
    const group = makeGroup();
    const category = makeCategory({ group_id: group.id, name: "Old Category", hidden: true });
    await categoryGroups.create(group);
    await categories.create(category);

    render(<BudgetScreen />);

    expect(await screen.findByText("Old Category (Hidden)")).toBeInTheDocument();
  });
});
