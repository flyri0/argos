import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";

import { db } from "../../db";
import { AccountsScreen } from "./AccountsScreen";

beforeEach(async () => {
  await Promise.all(db.tables.map((table) => table.clear()));
});

describe("AccountsScreen", () => {
  it("shows the empty state when there are no accounts", async () => {
    render(<AccountsScreen />);

    expect(await screen.findByText("No accounts yet.")).toBeInTheDocument();
  });

  it("creates an account and lists it with a zero balance", async () => {
    const user = userEvent.setup();
    render(<AccountsScreen />);

    await user.click(await screen.findByRole("button", { name: "Add account" }));
    await user.type(screen.getByLabelText("Name"), "Checking");
    await user.selectOptions(screen.getByLabelText("Type"), "savings");
    await user.click(screen.getByRole("button", { name: "Save" }));

    // Scoped to the accounts table (which only renders once an account
    // exists) rather than a bare screen.findByText("Checking") — the Type
    // select in the still-open form already renders an
    // <option>Checking</option> (the "checking" account type) the moment
    // the form opens, so an unscoped text query can resolve against that
    // instead of the real row, since this account happens to be named the
    // same as that type's label.
    const table = await screen.findByRole("table");
    const row = within(table).getByText("Checking").closest("tr");
    expect(row).not.toBeNull();
    expect(within(row!).getByText("Savings")).toBeInTheDocument();
    expect(within(row!).getByText("$0.00")).toBeInTheDocument();
  });

  it("records a starting balance as a transaction when creating an account", async () => {
    const user = userEvent.setup();
    render(<AccountsScreen />);

    await user.click(await screen.findByRole("button", { name: "Add account" }));
    await user.type(screen.getByLabelText("Name"), "Household");
    await user.type(screen.getByLabelText("Starting balance"), "500");
    await user.click(screen.getByRole("button", { name: "Save" }));

    const table = await screen.findByRole("table");
    const row = within(table).getByText("Household").closest("tr");
    expect(row).not.toBeNull();
    expect(within(row!).getByText("$500.00")).toBeInTheDocument();

    const stored = await db.transactions.toArray();
    expect(stored).toHaveLength(1);
    expect(stored[0]).toMatchObject({
      amount: 50000,
      category_id: null,
      payee_id: null,
      cleared: true,
      notes: "Starting Balance",
    });
  });

  it("does not offer a starting balance field when editing an existing account", async () => {
    const user = userEvent.setup();
    render(<AccountsScreen />);

    await user.click(await screen.findByRole("button", { name: "Add account" }));
    await user.type(screen.getByLabelText("Name"), "Household");
    await user.click(screen.getByRole("button", { name: "Save" }));

    await user.click(await screen.findByRole("button", { name: "Edit" }));
    expect(screen.queryByLabelText("Starting balance")).not.toBeInTheDocument();
  });

  it("rejects an empty name instead of creating a row", async () => {
    const user = userEvent.setup();
    render(<AccountsScreen />);

    await user.click(await screen.findByRole("button", { name: "Add account" }));
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Name is required.",
    );
    expect(screen.queryByText("No accounts yet.")).not.toBeInTheDocument();
  });

  it("edits an existing account", async () => {
    const user = userEvent.setup();
    render(<AccountsScreen />);

    await user.click(await screen.findByRole("button", { name: "Add account" }));
    await user.type(screen.getByLabelText("Name"), "Household");
    await user.click(screen.getByRole("button", { name: "Save" }));

    await user.click(await screen.findByRole("button", { name: "Edit" }));
    const nameInput = screen.getByLabelText("Name");
    await user.clear(nameInput);
    await user.type(nameInput, "Everyday Spending");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByText("Everyday Spending")).toBeInTheDocument();
    expect(screen.queryByText("Household")).not.toBeInTheDocument();
  });

  it("closes an account after confirming, then allows reopening it", async () => {
    const user = userEvent.setup();
    render(<AccountsScreen />);

    await user.click(await screen.findByRole("button", { name: "Add account" }));
    await user.type(screen.getByLabelText("Name"), "Household");
    await user.click(screen.getByRole("button", { name: "Save" }));

    await user.click(await screen.findByRole("button", { name: "Close" }));
    expect(
      screen.getByText(
        "Close this account? It will no longer accept new transactions.",
      ),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Confirm" }));

    expect(await screen.findByText("Household (Closed)")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Reopen" }));
    await waitFor(() =>
      expect(screen.getByText("Household")).toBeInTheDocument(),
    );
  });
});
