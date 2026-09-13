import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";

import { db, nextHlc } from "../../db";
import { PayeesScreen } from "./PayeesScreen";

beforeEach(async () => {
  await Promise.all(db.tables.map((table) => table.clear()));
});

async function addPayee(user: ReturnType<typeof userEvent.setup>, name: string) {
  await user.click(await screen.findByRole("button", { name: "Add payee" }));
  await user.type(screen.getByLabelText("Name"), name);
  await user.click(screen.getByRole("button", { name: "Save" }));
}

describe("PayeesScreen", () => {
  it("shows the empty state with no payees", async () => {
    render(<PayeesScreen />);
    expect(await screen.findByText("No payees yet.")).toBeInTheDocument();
  });

  it("creates and renames a payee", async () => {
    const user = userEvent.setup();
    render(<PayeesScreen />);

    await addPayee(user, "Landlord");
    expect(await screen.findByText("Landlord")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Edit" }));
    const nameInput = screen.getByLabelText("Name");
    await user.clear(nameInput);
    await user.type(nameInput, "Property Manager");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByText("Property Manager")).toBeInTheDocument();
    expect(screen.queryByText("Landlord")).not.toBeInTheDocument();
  });

  it("deletes a payee that isn't in use via a simple confirm", async () => {
    const user = userEvent.setup();
    render(<PayeesScreen />);
    await addPayee(user, "Landlord");

    await user.click(await screen.findByRole("button", { name: "Delete" }));
    expect(screen.getByText("Delete this payee?")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Confirm" }));

    expect(await screen.findByText("No payees yet.")).toBeInTheDocument();
    expect(screen.queryByText("Landlord")).not.toBeInTheDocument();
  });

  it("reassigns transactions off a payee that's in use before deleting it", async () => {
    const user = userEvent.setup();
    render(<PayeesScreen />);
    await addPayee(user, "Landlord");
    await addPayee(user, "Property Manager");

    const landlord = (await db.payees.toArray()).find((p) => p.name === "Landlord")!;
    await db.transactions.add({
      id: crypto.randomUUID(),
      ...nextHlc(),
      deleted_at: null,
      account_id: "acc-1",
      category_id: null,
      payee_id: landlord.id,
      parent_id: null,
      date: "2026-03-01",
      amount: -1000,
      cleared: false,
      notes: "",
      transfer_id: null,
    });

    const landlordRow = (await screen.findByText("Landlord")).closest("li")!;
    await user.click(
      within(landlordRow).getByRole("button", { name: "Delete" }),
    );

    expect(
      screen.getByText(
        "This payee is in use. Choose another payee to move its transactions to:",
      ),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Move & delete" }));

    await waitFor(() =>
      expect(screen.queryByText("Landlord")).not.toBeInTheDocument(),
    );
    const movedTransaction = (await db.transactions.toArray())[0];
    const propertyManager = (await db.payees.toArray()).find(
      (p) => p.name === "Property Manager",
    )!;
    expect(movedTransaction.payee_id).toBe(propertyManager.id);
  });
});
