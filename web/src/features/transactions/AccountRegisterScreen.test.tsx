import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";

import { accounts, categories, db, payees } from "../../db";
import type { Account, Category, Payee } from "../../db";
import { AccountRegisterScreen } from "./AccountRegisterScreen";

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

describe("AccountRegisterScreen", () => {
  it("shows the empty state when the account has no transactions", async () => {
    const account = makeAccount();
    await accounts.create(account);

    render(<AccountRegisterScreen account={account} onBack={() => {}} />);

    expect(await screen.findByText("No transactions yet.")).toBeInTheDocument();
  });

  it("creates a transaction and lists it under its category and payee", async () => {
    const account = makeAccount();
    const category = makeCategory({ name: "Groceries" });
    const payee = makePayee({ name: "Corner Store" });
    await accounts.create(account);
    await categories.create(category);
    await payees.create(payee);

    const user = userEvent.setup();
    render(<AccountRegisterScreen account={account} onBack={() => {}} />);

    await user.click(await screen.findByRole("button", { name: "Add transaction" }));
    await user.selectOptions(screen.getByLabelText("Payee"), payee.id);
    await user.selectOptions(screen.getByLabelText("Category"), category.id);
    await user.type(screen.getByLabelText("Amount"), "12.34");
    await user.click(screen.getByRole("button", { name: "Save" }));

    // Scoped to the transaction table (which only renders once the first
    // transaction exists) rather than a bare screen.findByText("Groceries")
    // — the category select in the still-open form already renders an
    // <option>Groceries</option> the moment the modal opens, so an
    // unscoped text query can resolve against that instead of the real row.
    const table = await screen.findByRole("table");
    const row = within(table).getByText("Groceries").closest("tr");
    expect(row).not.toBeNull();
    expect(within(row!).getByText("Corner Store")).toBeInTheDocument();
    expect(within(row!).getByText(/12\.34/)).toBeInTheDocument();
  });

  it("rejects a zero amount instead of creating a row", async () => {
    const account = makeAccount();
    await accounts.create(account);

    const user = userEvent.setup();
    render(<AccountRegisterScreen account={account} onBack={() => {}} />);

    await user.click(await screen.findByRole("button", { name: "Add transaction" }));
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Enter an amount greater than zero.",
    );
  });

  it("edits an existing transaction", async () => {
    const account = makeAccount();
    await accounts.create(account);

    const user = userEvent.setup();
    render(<AccountRegisterScreen account={account} onBack={() => {}} />);

    await user.click(await screen.findByRole("button", { name: "Add transaction" }));
    await user.type(screen.getByLabelText("Amount"), "10");
    await user.click(screen.getByRole("button", { name: "Save" }));
    await screen.findByRole("button", { name: "Edit" });

    await user.click(screen.getByRole("button", { name: "Edit" }));
    await user.type(screen.getByLabelText("Notes"), "Updated note");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByText("Updated note")).toBeInTheDocument();
  });

  it("marks a transaction cleared and back to uncleared", async () => {
    const account = makeAccount();
    await accounts.create(account);

    const user = userEvent.setup();
    render(<AccountRegisterScreen account={account} onBack={() => {}} />);

    await user.click(await screen.findByRole("button", { name: "Add transaction" }));
    await user.type(screen.getByLabelText("Amount"), "10");
    await user.click(screen.getByRole("button", { name: "Save" }));

    await user.click(await screen.findByRole("button", { name: "Mark cleared" }));
    expect(await screen.findByText(/Cleared/)).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Mark uncleared" }));
    await waitFor(() => expect(screen.queryByText(/Cleared/)).not.toBeInTheDocument());
  });

  it("deletes a transaction after confirming", async () => {
    const account = makeAccount();
    await accounts.create(account);

    const user = userEvent.setup();
    render(<AccountRegisterScreen account={account} onBack={() => {}} />);

    await user.click(await screen.findByRole("button", { name: "Add transaction" }));
    await user.type(screen.getByLabelText("Amount"), "10");
    await user.click(screen.getByRole("button", { name: "Save" }));

    await user.click(await screen.findByRole("button", { name: "Delete" }));
    expect(screen.getByText("Delete this transaction?")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Confirm" }));

    expect(await screen.findByText("No transactions yet.")).toBeInTheDocument();
  });

  it("disables adding new transactions for a closed account", async () => {
    const account = makeAccount({ closed: true });
    await accounts.create(account);

    render(<AccountRegisterScreen account={account} onBack={() => {}} />);

    expect(
      await screen.findByText("This account is closed and can't accept new transactions."),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add transaction" })).not.toBeInTheDocument();
  });

  it("excludes closed accounts from the transfer target list", async () => {
    const checking = makeAccount({ name: "Checking" });
    const closedSavings = makeAccount({ name: "Old Savings", closed: true });
    await accounts.create(checking);
    await accounts.create(closedSavings);

    const user = userEvent.setup();
    render(<AccountRegisterScreen account={checking} onBack={() => {}} />);

    await user.click(await screen.findByRole("button", { name: "Add transaction" }));
    expect(screen.queryByLabelText("This is a transfer")).not.toBeInTheDocument();
  });

  it("creates a transfer, generating a mirror transaction in the target account", async () => {
    const checking = makeAccount({ name: "Checking" });
    const savings = makeAccount({ name: "Savings" });
    await accounts.create(checking);
    await accounts.create(savings);

    const user = userEvent.setup();
    render(<AccountRegisterScreen account={checking} onBack={() => {}} />);

    await user.click(await screen.findByRole("button", { name: "Add transaction" }));
    await user.click(screen.getByLabelText("This is a transfer"));
    await user.selectOptions(screen.getByLabelText("Transfer to account"), savings.id);
    await user.type(screen.getByLabelText("Amount"), "50");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByText("Transfer: Savings")).toBeInTheDocument();

    const mirrorRows = await db.transactions.where("account_id").equals(savings.id).toArray();
    expect(mirrorRows).toHaveLength(1);
    expect(mirrorRows[0].amount).toBe(5000);
    expect(mirrorRows[0].category_id).toBeNull();
    expect(mirrorRows[0].transfer_id).not.toBeNull();
  });

  it("deleting either leg of a transfer removes both", async () => {
    const checking = makeAccount({ name: "Checking" });
    const savings = makeAccount({ name: "Savings" });
    await accounts.create(checking);
    await accounts.create(savings);

    const user = userEvent.setup();
    render(<AccountRegisterScreen account={checking} onBack={() => {}} />);

    await user.click(await screen.findByRole("button", { name: "Add transaction" }));
    await user.click(screen.getByLabelText("This is a transfer"));
    await user.selectOptions(screen.getByLabelText("Transfer to account"), savings.id);
    await user.type(screen.getByLabelText("Amount"), "50");
    await user.click(screen.getByRole("button", { name: "Save" }));

    await user.click(await screen.findByRole("button", { name: "Delete" }));
    expect(
      screen.getByText("This will also delete the matching transfer transaction in Savings."),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Confirm" }));

    const remaining = await db.transactions.toArray();
    expect(remaining.every((row) => row.deleted_at !== null)).toBe(true);
  });
});
