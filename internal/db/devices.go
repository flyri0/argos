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

const deviceColumns = "id, name, token_hash, approved_at, last_seen_at, revoked_at"

func scanDevice(row scanner) (Device, error) {
	var d Device
	err := row.Scan(&d.ID, &d.Name, &d.TokenHash, &d.ApprovedAt, &d.LastSeenAt, &d.RevokedAt)
	return d, err
}

// ListDevices returns every device (§6.2), including revoked ones — the
// settings page needs to show a revoked device's last known name and state,
// not just silently drop it from the list.
func ListDevices(ctx context.Context, conn *sql.DB) ([]Device, error) {
	rows, err := conn.QueryContext(ctx, `SELECT `+deviceColumns+` FROM devices ORDER BY approved_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// FindActiveDeviceByToken looks up the device whose token_hash matches, but
// only among non-revoked devices (§6.2: "the server checks it against
// devices.token_hash and rejects revoked or unknown tokens"). found is false
// for both an unknown hash and a revoked device — a caller that only needs
// to decide "may this request proceed" doesn't need to tell those apart.
func FindActiveDeviceByToken(ctx context.Context, conn *sql.DB, tokenHash string) (Device, bool, error) {
	row := conn.QueryRowContext(ctx, `SELECT `+deviceColumns+` FROM devices WHERE token_hash = ? AND revoked_at IS NULL`, tokenHash)
	d, err := scanDevice(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, false, nil
	}
	if err != nil {
		return Device{}, false, err
	}
	return d, true, nil
}

// TouchDeviceLastSeen records that id made an authenticated request at now.
func TouchDeviceLastSeen(ctx context.Context, conn *sql.DB, id string, now int64) error {
	_, err := conn.ExecContext(ctx, `UPDATE devices SET last_seen_at = ? WHERE id = ?`, now, id)
	return err
}

// NewDevice carries what's needed to create a device via the normal §6.2
// approve flow, as opposed to CreateFirstDevice's bootstrap-only path.
type NewDevice struct {
	Name       string
	TokenHash  string
	ApprovedAt int64
}

// CreateDevice inserts a new devices row for an approved subsequent device
// (§6.2). Unlike CreateFirstDevice, it has no "devices must be empty" gate —
// the caller (the approve handler) is responsible for having already
// verified the approving device's own token.
func CreateDevice(ctx context.Context, conn *sql.DB, in NewDevice) (Device, error) {
	d := Device{ID: uuid.NewString(), Name: in.Name, TokenHash: in.TokenHash, ApprovedAt: in.ApprovedAt}
	_, err := conn.ExecContext(ctx, `
		INSERT INTO devices (id, name, token_hash, approved_at, last_seen_at, revoked_at)
		VALUES (?, ?, ?, ?, NULL, NULL)`,
		d.ID, d.Name, d.TokenHash, d.ApprovedAt)
	if err != nil {
		return Device{}, err
	}
	return d, nil
}

func getDeviceTx(ctx context.Context, tx *sql.Tx, id string) (Device, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+deviceColumns+` FROM devices WHERE id = ?`, id)
	d, err := scanDevice(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, ErrNotFound
	}
	return d, err
}

// UpdateDeviceName renames a device (§6.2, §7.3). Returns ErrNotFound if id
// doesn't exist.
func UpdateDeviceName(ctx context.Context, conn *sql.DB, id, name string) (Device, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return Device{}, err
	}
	defer tx.Rollback()

	if _, err := getDeviceTx(ctx, tx, id); err != nil {
		return Device{}, err
	}

	if _, err := tx.ExecContext(ctx, `UPDATE devices SET name = ? WHERE id = ?`, name, id); err != nil {
		return Device{}, err
	}

	updated, err := getDeviceTx(ctx, tx, id)
	if err != nil {
		return Device{}, err
	}
	if err := tx.Commit(); err != nil {
		return Device{}, err
	}
	return updated, nil
}

// RevokeDevice sets revoked_at (§6.2: "immediately invalidates that
// device's token") — never a hard delete, per CLAUDE.md. Returns
// ErrNotFound if id doesn't exist. Revoking an already-revoked device just
// overwrites revoked_at with the new timestamp; there's no meaningful
// distinction to enforce between "revoke" and "revoke again".
func RevokeDevice(ctx context.Context, conn *sql.DB, id string, revokedAt int64) (Device, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return Device{}, err
	}
	defer tx.Rollback()

	if _, err := getDeviceTx(ctx, tx, id); err != nil {
		return Device{}, err
	}

	if _, err := tx.ExecContext(ctx, `UPDATE devices SET revoked_at = ? WHERE id = ?`, revokedAt, id); err != nil {
		return Device{}, err
	}

	updated, err := getDeviceTx(ctx, tx, id)
	if err != nil {
		return Device{}, err
	}
	if err := tx.Commit(); err != nil {
		return Device{}, err
	}
	return updated, nil
}
