import { beforeEach, describe, expect, it } from "vitest";

import { budgetEntryId } from "../lib/uuid";
import { setBudgetedAmount } from "./budget";
import { db } from "./db";
import { budgetEntries, outbox } from "./helpers";

beforeEach(async () => {
  await Promise.all(db.tables.map((table) => table.clear()));
});

describe("setBudgetedAmount", () => {
  it("inserts a new row when none exists yet for the category/month", async () => {
    await setBudgetedAmount("cat-1", "2026-03", 5000);

    const rows = await budgetEntries.list();
    expect(rows).toHaveLength(1);
    expect(rows[0]).toMatchObject({ category_id: "cat-1", month: "2026-03", budgeted: 5000 });
    expect(rows[0].deleted_at).toBeNull();
  });

  it("is a no-op setting to 0 when no row exists yet", async () => {
    await setBudgetedAmount("cat-1", "2026-03", 0);
    expect(await budgetEntries.list()).toHaveLength(0);
  });

  it("updates the existing row in place, keeping its id, on a second edit", async () => {
    await setBudgetedAmount("cat-1", "2026-03", 5000);
    const firstId = (await budgetEntries.list())[0].id;

    await setBudgetedAmount("cat-1", "2026-03", 7500);

    const rows = await budgetEntries.list();
    expect(rows).toHaveLength(1);
    expect(rows[0].id).toBe(firstId);
    expect(rows[0].budgeted).toBe(7500);
  });

  it("soft-deletes the row instead of storing a zero", async () => {
    await setBudgetedAmount("cat-1", "2026-03", 5000);
    const firstId = (await budgetEntries.list())[0].id;

    await setBudgetedAmount("cat-1", "2026-03", 0);

    const row = await budgetEntries.get(firstId);
    expect(row?.deleted_at).not.toBeNull();
    expect(row?.budgeted).toBe(5000);
  });

  it("enqueues a standalone upsert on insert and a \"delete\" op on the zero-out (§2.2/§2.4)", async () => {
    await setBudgetedAmount("cat-1", "2026-03", 5000);
    await setBudgetedAmount("cat-1", "2026-03", 0);

    const entries = await outbox.listUnsynced();
    expect(entries).toHaveLength(2);
    expect(entries[0].op).toBe("upsert");
    expect(entries[0].group_id).toBeNull();
    expect(entries[1].op).toBe("delete");
  });

  it("keeps (category_id, month) rows independent of each other", async () => {
    await setBudgetedAmount("cat-1", "2026-03", 1000);
    await setBudgetedAmount("cat-1", "2026-04", 2000);
    await setBudgetedAmount("cat-2", "2026-03", 3000);

    const rows = await budgetEntries.list();
    expect(rows).toHaveLength(3);
  });

  it("revives the same row after a prior zero soft-deleted it, enqueuing an upsert", async () => {
    await setBudgetedAmount("cat-1", "2026-03", 1000);
    const firstId = (await budgetEntries.list())[0].id;
    await setBudgetedAmount("cat-1", "2026-03", 0);

    await setBudgetedAmount("cat-1", "2026-03", 4000);

    const rows = await budgetEntries.list();
    expect(rows).toHaveLength(1);
    expect(rows[0]).toMatchObject({ id: firstId, budgeted: 4000, deleted_at: null });

    const entries = await outbox.listUnsynced();
    const last = entries[entries.length - 1];
    expect(last.op).toBe("upsert");
    expect(last.row).toMatchObject({ id: firstId, deleted_at: null });
  });

  it("gives a brand-new pair its deterministic UUIDv5 id (§5.2)", async () => {
    await setBudgetedAmount("cat-1", "2026-03", 5000);

    const rows = await budgetEntries.list();
    expect(rows[0].id).toBe(budgetEntryId("cat-1", "2026-03"));
  });
});

describe("budgetEntryId", () => {
  // Same value asserted by internal/db's TestBudgetEntryID, so both sides
  // mint the same id for a pair.
  it("matches the Go implementation", () => {
    expect(budgetEntryId("11111111-1111-1111-1111-111111111111", "2026-03")).toBe(
      "ee5574d2-1dda-57f6-a1a4-4b604765be00",
    );
  });
});
