package db

import (
	"context"
	"database/sql"
	"errors"
)

// ErrNotFound is returned when a lookup by id finds no non-deleted row.
var ErrNotFound = errors.New("not found")

// ErrAlreadyExists is returned when a create supplies an id that's already in use.
var ErrAlreadyExists = errors.New("already exists")

// Account is the full accounts row: business columns (§5.2) plus the sync
// metadata columns (§5.1) present on every syncable table.
type Account struct {
	ID            string
	Name          string
	Type          string
	OnBudget      bool
	Closed        bool
	Currency      string
	Notes         sql.NullString
	HLCPhysical   int64
	HLCCounter    int64
	HLCNodeID     string
	ServerVersion int64
	DeletedAt     sql.NullInt64
}

// NewAccount carries everything the caller (the writing device) must supply
// to create an account. id and the hlc_* triple are the device's own —
// never server-assigned (§5.1) — the server only assigns server_version.
type NewAccount struct {
	ID          string
	Name        string
	Type        string
	OnBudget    bool
	Closed      bool
	Currency    string
	Notes       sql.NullString
	HLCPhysical int64
	HLCCounter  int64
	HLCNodeID   string
}

// AccountUpdate carries a partial update: nil fields are left unchanged.
// The hlc_* triple is required on every update, since editing a row is
// itself a new device-stamped write.
type AccountUpdate struct {
	Name        *string
	Type        *string
	OnBudget    *bool
	Closed      *bool
	Currency    *string
	Notes       *sql.NullString
	HLCPhysical int64
	HLCCounter  int64
	HLCNodeID   string
}

const accountColumns = `id, name, type, on_budget, closed, currency, notes, hlc_physical, hlc_counter, hlc_node_id, server_version, deleted_at`

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanAccount(s scanner) (Account, error) {
	var a Account
	var onBudget, closed int64
	if err := s.Scan(&a.ID, &a.Name, &a.Type, &onBudget, &closed, &a.Currency, &a.Notes,
		&a.HLCPhysical, &a.HLCCounter, &a.HLCNodeID, &a.ServerVersion, &a.DeletedAt); err != nil {
		return Account{}, err
	}
	a.OnBudget = onBudget != 0
	a.Closed = closed != 0
	return a, nil
}

// ListAccounts returns every non-deleted account, ordered by name.
func ListAccounts(ctx context.Context, conn *sql.DB) ([]Account, error) {
	rows, err := conn.QueryContext(ctx, `SELECT `+accountColumns+` FROM accounts WHERE deleted_at IS NULL ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	accounts := make([]Account, 0)
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, a)
	}
	return accounts, rows.Err()
}

// GetAccount returns the non-deleted account with the given id, or ErrNotFound.
func GetAccount(ctx context.Context, conn *sql.DB, id string) (Account, error) {
	row := conn.QueryRowContext(ctx, `SELECT `+accountColumns+` FROM accounts WHERE id = ? AND deleted_at IS NULL`, id)
	a, err := scanAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	if err != nil {
		return Account{}, err
	}
	return a, nil
}

func getAccountTx(ctx context.Context, tx *sql.Tx, id string) (Account, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+accountColumns+` FROM accounts WHERE id = ? AND deleted_at IS NULL`, id)
	a, err := scanAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	return a, err
}

// nextServerVersion computes the next value of the strictly-increasing,
// whole-database version counter (§5.1) from the tables that carry it —
// there is no dedicated sequence table, so it's derived from current data.
func nextServerVersion(ctx context.Context, tx *sql.Tx) (int64, error) {
	var version int64
	err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(v), 0) + 1 FROM (
			SELECT MAX(server_version) AS v FROM accounts
			UNION ALL SELECT MAX(server_version) FROM category_groups
			UNION ALL SELECT MAX(server_version) FROM categories
			UNION ALL SELECT MAX(server_version) FROM payees
			UNION ALL SELECT MAX(server_version) FROM transactions
			UNION ALL SELECT MAX(server_version) FROM budget_entries
		)`).Scan(&version)
	return version, err
}

// CreateAccount inserts a new account with a freshly assigned server_version.
// Returns ErrAlreadyExists if in.ID is already in use.
func CreateAccount(ctx context.Context, conn *sql.DB, in NewAccount) (Account, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return Account{}, err
	}
	defer tx.Rollback()

	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE id = ?)`, in.ID).Scan(&exists); err != nil {
		return Account{}, err
	}
	if exists {
		return Account{}, ErrAlreadyExists
	}

	version, err := nextServerVersion(ctx, tx)
	if err != nil {
		return Account{}, err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO accounts (id, name, type, on_budget, closed, currency, notes, hlc_physical, hlc_counter, hlc_node_id, server_version, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
		in.ID, in.Name, in.Type, in.OnBudget, in.Closed, in.Currency, in.Notes, in.HLCPhysical, in.HLCCounter, in.HLCNodeID, version)
	if err != nil {
		return Account{}, err
	}

	row := tx.QueryRowContext(ctx, `SELECT `+accountColumns+` FROM accounts WHERE id = ?`, in.ID)
	a, err := scanAccount(row)
	if err != nil {
		return Account{}, err
	}

	if err := tx.Commit(); err != nil {
		return Account{}, err
	}
	return a, nil
}

// UpdateAccount applies a partial update to the non-deleted account with the
// given id, assigning it a fresh server_version. Returns ErrNotFound if no
// such account exists, or ErrStaleWrite if the stored row is newer (§7.1).
func UpdateAccount(ctx context.Context, conn *sql.DB, id string, in AccountUpdate) (Account, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return Account{}, err
	}
	defer tx.Rollback()

	current, err := scanAccount(tx.QueryRowContext(ctx, `SELECT `+accountColumns+` FROM accounts WHERE id = ? AND deleted_at IS NULL`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	if err != nil {
		return Account{}, err
	}
	if err := checkNotStaleTx(ctx, tx, "accounts", id, in.HLCPhysical, in.HLCCounter, in.HLCNodeID); err != nil {
		return Account{}, err
	}

	if in.Name != nil {
		current.Name = *in.Name
	}
	if in.Type != nil {
		current.Type = *in.Type
	}
	if in.OnBudget != nil {
		current.OnBudget = *in.OnBudget
	}
	if in.Closed != nil {
		current.Closed = *in.Closed
	}
	if in.Currency != nil {
		current.Currency = *in.Currency
	}
	if in.Notes != nil {
		current.Notes = *in.Notes
	}

	version, err := nextServerVersion(ctx, tx)
	if err != nil {
		return Account{}, err
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE accounts
		SET name = ?, type = ?, on_budget = ?, closed = ?, currency = ?, notes = ?,
		    hlc_physical = ?, hlc_counter = ?, hlc_node_id = ?, server_version = ?
		WHERE id = ?`,
		current.Name, current.Type, current.OnBudget, current.Closed, current.Currency, current.Notes,
		in.HLCPhysical, in.HLCCounter, in.HLCNodeID, version, id)
	if err != nil {
		return Account{}, err
	}

	row := tx.QueryRowContext(ctx, `SELECT `+accountColumns+` FROM accounts WHERE id = ?`, id)
	a, err := scanAccount(row)
	if err != nil {
		return Account{}, err
	}

	if err := tx.Commit(); err != nil {
		return Account{}, err
	}
	return a, nil
}
