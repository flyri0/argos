package db

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
)

// ErrBootstrapClosed is returned by CreateFirstDevice when devices already
// has at least one row: the bootstrap window (§6.1) is permanently closed
// for this installation, regardless of how it got closed.
var ErrBootstrapClosed = errors.New("bootstrap window is closed: a device is already paired")

// Device is a paired device (§5.2). Not synced to clients.
type Device struct {
	ID         string
	Name       string
	TokenHash  string
	ApprovedAt int64
	LastSeenAt sql.NullInt64
	RevokedAt  sql.NullInt64
}

// DevicesEmpty reports whether the devices table has no rows at all — the
// condition that gates both the bootstrap setup code (§6.1) and this
// endpoint's availability.
func DevicesEmpty(ctx context.Context, conn *sql.DB) (bool, error) {
	var count int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM devices`).Scan(&count); err != nil {
		return false, err
	}
	return count == 0, nil
}

// NewFirstDevice carries what's needed to create the bootstrap device
// (§6.1). Unlike the syncable tables (§5.1), the id is server-generated —
// devices is server-side auth metadata, never written by a client directly.
type NewFirstDevice struct {
	Name       string
	TokenHash  string
	ApprovedAt int64
}

// CreateFirstDevice inserts the first devices row, generating its id,
// atomically with the check that devices is still empty — so two
// concurrent bootstrap requests can't both succeed. Returns
// ErrBootstrapClosed if a device already exists.
func CreateFirstDevice(ctx context.Context, conn *sql.DB, in NewFirstDevice) (Device, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return Device{}, err
	}
	defer tx.Rollback()

	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM devices`).Scan(&count); err != nil {
		return Device{}, err
	}
	if count > 0 {
		return Device{}, ErrBootstrapClosed
	}

	d := Device{ID: uuid.NewString(), Name: in.Name, TokenHash: in.TokenHash, ApprovedAt: in.ApprovedAt}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO devices (id, name, token_hash, approved_at, last_seen_at, revoked_at)
		VALUES (?, ?, ?, ?, NULL, NULL)`,
		d.ID, d.Name, d.TokenHash, d.ApprovedAt)
	if err != nil {
		return Device{}, err
	}

	if err := tx.Commit(); err != nil {
		return Device{}, err
	}
	return d, nil
}
