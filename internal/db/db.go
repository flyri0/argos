package db

import (
	"database/sql"
	"fmt"
	"strings"
)

// Open returns a ready-to-use handle to the SQLite file at path.
//
// _txlock=immediate makes every transaction opened via (*sql.DB).Begin /
// BeginTx acquire SQLite's write lock up front, so a read-then-write
// sequence (e.g. computing the next server_version, §5.1) is atomic against
// concurrent writers instead of racing on a lock upgrade. _busy_timeout
// makes a writer that loses that race wait instead of failing immediately.
func Open(path string) (*sql.DB, error) {
	dsn := path
	if strings.Contains(dsn, "?") {
		dsn += "&_txlock=immediate&_busy_timeout=5000"
	} else {
		dsn += "?_txlock=immediate&_busy_timeout=5000"
	}

	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	return conn, nil
}
