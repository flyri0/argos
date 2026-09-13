import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";

import { db, nextHlc } from "../../db";
import { CategoriesScreen } from "./CategoriesScreen";

beforeEach(async () => {
  await Promise.all(db.tables.map((table) => table.clear()));
});

async function addGroup(user: ReturnType<typeof userEvent.setup>, name: string) {
  await user.click(await screen.findByRole("button", { name: "Add group" }));
  await user.type(screen.getByLabelText("Group name"), name);
  await user.click(screen.getByRole("button", { name: "Save" }));
}

async function addCategory(user: ReturnType<typeof userEvent.setup>, name: string) {
  await user.click(await screen.findByRole("button", { name: "Add category" }));
  await user.type(screen.getByLabelText("Name"), name);
  await user.click(screen.getByRole("button", { name: "Save" }));
}

describe("CategoriesScreen", () => {
  it("shows the empty state with no groups", async () => {
    render(<CategoriesScreen />);
    expect(await screen.findByText("No category groups yet.")).toBeInTheDocument();
  });

  it("creates a group and a category under it", async () => {
    const user = userEvent.setup();
    render(<CategoriesScreen />);

    await addGroup(user, "Bills");
    expect(await screen.findByText("Bills")).toBeInTheDocument();

    await addCategory(user, "Rent");
    expect(await screen.findByText("Rent")).toBeInTheDocument();
  });

  it("only offers the income-group checkbox while no income group exists", async () => {
    const user = userEvent.setup();
    render(<CategoriesScreen />);

    await user.click(await screen.findByRole("button", { name: "Add group" }));
    expect(
      screen.getByLabelText("This is the income group"),
    ).toBeInTheDocument();
    await user.type(screen.getByLabelText("Group name"), "Income");
    await user.click(screen.getByLabelText("This is the income group"));
    await user.click(screen.getByRole("button", { name: "Save" }));

    await addGroup(user, "Bills");
    expect(
      screen.queryByLabelText("This is the income group"),
    ).not.toBeInTheDocument();
  });

  it("renames a category group", async () => {
    const user = userEvent.setup();
    render(<CategoriesScreen />);
    await addGroup(user, "Bills");

    await user.click(await screen.findByRole("button", { name: "Rename" }));
    const nameInput = screen.getByLabelText("Group name");
    await user.clear(nameInput);
    await user.type(nameInput, "Fixed Costs");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByText("Fixed Costs")).toBeInTheDocument();
    expect(screen.queryByText("Bills")).not.toBeInTheDocument();
  });

  it("toggles a category's hidden state", async () => {
    const user = userEvent.setup();
    render(<CategoriesScreen />);
    await addGroup(user, "Bills");
    await addCategory(user, "Rent");

    await user.click(await screen.findByRole("button", { name: "Hide" }));
    expect(await screen.findByText("Rent (Hidden)")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Unhide" }));
    expect(await screen.findByText("Rent")).toBeInTheDocument();
    expect(screen.queryByText("Rent (Hidden)")).not.toBeInTheDocument();
  });

  it("edits a category's name", async () => {
    const user = userEvent.setup();
    render(<CategoriesScreen />);
    await addGroup(user, "Bills");
    await addCategory(user, "Rent");

    await user.click(await screen.findByRole("button", { name: "Edit" }));
    const nameInput = screen.getByLabelText("Name");
    await user.clear(nameInput);
    await user.type(nameInput, "Mortgage");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByText("Mortgage")).toBeInTheDocument();
    expect(screen.queryByText("Rent")).not.toBeInTheDocument();
  });

  it("deletes a category that isn't in use via a simple confirm", async () => {
    const user = userEvent.setup();
    render(<CategoriesScreen />);
    await addGroup(user, "Bills");
    await addCategory(user, "Rent");

    await user.click(await screen.findByRole("button", { name: "Delete" }));
    expect(screen.getByText("Delete this category?")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Confirm" }));

    expect(await screen.findByText("No categories in this group yet.")).toBeInTheDocument();
    expect(screen.queryByText("Rent")).not.toBeInTheDocument();
  });

  it("reassigns transactions off a category that's in use before deleting it", async () => {
    const user = userEvent.setup();
    render(<CategoriesScreen />);
    await addGroup(user, "Bills");
    await addCategory(user, "Rent");
    await addCategory(user, "Mortgage");

    const rentCategory = (await db.categories.toArray()).find((c) => c.name === "Rent")!;
    await db.transactions.add({
      id: crypto.randomUUID(),
      ...nextHlc(),
      deleted_at: null,
      account_id: "acc-1",
      category_id: rentCategory.id,
      payee_id: null,
      parent_id: null,
      date: "2026-03-01",
      amount: -1000,
      cleared: false,
      notes: "",
      transfer_id: null,
    });

    const rentRow = (await screen.findByText("Rent")).closest("li")!;
    await user.click(within(rentRow).getByRole("button", { name: "Delete" }));

    expect(
      screen.getByText(
        "This category is in use. Choose another category to move its transactions and budget to:",
      ),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Move & delete" }));

    await waitFor(() =>
      expect(screen.queryByText("Rent")).not.toBeInTheDocument(),
    );
    const movedTransaction = await db.transactions.toArray();
    expect(movedTransaction[0].category_id).toBe(
      (await db.categories.toArray()).find((c) => c.name === "Mortgage")!.id,
    );
  });
});
