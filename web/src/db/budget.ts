import { budgetEntryId } from "../lib/uuid";
import { db } from "./db";
import { enqueueRowMutation } from "./helpers";
import { compareHlc, nextHlc } from "./hlc";
import type { BudgetEntry } from "./types";

// §5.2: (category_id, month) is a budget entry's identity. Locally a pair can
// briefly hold more than one row (e.g. before a pull resolves a duplicate),
// so prefer the live row, else the most recent tombstone.
export async function findBudgetEntryForPair(
  categoryId: string,
  month: string,
): Promise<BudgetEntry | undefined> {
  const rows = await db.budget_entries
    .where("[category_id+month]")
    .equals([categoryId, month])
    .toArray();
  const live = rows.find((row) => row.deleted_at === null);
  if (live) return live;
  return rows.reduce<BudgetEntry | undefined>(
    (latest, row) => (latest === undefined || compareHlc(row, latest) > 0 ? row : latest),
    undefined,
  );
}

// §7.3 PUT /api/budget/:month/:category_id, done directly against the
// local Dexie replica: any existing row for the pair — live or tombstone —
// is updated in place so its id survives, and a brand-new pair gets its
// deterministic UUIDv5 id (§5.2). Per §5.2, setting budgeted to 0
// soft-deletes the row instead of storing a zero (a no-op if no live row
// exists) — this is what keeps §5.4's "any row exists" in-use signal
// meaningful over time. Mirrors internal/db.SetBudgetedAmount.
export async function setBudgetedAmount(
  categoryId: string,
  month: string,
  budgeted: number,
): Promise<void> {
  await db.transaction("rw", db.budget_entries, db.outbox, async () => {
    const existing = await findBudgetEntryForPair(categoryId, month);

    if (!existing) {
      if (budgeted === 0) return;
      const row: BudgetEntry = {
        id: budgetEntryId(categoryId, month),
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

    if (existing.deleted_at !== null) {
      if (budgeted === 0) return;
      await db.budget_entries.update(existing.id, {
        budgeted,
        deleted_at: null,
        ...nextHlc(),
      });
    } else if (budgeted === 0) {
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
