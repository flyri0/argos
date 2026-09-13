import type { Transaction } from "./types";

// §5.3: account balance is never stored, always computed by summing
// transaction amounts. Filters out soft-deleted rows itself so a caller
// can pass a raw table read without pre-filtering.
export function computeBalance(transactions: Transaction[]): number {
  return transactions.reduce(
    (sum, transaction) =>
      transaction.deleted_at === null ? sum + transaction.amount : sum,
    0,
  );
}
