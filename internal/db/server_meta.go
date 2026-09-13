package db

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
)

// initialSchemaVersion is the schema_version stored the first time
// server_meta is populated (§5.2). It only ever moves forward from here, on
// a future breaking schema change.
const initialSchemaVersion = 1

// ServerMeta is the single server_meta row (§5.2): the server's sync
// identity and current schema version, copied into every /sync response
// (§2.4) so a client can detect a reset or a schema mismatch.
type ServerMeta struct {
	SyncID        string
	SchemaVersion int64
}

// GetOrInitServerMeta returns the server's server_meta row, generating and
// storing it on first use (an empty table, i.e. first startup, §5.2): a
// fresh random sync_id and schema_version 1. Later calls just return the
// existing row unchanged.
func GetOrInitServerMeta(ctx context.Context, conn *sql.DB) (ServerMeta, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return ServerMeta{}, err
	}
	defer tx.Rollback()

	var meta ServerMeta
	err = tx.QueryRowContext(ctx, `SELECT sync_id, schema_version FROM server_meta LIMIT 1`).Scan(&meta.SyncID, &meta.SchemaVersion)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		meta = ServerMeta{SyncID: uuid.NewString(), SchemaVersion: initialSchemaVersion}
		if _, err := tx.ExecContext(ctx, `INSERT INTO server_meta (sync_id, schema_version) VALUES (?, ?)`, meta.SyncID, meta.SchemaVersion); err != nil {
			return ServerMeta{}, err
		}
	case err != nil:
		return ServerMeta{}, err
	}

	if err := tx.Commit(); err != nil {
		return ServerMeta{}, err
	}
	return meta, nil
}
