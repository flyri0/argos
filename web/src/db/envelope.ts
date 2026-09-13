import { computeBalance } from "./balance";
import type { Account, BudgetEntry, Transaction } from "./types";

// §5.3's budgeting engine, reimplemented in TypeScript so the monthly
// budget grid can compute figures client-side without a round-trip —
// project_spec.md §5.3 notes the engine may be implemented twice, once in
// Go (internal/budget/envelope.go) and once here. Kept free of any Dexie
// dependency, like its Go counterpart is free of the DB layer, so it stays
// pure and testable in isolation.

// Sums the amount of every non-deleted transaction in categoryId during
// month ("YYYY-MM"). A transaction's own date is "YYYY-MM-DD" (§5.1), so
// matching compares its first 7 characters. Mirrors internal/budget.Activity.
export function activity(
  transactions: Transaction[],
  categoryId: string,
  month: string,
): number {
  let total = 0;
  for (const transaction of transactions) {
    if (transaction.deleted_at !== null) continue;
    if (transaction.category_id !== categoryId) continue;
    if (transaction.date.slice(0, 7) !== month) continue;
    total += transaction.amount;
  }
  return total;
}

// Implements §5.3's rollover rule: a negative previousAvailable (last
// month overspent) resets to zero rather than carrying the deficit
// forward. Pass previousAvailable as 0 for a category's first budgeted
// month, since there is no prior month to roll over from. Mirrors
// internal/budget.Available.
export function available(
  previousAvailable: number,
  budgeted: number,
  activityThisMonth: number,
): number {
  const rolledOver = previousAvailable < 0 ? 0 : previousAvailable;
  return rolledOver + budgeted + activityThisMonth;
}

export interface CategoryMonthFigures {
  budgeted: number;
  activity: number;
  available: number;
}

// Walks a category's full budget_entries/transaction history up to and
// including month, applying available()'s rollover rule forward from the
// earliest relevant month, so a rollover or an overspend from arbitrarily
// far back still reaches the target month correctly. Mirrors
// internal/api.rollupCategory, but takes the full (unfiltered) local
// tables rather than pre-scoped-by-category query results, since the grid
// reads every category off of one Dexie live query rather than issuing a
// query per category.
export function rollupCategory(
  entries: BudgetEntry[],
  transactions: Transaction[],
  categoryId: string,
  month: string,
): CategoryMonthFigures {
  const budgetedByMonth = new Map<string, number>();
  for (const entry of entries) {
    if (entry.deleted_at !== null) continue;
    if (entry.category_id !== categoryId) continue;
    budgetedByMonth.set(entry.month, entry.budgeted);
  }

  const months = new Set<string>([month, ...budgetedByMonth.keys()]);
  for (const transaction of transactions) {
    if (transaction.deleted_at !== null) continue;
    if (transaction.category_id !== categoryId) continue;
    if (transaction.date.length >= 7) months.add(transaction.date.slice(0, 7));
  }

  const ordered = [...months].filter((m) => m <= month).sort();

  let previousAvailable = 0;
  let result: CategoryMonthFigures = { budgeted: 0, activity: 0, available: 0 };
  for (const m of ordered) {
    const budgeted = budgetedByMonth.get(m) ?? 0;
    const activityThisMonth = activity(transactions, categoryId, m);
    const availableThisMonth = available(previousAvailable, budgeted, activityThisMonth);
    previousAvailable = availableThisMonth;
    if (m === month) {
      result = { budgeted, activity: activityThisMonth, available: availableThisMonth };
    }
  }
  return result;
}

// Implements §5.3's "Available to Budget" total: the combined balance of
// every on-budget, non-deleted account, minus everything already budgeted
// across all months up to and including month. Off-budget accounts (e.g. a
// tracked investment) never count toward it, and a month doesn't count
// budgeted amounts assigned ahead of the one being viewed. Mirrors
// internal/api.BudgetHandler.Get's to_budget computation, built on top of
// internal/budget.ToBudget there.
export function toBudget(
  accounts: Account[],
  transactions: Transaction[],
  budgetEntries: BudgetEntry[],
  month: string,
): number {
  let totalBalance = 0;
  for (const account of accounts) {
    if (account.deleted_at !== null || !account.on_budget) continue;
    totalBalance += computeBalance(
      transactions.filter((t) => t.account_id === account.id),
    );
  }

  let budgetedToDate = 0;
  for (const entry of budgetEntries) {
    if (entry.deleted_at !== null) continue;
    if (entry.month <= month) budgetedToDate += entry.budgeted;
  }

  return totalBalance - budgetedToDate;
}
