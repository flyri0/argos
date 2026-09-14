import { generateUUID } from "../lib/uuid";
import { db } from "./db";
import { enqueueRowMutation } from "./helpers";
import { nextHlc } from "./hlc";
import type { BudgetEntry } from "./types";

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
  await db.transaction("rw", db.budget_entries, db.outbox, async () => {
    const rows = await db.budget_entries
      .where("[category_id+month]")
      .equals([categoryId, month])
      .toArray();
    const existing = rows.find((row) => row.deleted_at === null);

    if (!existing) {
      if (budgeted === 0) return;
      const row: BudgetEntry = {
        id: generateUUID(),
        category_id: categoryId,
        month,
        budgeted,
        deleted_at: null,
        ...nextHlc(),
      };
      await db.budget_entries.add(row);
      await enqueueRowMutation("budget_entries", row);
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
    const updated = await db.budget_entries.get(existing.id);
    if (updated) await enqueueRowMutation("budget_entries", updated);
  });
}
