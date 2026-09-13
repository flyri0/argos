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

    const row = (await screen.findByText("Checking")).closest("tr");
    expect(row).not.toBeNull();
    expect(within(row!).getByText("Savings")).toBeInTheDocument();
    expect(within(row!).getByText("$0.00")).toBeInTheDocument();
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
