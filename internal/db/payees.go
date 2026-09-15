package db

import (
	"context"
	"database/sql"
	"errors"
)

// ErrPayeeInUse is returned from DeletePayee when the payee has referencing
// transactions and no reassignTo was given.
var ErrPayeeInUse = errors.New("payee in use")

// Payee is a payees row (§5.2) plus its sync metadata (§5.1).
type Payee struct {
	ID            string
	Name          string
	HLCPhysical   int64
	HLCCounter    int64
	HLCNodeID     string
	ServerVersion int64
	DeletedAt     sql.NullInt64
}

// NewPayee carries everything the caller must supply to create a payee.
// id and the hlc_* triple are the device's own, never server-assigned (§5.1).
type NewPayee struct {
	ID          string
	Name        string
	HLCPhysical int64
	HLCCounter  int64
	HLCNodeID   string
}

// PayeeUpdate carries a partial rename (§7.3): nil Name leaves it unchanged.
type PayeeUpdate struct {
	Name        *string
	HLCPhysical int64
	HLCCounter  int64
	HLCNodeID   string
}

const payeeColumns = `id, name, hlc_physical, hlc_counter, hlc_node_id, server_version, deleted_at`

func scanPayee(s scanner) (Payee, error) {
	var p Payee
	if err := s.Scan(&p.ID, &p.Name, &p.HLCPhysical, &p.HLCCounter, &p.HLCNodeID, &p.ServerVersion, &p.DeletedAt); err != nil {
		return Payee{}, err
	}
	return p, nil
}

// ListPayees returns every non-deleted payee, ordered by name.
func ListPayees(ctx context.Context, conn *sql.DB) ([]Payee, error) {
	rows, err := conn.QueryContext(ctx, `SELECT `+payeeColumns+` FROM payees WHERE deleted_at IS NULL ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	payees := make([]Payee, 0)
	for rows.Next() {
		p, err := scanPayee(rows)
		if err != nil {
			return nil, err
		}
		payees = append(payees, p)
	}
	return payees, rows.Err()
}

func getPayeeTx(ctx context.Context, tx *sql.Tx, id string) (Payee, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+payeeColumns+` FROM payees WHERE id = ? AND deleted_at IS NULL`, id)
	p, err := scanPayee(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Payee{}, ErrNotFound
	}
	return p, err
}

// CreatePayee inserts a new payee with a freshly assigned server_version.
// Returns ErrAlreadyExists if in.ID is already in use.
func CreatePayee(ctx context.Context, conn *sql.DB, in NewPayee) (Payee, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return Payee{}, err
	}
	defer tx.Rollback()

	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM payees WHERE id = ?)`, in.ID).Scan(&exists); err != nil {
		return Payee{}, err
	}
	if exists {
		return Payee{}, ErrAlreadyExists
	}

	version, err := nextServerVersion(ctx, tx)
	if err != nil {
		return Payee{}, err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO payees (id, name, hlc_physical, hlc_counter, hlc_node_id, server_version, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, NULL)`,
		in.ID, in.Name, in.HLCPhysical, in.HLCCounter, in.HLCNodeID, version)
	if err != nil {
		return Payee{}, err
	}

	p, err := getPayeeTx(ctx, tx, in.ID)
	if err != nil {
		return Payee{}, err
	}
	if err := tx.Commit(); err != nil {
		return Payee{}, err
	}
	return p, nil
}

// UpdatePayee applies a partial rename to the non-deleted payee with the
// given id, assigning it a fresh server_version. Returns ErrNotFound if no
// such payee exists.
func UpdatePayee(ctx context.Context, conn *sql.DB, id string, in PayeeUpdate) (Payee, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return Payee{}, err
	}
	defer tx.Rollback()

	current, err := getPayeeTx(ctx, tx, id)
	if err != nil {
		return Payee{}, err
	}
	if err := checkNotStaleTx(ctx, tx, "payees", id, in.HLCPhysical, in.HLCCounter, in.HLCNodeID); err != nil {
		return Payee{}, err
	}

	if in.Name != nil {
		current.Name = *in.Name
	}

	version, err := nextServerVersion(ctx, tx)
	if err != nil {
		return Payee{}, err
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE payees
		SET name = ?, hlc_physical = ?, hlc_counter = ?, hlc_node_id = ?, server_version = ?
		WHERE id = ?`,
		current.Name, in.HLCPhysical, in.HLCCounter, in.HLCNodeID, version, id)
	if err != nil {
		return Payee{}, err
	}

	p, err := getPayeeTx(ctx, tx, id)
	if err != nil {
		return Payee{}, err
	}
	if err := tx.Commit(); err != nil {
		return Payee{}, err
	}
	return p, nil
}

// DeletePayee soft-deletes the non-deleted payee with the given id (§5.1).
// Per §5.4, a payee can only be deleted outright if no non-deleted
// transaction references it — payees have no balance concept, so unlike
// DeleteCategory there's nothing beyond transactions to check or move. If
// it has been used, reassignTo must name another existing, non-deleted
// payee; every referencing transaction moves to it in the same transaction
// before the source is soft-deleted. Returns ErrNotFound, ErrPayeeInUse
// (reassignTo required), ErrReassignTargetNotFound, or ErrStaleWrite if any
// row it would modify is newer (§7.1).
func DeletePayee(ctx context.Context, conn *sql.DB, id string, reassignTo *string, hlcPhysical, hlcCounter int64, hlcNodeID string) (Payee, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return Payee{}, err
	}
	defer tx.Rollback()

	if _, err := getPayeeTx(ctx, tx, id); err != nil {
		return Payee{}, err
	}
	if err := checkNotStaleTx(ctx, tx, "payees", id, hlcPhysical, hlcCounter, hlcNodeID); err != nil {
		return Payee{}, err
	}

	var txnCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM transactions WHERE payee_id = ? AND deleted_at IS NULL`, id).Scan(&txnCount); err != nil {
		return Payee{}, err
	}

	if txnCount > 0 {
		if reassignTo == nil {
			return Payee{}, ErrPayeeInUse
		}
		if _, err := getPayeeTx(ctx, tx, *reassignTo); err != nil {
			if errors.Is(err, ErrNotFound) {
				return Payee{}, ErrReassignTargetNotFound
			}
			return Payee{}, err
		}
		if err := checkRowsNotStaleTx(ctx, tx,
			`SELECT hlc_physical, hlc_counter, hlc_node_id FROM transactions WHERE payee_id = ? AND deleted_at IS NULL`, []any{id},
			hlcPhysical, hlcCounter, hlcNodeID); err != nil {
			return Payee{}, err
		}

		version, err := nextServerVersion(ctx, tx)
		if err != nil {
			return Payee{}, err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE transactions
			SET payee_id = ?, hlc_physical = ?, hlc_counter = ?, hlc_node_id = ?, server_version = ?
			WHERE payee_id = ? AND deleted_at IS NULL`,
			*reassignTo, hlcPhysical, hlcCounter, hlcNodeID, version, id); err != nil {
			return Payee{}, err
		}
	}

	version, err := nextServerVersion(ctx, tx)
	if err != nil {
		return Payee{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE payees
		SET deleted_at = ?, hlc_physical = ?, hlc_counter = ?, hlc_node_id = ?, server_version = ?
		WHERE id = ?`,
		hlcPhysical, hlcPhysical, hlcCounter, hlcNodeID, version, id); err != nil {
		return Payee{}, err
	}

	row := tx.QueryRowContext(ctx, `SELECT `+payeeColumns+` FROM payees WHERE id = ?`, id)
	p, err := scanPayee(row)
	if err != nil {
		return Payee{}, err
	}
	if err := tx.Commit(); err != nil {
		return Payee{}, err
	}
	return p, nil
}
