import type { Account, BudgetEntry, Category, CategoryGroup, Transaction } from "./types";

// §5.3's budgeting engine, reimplemented in TypeScript so the monthly
// budget grid can compute figures client-side without a round-trip —
// project_spec.md §5.3 notes the engine may be implemented twice, once in
// Go (internal/budget/envelope.go) and once here. Kept free of any Dexie
// dependency, like its Go counterpart is free of the DB layer, so it stays
// pure and testable in isolation.

// Ids of every non-deleted on-budget account — the only accounts whose
// transactions count toward activity and to_budget (§5.3).
export function onBudgetAccountIds(accounts: Account[]): Set<string> {
  const ids = new Set<string>();
  for (const account of accounts) {
    if (account.deleted_at === null && account.on_budget) ids.add(account.id);
  }
  return ids;
}

// Sums the amount of every non-deleted transaction in categoryId during
// month ("YYYY-MM"), counting only transactions in on-budget accounts. A
// transaction's own date is "YYYY-MM-DD" (§5.1), so matching compares its
// first 7 characters. Mirrors internal/budget.Activity.
export function activity(
  transactions: Transaction[],
  onBudgetAccounts: Set<string>,
  categoryId: string,
  month: string,
): number {
  let total = 0;
  for (const transaction of transactions) {
    if (transaction.deleted_at !== null) continue;
    if (!onBudgetAccounts.has(transaction.account_id)) continue;
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
// internal/budget.RollupCategory, but also filters soft-deleted entries
// itself since it reads the raw local tables.
export function rollupCategory(
  entries: BudgetEntry[],
  transactions: Transaction[],
  onBudgetAccounts: Set<string>,
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
    if (transaction.category_id !== categoryId) continue;
    if (transaction.date.length >= 7) months.add(transaction.date.slice(0, 7));
  }

  const ordered = [...months].filter((m) => m <= month).sort();

  let previousAvailable = 0;
  let result: CategoryMonthFigures = { budgeted: 0, activity: 0, available: 0 };
  for (const m of ordered) {
    const budgeted = budgetedByMonth.get(m) ?? 0;
    const activityThisMonth = activity(transactions, onBudgetAccounts, categoryId, m);
    const availableThisMonth = available(previousAvailable, budgeted, activityThisMonth);
    previousAvailable = availableThisMonth;
    if (m === month) {
      result = { budgeted, activity: activityThisMonth, available: availableThisMonth };
    }
  }
  return result;
}

// Sums every non-deleted transaction in accountId dated on or before the
// last day of month (§5.3). "YYYY-MM-DD" dates order lexicographically, so
// comparing the month prefix is enough. Mirrors internal/budget.BalanceThrough.
export function balanceThrough(
  transactions: Transaction[],
  accountId: string,
  month: string,
): number {
  let total = 0;
  for (const transaction of transactions) {
    if (transaction.deleted_at !== null) continue;
    if (transaction.account_id !== accountId) continue;
    if (transaction.date.slice(0, 7) > month) continue;
    total += transaction.amount;
  }
  return total;
}

// Implements §5.3's "Available to Budget": on-budget balances through month
// minus what's still sitting in non-income envelopes, so that
// to_budget + Σ non-income available = Σ on-budget balance. Subtracting
// available rather than budgeted keeps categorized spending from being
// counted twice. Mirrors internal/api.BudgetHandler.Get.
export function toBudget(
  accounts: Account[],
  categoryGroups: CategoryGroup[],
  categories: Category[],
  transactions: Transaction[],
  budgetEntries: BudgetEntry[],
  month: string,
): number {
  const onBudget = onBudgetAccountIds(accounts);

  let total = 0;
  for (const accountId of onBudget) {
    total += balanceThrough(transactions, accountId, month);
  }

  const incomeGroupIds = new Set(
    categoryGroups.filter((g) => g.deleted_at === null && g.is_income).map((g) => g.id),
  );
  for (const category of categories) {
    if (category.deleted_at !== null || incomeGroupIds.has(category.group_id)) continue;
    total -= rollupCategory(budgetEntries, transactions, onBudget, category.id, month)
      .available;
  }

  return total;
}
