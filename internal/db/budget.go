package db

import (
	"context"
	"database/sql"
	"errors"
)

// BudgetEntry is a budget_entries row (§5.2) plus its sync metadata (§5.1).
type BudgetEntry struct {
	ID            string
	CategoryID    string
	Month         string
	Budgeted      int64
	HLCPhysical   int64
	HLCCounter    int64
	HLCNodeID     string
	ServerVersion int64
	DeletedAt     sql.NullInt64
}

const budgetEntryColumns = `id, category_id, month, budgeted, hlc_physical, hlc_counter, hlc_node_id, server_version, deleted_at`

func scanBudgetEntry(s scanner) (BudgetEntry, error) {
	var e BudgetEntry
	if err := s.Scan(&e.ID, &e.CategoryID, &e.Month, &e.Budgeted,
		&e.HLCPhysical, &e.HLCCounter, &e.HLCNodeID, &e.ServerVersion, &e.DeletedAt); err != nil {
		return BudgetEntry{}, err
	}
	return e, nil
}

// ListBudgetEntriesForCategory returns every non-deleted budget_entries row
// for categoryID, across all months. Computing available for any one month
// (§5.3) requires walking the category's whole history, not just the
// target month, so callers fetch it all and do that walk themselves.
func ListBudgetEntriesForCategory(ctx context.Context, conn *sql.DB, categoryID string) ([]BudgetEntry, error) {
	rows, err := conn.QueryContext(ctx, `SELECT `+budgetEntryColumns+` FROM budget_entries WHERE category_id = ? AND deleted_at IS NULL`, categoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]BudgetEntry, 0)
	for rows.Next() {
		e, err := scanBudgetEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListTransactionsForCategory returns every non-deleted transaction whose
// category_id is categoryID, across all accounts — needed to compute
// activity for any month (§5.3).
func ListTransactionsForCategory(ctx context.Context, conn *sql.DB, categoryID string) ([]Transaction, error) {
	rows, err := conn.QueryContext(ctx, `SELECT `+transactionColumns+` FROM transactions WHERE category_id = ? AND deleted_at IS NULL`, categoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Transaction, 0)
	for rows.Next() {
		t, err := scanTransaction(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ErrIncomeCategoryNotBudgetable is returned when a budget entry targets a
// category in the income group. Income reaches to_budget through account
// balances (§5.3), so an income budget entry would be counted nowhere.
var ErrIncomeCategoryNotBudgetable = errors.New("income-group categories cannot be budgeted")

func rejectIncomeCategoryTx(ctx context.Context, tx *sql.Tx, categoryID string) error {
	var isIncome int64
	err := tx.QueryRowContext(ctx, `
		SELECT g.is_income FROM categories c JOIN category_groups g ON g.id = c.group_id
		WHERE c.id = ?`, categoryID).Scan(&isIncome)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if isIncome != 0 {
		return ErrIncomeCategoryNotBudgetable
	}
	return nil
}

func getBudgetEntryTx(ctx context.Context, tx *sql.Tx, categoryID, month string) (BudgetEntry, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+budgetEntryColumns+` FROM budget_entries WHERE category_id = ? AND month = ? AND deleted_at IS NULL`, categoryID, month)
	e, err := scanBudgetEntry(row)
	if errors.Is(err, sql.ErrNoRows) {
		return BudgetEntry{}, ErrNotFound
	}
	return e, err
}

// SetBudgetedAmount upserts categoryID's budgeted amount for month (§7.3),
// respecting the unique constraint on (category_id, month) (§5.2): an
// existing row is updated in place — never deleted and reinserted, so its
// id survives across edits — and a new row (with the caller-supplied id)
// is inserted only if none exists yet. Per §5.2's note, setting budgeted to
// 0 soft-deletes the row instead of storing a zero (a no-op if no row
// exists for that category/month yet), which is what keeps §5.4's "any row
// exists" in-use signal meaningful over time. Returns ErrCategoryNotFound
// if categoryID doesn't name an existing, non-deleted category.
func SetBudgetedAmount(ctx context.Context, conn *sql.DB, id, categoryID, month string, budgeted, hlcPhysical, hlcCounter int64, hlcNodeID string) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := getCategoryTx(ctx, tx, categoryID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return ErrCategoryNotFound
		}
		return err
	}
	if err := rejectIncomeCategoryTx(ctx, tx, categoryID); err != nil {
		return err
	}

	existing, err := getBudgetEntryTx(ctx, tx, categoryID, month)
	switch {
	case errors.Is(err, ErrNotFound):
		if budgeted == 0 {
			return tx.Commit()
		}
		version, err := nextServerVersion(ctx, tx)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO budget_entries (id, category_id, month, budgeted, hlc_physical, hlc_counter, hlc_node_id, server_version, deleted_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
			id, categoryID, month, budgeted, hlcPhysical, hlcCounter, hlcNodeID, version); err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		version, err := nextServerVersion(ctx, tx)
		if err != nil {
			return err
		}
		if budgeted == 0 {
			if _, err := tx.ExecContext(ctx, `
				UPDATE budget_entries
				SET deleted_at = ?, hlc_physical = ?, hlc_counter = ?, hlc_node_id = ?, server_version = ?
				WHERE id = ?`,
				hlcPhysical, hlcPhysical, hlcCounter, hlcNodeID, version, existing.ID); err != nil {
				return err
			}
		} else {
			if _, err := tx.ExecContext(ctx, `
				UPDATE budget_entries
				SET budgeted = ?, hlc_physical = ?, hlc_counter = ?, hlc_node_id = ?, server_version = ?
				WHERE id = ?`,
				budgeted, hlcPhysical, hlcCounter, hlcNodeID, version, existing.ID); err != nil {
				return err
			}
		}
	}

	return tx.Commit()
}
