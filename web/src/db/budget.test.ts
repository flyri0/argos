import { beforeEach, describe, expect, it } from "vitest";

import { setBudgetedAmount } from "./budget";
import { db } from "./db";
import { budgetEntries } from "./helpers";

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

  it("keeps (category_id, month) rows independent of each other", async () => {
    await setBudgetedAmount("cat-1", "2026-03", 1000);
    await setBudgetedAmount("cat-1", "2026-04", 2000);
    await setBudgetedAmount("cat-2", "2026-03", 3000);

    const rows = await budgetEntries.list();
    expect(rows).toHaveLength(3);
  });

  it("re-creates a row after a prior zero soft-deleted it", async () => {
    await setBudgetedAmount("cat-1", "2026-03", 1000);
    await setBudgetedAmount("cat-1", "2026-03", 0);

    await setBudgetedAmount("cat-1", "2026-03", 4000);

    const live = (await budgetEntries.list()).filter((row) => row.deleted_at === null);
    expect(live).toHaveLength(1);
    expect(live[0].budgeted).toBe(4000);
  });
});
