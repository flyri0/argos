import { db } from "./db";
import { nextHlc } from "./hlc";

// §7.3 PUT /api/budget/:month/:category_id, done directly against the
// local Dexie replica: an existing (category_id, month) row is updated in
// place so its id survives the edit, and a new row is inserted only if
// none exists yet. Per §5.2, setting budgeted to 0 soft-deletes the row
// instead of storing a zero (a no-op if no row exists for that
// category/month yet) — this is what keeps §5.4's "any row exists"
// in-use signal meaningful over time. Mirrors internal/db.SetBudgetedAmount.
export async function setBudgetedAmount(
  categoryId: string,
  month: string,
  budgeted: number,
): Promise<void> {
  await db.transaction("rw", db.budget_entries, async () => {
    const rows = await db.budget_entries
      .where("[category_id+month]")
      .equals([categoryId, month])
      .toArray();
    const existing = rows.find((row) => row.deleted_at === null);

    if (!existing) {
      if (budgeted === 0) return;
      await db.budget_entries.add({
        id: crypto.randomUUID(),
        category_id: categoryId,
        month,
        budgeted,
        deleted_at: null,
        ...nextHlc(),
      });
      return;
    }

    if (budgeted === 0) {
      await db.budget_entries.update(existing.id, {
        deleted_at: Date.now(),
        ...nextHlc(),
      });
    } else {
      await db.budget_entries.update(existing.id, {
        budgeted,
        ...nextHlc(),
      });
    }
  });
}
