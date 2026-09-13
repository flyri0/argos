package db

import (
	"context"
	"database/sql"
)

// TransactionAmount is the minimal shape needed to compute an account's
// balance (§5.3) — raw data only; the filtering and summing rule lives in
// internal/budget, not here.
type TransactionAmount struct {
	AccountID string
	Amount    int64
	DeletedAt sql.NullInt64
}

// ListTransactionAmountsForAccount returns every transaction row for
// accountID, deleted or not — callers hand these to internal/budget to
// compute the actual balance.
func ListTransactionAmountsForAccount(ctx context.Context, conn *sql.DB, accountID string) ([]TransactionAmount, error) {
	rows, err := conn.QueryContext(ctx, `SELECT account_id, amount, deleted_at FROM transactions WHERE account_id = ?`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]TransactionAmount, 0)
	for rows.Next() {
		var t TransactionAmount
		if err := rows.Scan(&t.AccountID, &t.Amount, &t.DeletedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
