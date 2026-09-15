package db

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
)

// ErrAccountClosed is returned when a transaction is being created against
// an account with closed = true (§7.1).
var ErrAccountClosed = errors.New("account closed")

// ErrAccountNotFound, ErrCategoryNotFound, and ErrPayeeNotFound distinguish
// which foreign reference on a transaction failed to resolve, since a
// single create/update can reference all three at once.
var (
	ErrAccountNotFound  = errors.New("account not found")
	ErrCategoryNotFound = errors.New("category not found")
	ErrPayeeNotFound    = errors.New("payee not found")
)

// Transaction is a transactions row (§5.2) plus its sync metadata (§5.1).
// parent_id always scans as NULL in the MVP — splits aren't implemented.
type Transaction struct {
	ID            string
	AccountID     string
	CategoryID    sql.NullString
	PayeeID       sql.NullString
	ParentID      sql.NullString
	Date          string
	Amount        int64
	Cleared       bool
	Notes         string
	TransferID    sql.NullString
	HLCPhysical   int64
	HLCCounter    int64
	HLCNodeID     string
	ServerVersion int64
	DeletedAt     sql.NullInt64
}

// NewTransaction carries everything the caller must supply to create a
// non-transfer transaction. id and the hlc_* triple are the device's own,
// never server-assigned (§5.1).
type NewTransaction struct {
	ID          string
	AccountID   string
	CategoryID  sql.NullString
	PayeeID     sql.NullString
	Date        string
	Amount      int64
	Cleared     bool
	Notes       string
	HLCPhysical int64
	HLCCounter  int64
	HLCNodeID   string
}

// NewTransfer carries everything needed to create both sides of an
// inter-account transfer. Both ids and both HLC triples are the caller's
// own — each row is its own write and gets its own HLC stamp, not a shared
// one (per this milestone's instructions).
type NewTransfer struct {
	ID                    string
	AccountID             string
	TransferTransactionID string
	TransferAccountID     string
	PayeeID               sql.NullString
	Date                  string
	Amount                int64
	Cleared               bool
	Notes                 string
	HLCPhysical           int64
	HLCCounter            int64
	HLCNodeID             string
	TransferHLCPhysical   int64
	TransferHLCCounter    int64
	TransferHLCNodeID     string
}

// TransactionUpdate carries a partial update (§7.3): nil fields are left
// unchanged. transfer_id and parent_id are not updatable here — editing
// either would break the invariants a transfer or (future) split relies
// on, and neither is asked for by this prompt.
type TransactionUpdate struct {
	AccountID   *string
	CategoryID  *sql.NullString
	PayeeID     *sql.NullString
	Date        *string
	Amount      *int64
	Cleared     *bool
	Notes       *string
	HLCPhysical int64
	HLCCounter  int64
	HLCNodeID   string
}

// TransactionAmount is the minimal shape needed to compute an account's
// balance (§5.3) — raw data only; the filtering and summing rule lives in
// internal/budget, not here.
type TransactionAmount struct {
	AccountID string
	Date      string
	Amount    int64
	DeletedAt sql.NullInt64
}

// ListTransactionAmountsForAccount returns every transaction row for
// accountID, deleted or not — callers hand these to internal/budget to
// compute the actual balance.
func ListTransactionAmountsForAccount(ctx context.Context, conn *sql.DB, accountID string) ([]TransactionAmount, error) {
	rows, err := conn.QueryContext(ctx, `SELECT account_id, date, amount, deleted_at FROM transactions WHERE account_id = ?`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]TransactionAmount, 0)
	for rows.Next() {
		var t TransactionAmount
		if err := rows.Scan(&t.AccountID, &t.Date, &t.Amount, &t.DeletedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

const transactionColumns = `id, account_id, category_id, payee_id, parent_id, date, amount, cleared, notes, transfer_id, hlc_physical, hlc_counter, hlc_node_id, server_version, deleted_at`

func scanTransaction(s scanner) (Transaction, error) {
	var t Transaction
	var cleared int64
	if err := s.Scan(&t.ID, &t.AccountID, &t.CategoryID, &t.PayeeID, &t.ParentID, &t.Date, &t.Amount, &cleared, &t.Notes, &t.TransferID,
		&t.HLCPhysical, &t.HLCCounter, &t.HLCNodeID, &t.ServerVersion, &t.DeletedAt); err != nil {
		return Transaction{}, err
	}
	t.Cleared = cleared != 0
	return t, nil
}

// ListTransactionsForAccount returns every non-deleted transaction for
// accountID, most recent first.
func ListTransactionsForAccount(ctx context.Context, conn *sql.DB, accountID string) ([]Transaction, error) {
	rows, err := conn.QueryContext(ctx, `SELECT `+transactionColumns+` FROM transactions WHERE account_id = ? AND deleted_at IS NULL ORDER BY date DESC, id`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	transactions := make([]Transaction, 0)
	for rows.Next() {
		t, err := scanTransaction(rows)
		if err != nil {
			return nil, err
		}
		transactions = append(transactions, t)
	}
	return transactions, rows.Err()
}

// GetTransaction returns the non-deleted transaction with the given id, or ErrNotFound.
func GetTransaction(ctx context.Context, conn *sql.DB, id string) (Transaction, error) {
	row := conn.QueryRowContext(ctx, `SELECT `+transactionColumns+` FROM transactions WHERE id = ? AND deleted_at IS NULL`, id)
	t, err := scanTransaction(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Transaction{}, ErrNotFound
	}
	return t, err
}

func getTransactionTx(ctx context.Context, tx *sql.Tx, id string) (Transaction, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+transactionColumns+` FROM transactions WHERE id = ? AND deleted_at IS NULL`, id)
	t, err := scanTransaction(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Transaction{}, ErrNotFound
	}
	return t, err
}

// checkAccountUsable verifies accountID names an existing, non-deleted,
// non-closed account within tx. Returns ErrAccountNotFound or ErrAccountClosed.
func checkAccountUsable(ctx context.Context, tx *sql.Tx, accountID string) error {
	a, err := getAccountTx(ctx, tx, accountID)
	if errors.Is(err, ErrNotFound) {
		return ErrAccountNotFound
	}
	if err != nil {
		return err
	}
	if a.Closed {
		return ErrAccountClosed
	}
	return nil
}

// liveTransferSiblingTx returns the id of the other non-deleted leg sharing
// transferID, if any.
func liveTransferSiblingTx(ctx context.Context, tx *sql.Tx, transferID, id string) (string, bool, error) {
	var siblingID string
	err := tx.QueryRowContext(ctx, `SELECT id FROM transactions WHERE transfer_id = ? AND id != ? AND deleted_at IS NULL`, transferID, id).Scan(&siblingID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return siblingID, true, nil
}

func checkRowExistsTx(ctx context.Context, tx *sql.Tx, table, id string) (bool, error) {
	var exists bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM `+table+` WHERE id = ?)`, id).Scan(&exists)
	return exists, err
}

// CreateTransaction inserts a new, non-transfer transaction with a freshly
// assigned server_version. Returns ErrAccountNotFound/ErrAccountClosed,
// ErrCategoryNotFound, ErrPayeeNotFound, or ErrAlreadyExists.
func CreateTransaction(ctx context.Context, conn *sql.DB, in NewTransaction) (Transaction, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return Transaction{}, err
	}
	defer tx.Rollback()

	if err := checkAccountUsable(ctx, tx, in.AccountID); err != nil {
		return Transaction{}, err
	}
	if in.CategoryID.Valid {
		if _, err := getCategoryTx(ctx, tx, in.CategoryID.String); err != nil {
			if errors.Is(err, ErrNotFound) {
				return Transaction{}, ErrCategoryNotFound
			}
			return Transaction{}, err
		}
	}
	if in.PayeeID.Valid {
		if _, err := getPayeeTx(ctx, tx, in.PayeeID.String); err != nil {
			if errors.Is(err, ErrNotFound) {
				return Transaction{}, ErrPayeeNotFound
			}
			return Transaction{}, err
		}
	}

	exists, err := checkRowExistsTx(ctx, tx, "transactions", in.ID)
	if err != nil {
		return Transaction{}, err
	}
	if exists {
		return Transaction{}, ErrAlreadyExists
	}

	version, err := nextServerVersion(ctx, tx)
	if err != nil {
		return Transaction{}, err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO transactions (id, account_id, category_id, payee_id, parent_id, date, amount, cleared, notes, transfer_id, hlc_physical, hlc_counter, hlc_node_id, server_version, deleted_at)
		VALUES (?, ?, ?, ?, NULL, ?, ?, ?, ?, NULL, ?, ?, ?, ?, NULL)`,
		in.ID, in.AccountID, in.CategoryID, in.PayeeID, in.Date, in.Amount, in.Cleared, in.Notes, in.HLCPhysical, in.HLCCounter, in.HLCNodeID, version)
	if err != nil {
		return Transaction{}, err
	}

	t, err := getTransactionTx(ctx, tx, in.ID)
	if err != nil {
		return Transaction{}, err
	}
	if err := tx.Commit(); err != nil {
		return Transaction{}, err
	}
	return t, nil
}

// CreateTransfer inserts both sides of an inter-account transfer in one
// transaction, sharing a freshly generated transfer_id and carrying
// opposite amounts. Each row gets its own server_version. Returns
// ErrAccountNotFound/ErrAccountClosed (either account), ErrPayeeNotFound,
// or ErrAlreadyExists (either id).
func CreateTransfer(ctx context.Context, conn *sql.DB, in NewTransfer) (Transaction, Transaction, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return Transaction{}, Transaction{}, err
	}
	defer tx.Rollback()

	if err := checkAccountUsable(ctx, tx, in.AccountID); err != nil {
		return Transaction{}, Transaction{}, err
	}
	if err := checkAccountUsable(ctx, tx, in.TransferAccountID); err != nil {
		return Transaction{}, Transaction{}, err
	}
	if in.PayeeID.Valid {
		if _, err := getPayeeTx(ctx, tx, in.PayeeID.String); err != nil {
			if errors.Is(err, ErrNotFound) {
				return Transaction{}, Transaction{}, ErrPayeeNotFound
			}
			return Transaction{}, Transaction{}, err
		}
	}

	for _, id := range []string{in.ID, in.TransferTransactionID} {
		exists, err := checkRowExistsTx(ctx, tx, "transactions", id)
		if err != nil {
			return Transaction{}, Transaction{}, err
		}
		if exists {
			return Transaction{}, Transaction{}, ErrAlreadyExists
		}
	}

	transferID := uuid.NewString()

	version1, err := nextServerVersion(ctx, tx)
	if err != nil {
		return Transaction{}, Transaction{}, err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO transactions (id, account_id, category_id, payee_id, parent_id, date, amount, cleared, notes, transfer_id, hlc_physical, hlc_counter, hlc_node_id, server_version, deleted_at)
		VALUES (?, ?, NULL, ?, NULL, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
		in.ID, in.AccountID, in.PayeeID, in.Date, in.Amount, in.Cleared, in.Notes, transferID, in.HLCPhysical, in.HLCCounter, in.HLCNodeID, version1)
	if err != nil {
		return Transaction{}, Transaction{}, err
	}

	version2, err := nextServerVersion(ctx, tx)
	if err != nil {
		return Transaction{}, Transaction{}, err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO transactions (id, account_id, category_id, payee_id, parent_id, date, amount, cleared, notes, transfer_id, hlc_physical, hlc_counter, hlc_node_id, server_version, deleted_at)
		VALUES (?, ?, NULL, ?, NULL, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
		in.TransferTransactionID, in.TransferAccountID, in.PayeeID, in.Date, -in.Amount, in.Cleared, in.Notes, transferID, in.TransferHLCPhysical, in.TransferHLCCounter, in.TransferHLCNodeID, version2)
	if err != nil {
		return Transaction{}, Transaction{}, err
	}

	t1, err := getTransactionTx(ctx, tx, in.ID)
	if err != nil {
		return Transaction{}, Transaction{}, err
	}
	t2, err := getTransactionTx(ctx, tx, in.TransferTransactionID)
	if err != nil {
		return Transaction{}, Transaction{}, err
	}
	if err := tx.Commit(); err != nil {
		return Transaction{}, Transaction{}, err
	}
	return t1, t2, nil
}

// UpdateTransaction applies a partial update to the non-deleted transaction
// with the given id, assigning it a fresh server_version. On a transfer leg,
// date, payee_id, notes, and amount (negated) are mirrored to the live
// sibling and the pair is validated before commit (§7.1). Returns
// ErrNotFound, ErrAccountNotFound, ErrCategoryNotFound, ErrPayeeNotFound,
// ErrTransferLegFieldImmutable, ErrStaleWrite, or ErrTransferPairInvalid.
func UpdateTransaction(ctx context.Context, conn *sql.DB, id string, in TransactionUpdate) (Transaction, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return Transaction{}, err
	}
	defer tx.Rollback()

	current, err := getTransactionTx(ctx, tx, id)
	if err != nil {
		return Transaction{}, err
	}
	if err := checkNotStaleTx(ctx, tx, "transactions", id, in.HLCPhysical, in.HLCCounter, in.HLCNodeID); err != nil {
		return Transaction{}, err
	}
	if current.TransferID.Valid {
		if (in.AccountID != nil && *in.AccountID != current.AccountID) ||
			(in.CategoryID != nil && *in.CategoryID != current.CategoryID) {
			return Transaction{}, ErrTransferLegFieldImmutable
		}
	}

	if in.AccountID != nil {
		if _, err := getAccountTx(ctx, tx, *in.AccountID); err != nil {
			if errors.Is(err, ErrNotFound) {
				return Transaction{}, ErrAccountNotFound
			}
			return Transaction{}, err
		}
		current.AccountID = *in.AccountID
	}
	if in.CategoryID != nil {
		if in.CategoryID.Valid {
			if _, err := getCategoryTx(ctx, tx, in.CategoryID.String); err != nil {
				if errors.Is(err, ErrNotFound) {
					return Transaction{}, ErrCategoryNotFound
				}
				return Transaction{}, err
			}
		}
		current.CategoryID = *in.CategoryID
	}
	if in.PayeeID != nil {
		if in.PayeeID.Valid {
			if _, err := getPayeeTx(ctx, tx, in.PayeeID.String); err != nil {
				if errors.Is(err, ErrNotFound) {
					return Transaction{}, ErrPayeeNotFound
				}
				return Transaction{}, err
			}
		}
		current.PayeeID = *in.PayeeID
	}
	if in.Date != nil {
		current.Date = *in.Date
	}
	if in.Amount != nil {
		current.Amount = *in.Amount
	}
	if in.Cleared != nil {
		current.Cleared = *in.Cleared
	}
	if in.Notes != nil {
		current.Notes = *in.Notes
	}

	version, err := nextServerVersion(ctx, tx)
	if err != nil {
		return Transaction{}, err
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE transactions
		SET account_id = ?, category_id = ?, payee_id = ?, date = ?, amount = ?, cleared = ?, notes = ?,
		    hlc_physical = ?, hlc_counter = ?, hlc_node_id = ?, server_version = ?
		WHERE id = ?`,
		current.AccountID, current.CategoryID, current.PayeeID, current.Date, current.Amount, current.Cleared, current.Notes,
		in.HLCPhysical, in.HLCCounter, in.HLCNodeID, version, id)
	if err != nil {
		return Transaction{}, err
	}

	if current.TransferID.Valid {
		siblingID, found, err := liveTransferSiblingTx(ctx, tx, current.TransferID.String, id)
		if err != nil {
			return Transaction{}, err
		}
		if found {
			if err := checkNotStaleTx(ctx, tx, "transactions", siblingID, in.HLCPhysical, in.HLCCounter, in.HLCNodeID); err != nil {
				return Transaction{}, err
			}
			// cleared is per-leg; the shared fields travel with the pair.
			if _, err := tx.ExecContext(ctx, `
				UPDATE transactions
				SET date = ?, payee_id = ?, notes = ?, amount = ?,
				    hlc_physical = ?, hlc_counter = ?, hlc_node_id = ?, server_version = ?
				WHERE id = ?`,
				current.Date, current.PayeeID, current.Notes, -current.Amount,
				in.HLCPhysical, in.HLCCounter, in.HLCNodeID, version, siblingID); err != nil {
				return Transaction{}, err
			}
		}
		if err := CheckTransferPairTx(ctx, tx, current.TransferID.String); err != nil {
			return Transaction{}, err
		}
	}

	t, err := getTransactionTx(ctx, tx, id)
	if err != nil {
		return Transaction{}, err
	}
	if err := tx.Commit(); err != nil {
		return Transaction{}, err
	}
	return t, nil
}

// DeleteTransaction soft-deletes the non-deleted transaction with the given
// id (§5.1). On a transfer leg, the live sibling is soft-deleted in the same
// transaction with the same HLC, so the pair is never left half-deleted
// (§7.1). Returns ErrNotFound or ErrStaleWrite.
func DeleteTransaction(ctx context.Context, conn *sql.DB, id string, hlcPhysical, hlcCounter int64, hlcNodeID string) (Transaction, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return Transaction{}, err
	}
	defer tx.Rollback()

	current, err := getTransactionTx(ctx, tx, id)
	if err != nil {
		return Transaction{}, err
	}

	ids := []string{id}
	if current.TransferID.Valid {
		siblingID, found, err := liveTransferSiblingTx(ctx, tx, current.TransferID.String, id)
		if err != nil {
			return Transaction{}, err
		}
		if found {
			ids = append(ids, siblingID)
		}
	}
	for _, rowID := range ids {
		if err := checkNotStaleTx(ctx, tx, "transactions", rowID, hlcPhysical, hlcCounter, hlcNodeID); err != nil {
			return Transaction{}, err
		}
	}

	version, err := nextServerVersion(ctx, tx)
	if err != nil {
		return Transaction{}, err
	}
	for _, rowID := range ids {
		if _, err := tx.ExecContext(ctx, `
			UPDATE transactions
			SET deleted_at = ?, hlc_physical = ?, hlc_counter = ?, hlc_node_id = ?, server_version = ?
			WHERE id = ?`,
			hlcPhysical, hlcPhysical, hlcCounter, hlcNodeID, version, rowID); err != nil {
			return Transaction{}, err
		}
	}

	row := tx.QueryRowContext(ctx, `SELECT `+transactionColumns+` FROM transactions WHERE id = ?`, id)
	t, err := scanTransaction(row)
	if err != nil {
		return Transaction{}, err
	}
	if err := tx.Commit(); err != nil {
		return Transaction{}, err
	}
	return t, nil
}
