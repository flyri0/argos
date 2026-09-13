import { db } from "./db";
import { enqueueRowMutation } from "./helpers";
import { nextHlc } from "./hlc";
import type { BudgetEntry, Transaction } from "./types";

// §5.4: a category counts as "in use" if any non-deleted transaction
// references it, or — the "leftover balance" check — it has any
// budget_entries row at all, for any month, regardless of amount. This is
// deliberately coarse (it can occasionally demand an unnecessary
// reassignment) so it can never do the opposite and silently skip
// reassignment for a category that still carries money.
export function isCategoryInUse(
  categoryId: string,
  transactions: Transaction[],
  budgetEntries: BudgetEntry[],
): boolean {
  const hasTransaction = transactions.some(
    (transaction) =>
      transaction.category_id === categoryId && transaction.deleted_at === null,
  );
  if (hasTransaction) return true;

  return budgetEntries.some(
    (entry) => entry.category_id === categoryId && entry.deleted_at === null,
  );
}

// §5.4: a payee counts as "in use" if any non-deleted transaction
// references it — payees have no budget_entries equivalent.
export function isPayeeInUse(
  payeeId: string,
  transactions: Transaction[],
): boolean {
  return transactions.some(
    (transaction) => transaction.payee_id === payeeId && transaction.deleted_at === null,
  );
}

// §5.4: moves every transaction referencing `sourceId` onto `targetId`,
// merges budget_entries that collide on month (summing `budgeted`) and
// re-points the rest, then soft-deletes the source category. Wrapped in a
// Dexie transaction so a mid-way failure can't leave transactions pointing
// at one category while its budget_entries move to another — matching
// §5.4's own framing that losing track of money is the one failure mode
// this must never have. This only performs the local write; propagating it
// to other devices happens once /sync is wired in a later milestone.
export async function reassignCategory(
  sourceId: string,
  targetId: string,
): Promise<void> {
  // Every row this call touches — reassigned transactions, merged/re-pointed
  // budget_entries, and the source category's own delete — shares one
  // outbox group_id, so the server applies the whole reassignment as one
  // atomic unit (§2.3/§2.4: a delete bundled with its reassign_to move).
  const groupId = crypto.randomUUID();

  await db.transaction(
    "rw",
    [db.transactions, db.budget_entries, db.categories, db.outbox],
    async () => {
      const [sourceTransactions, sourceBudgetEntries] = await Promise.all([
        db.transactions.where("category_id").equals(sourceId).toArray(),
        db.budget_entries.where("category_id").equals(sourceId).toArray(),
      ]);

      for (const transaction of sourceTransactions) {
        if (transaction.deleted_at !== null) continue;
        await db.transactions.update(transaction.id, {
          category_id: targetId,
          ...nextHlc(),
        });
        const updated = await db.transactions.get(transaction.id);
        if (updated) await enqueueRowMutation("transactions", updated, groupId);
      }

      for (const entry of sourceBudgetEntries) {
        if (entry.deleted_at !== null) continue;

        const targetEntry = await db.budget_entries
          .where("[category_id+month]")
          .equals([targetId, entry.month])
          .first();

        if (targetEntry && targetEntry.deleted_at === null) {
          await db.budget_entries.update(targetEntry.id, {
            budgeted: targetEntry.budgeted + entry.budgeted,
            ...nextHlc(),
          });
          const updatedTarget = await db.budget_entries.get(targetEntry.id);
          if (updatedTarget) await enqueueRowMutation("budget_entries", updatedTarget, groupId);

          await db.budget_entries.update(entry.id, {
            deleted_at: Date.now(),
            ...nextHlc(),
          });
          const updatedSource = await db.budget_entries.get(entry.id);
          if (updatedSource) await enqueueRowMutation("budget_entries", updatedSource, groupId);
        } else {
          await db.budget_entries.update(entry.id, {
            category_id: targetId,
            ...nextHlc(),
          });
          const updated = await db.budget_entries.get(entry.id);
          if (updated) await enqueueRowMutation("budget_entries", updated, groupId);
        }
      }

      await db.categories.update(sourceId, {
        deleted_at: Date.now(),
        ...nextHlc(),
      });
      const updatedCategory = await db.categories.get(sourceId);
      if (updatedCategory) await enqueueRowMutation("categories", updatedCategory, groupId);
    },
  );
}

// §5.4: moves every transaction referencing `sourceId` onto `targetId`,
// then soft-deletes the source payee.
export async function reassignPayee(
  sourceId: string,
  targetId: string,
): Promise<void> {
  const groupId = crypto.randomUUID();

  await db.transaction("rw", [db.transactions, db.payees, db.outbox], async () => {
    const sourceTransactions = await db.transactions
      .where("payee_id")
      .equals(sourceId)
      .toArray();

    for (const transaction of sourceTransactions) {
      if (transaction.deleted_at !== null) continue;
      await db.transactions.update(transaction.id, {
        payee_id: targetId,
        ...nextHlc(),
      });
      const updated = await db.transactions.get(transaction.id);
      if (updated) await enqueueRowMutation("transactions", updated, groupId);
    }

    await db.payees.update(sourceId, { deleted_at: Date.now(), ...nextHlc() });
    const updatedPayee = await db.payees.get(sourceId);
    if (updatedPayee) await enqueueRowMutation("payees", updatedPayee, groupId);
  });
}
