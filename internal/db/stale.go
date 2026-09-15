package db

import (
	"context"
	"database/sql"
	"errors"

	hlc "argos/internal/sync"
)

// ErrStaleWrite is returned by a REST write whose HLC is not strictly greater
// than a row it would modify (§7.1). REST follows the same last-write-wins
// rule as /sync (§2.3), so an older write can't overwrite a newer one or lower
// a row's HLC.
var ErrStaleWrite = errors.New("stale write: a row it would modify has an equal or newer HLC")

// ErrTransferLegFieldImmutable is returned when a REST update tries to change
// account_id or category_id on a transfer leg (§7.1).
var ErrTransferLegFieldImmutable = errors.New("account_id and category_id cannot be changed on a transfer leg")

// checkNotStaleTx returns ErrStaleWrite if the row with the given id in table
// has an HLC equal to or greater than the incoming one. A missing row is not
// stale.
func checkNotStaleTx(ctx context.Context, tx *sql.Tx, table, id string, physical, counter int64, nodeID string) error {
	return checkRowsNotStaleTx(ctx, tx,
		`SELECT hlc_physical, hlc_counter, hlc_node_id FROM `+table+` WHERE id = ?`, []any{id},
		physical, counter, nodeID)
}

// checkRowsNotStaleTx is checkNotStaleTx for every row a query selects
// (hlc_physical, hlc_counter, hlc_node_id) — used for bulk moves such as a
// reassignment, which must be rejected entirely if any moved row is newer.
func checkRowsNotStaleTx(ctx context.Context, tx *sql.Tx, query string, args []any, physical, counter int64, nodeID string) error {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	incoming := hlc.HLC{Physical: physical, Counter: counter, NodeID: nodeID}
	for rows.Next() {
		var stored hlc.HLC
		if err := rows.Scan(&stored.Physical, &stored.Counter, &stored.NodeID); err != nil {
			return err
		}
		if hlc.Compare(stored, incoming) >= 0 {
			return ErrStaleWrite
		}
	}
	return rows.Err()
}
