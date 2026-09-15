package db

import (
	"database/sql"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Applies every migration before 0010 by hand, seeds transactions, then lets
// Migrate run the date-column rebuild, so the rebuild is tested against
// existing data rather than an empty table.
func TestMigrate_TransactionsDateTextRebuildPreservesRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "argos.db")
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := conn.Exec(`CREATE TABLE schema_migrations (name TEXT PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") && e.Name() < "0010" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		contents, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := applyMigration(conn, name, string(contents)); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := conn.Exec(`
		INSERT INTO transactions (id, account_id, category_id, payee_id, parent_id, date, amount, cleared, notes, transfer_id, hlc_physical, hlc_counter, hlc_node_id, server_version, deleted_at)
		VALUES
			('t1', 'acc-1', 'cat-1', 'payee-1', NULL, '2026-01-15', -500, 1, 'groceries', NULL, 1000, 2, 'node-a', 7, NULL),
			('t2', 'acc-2', NULL, NULL, NULL, '2026-02-28', 1500, 0, '', 'xfer-1', 1001, 0, 'node-b', 8, 1234)`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	conn.Close()

	if err := Migrate(path); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	conn, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer conn.Close()

	var declared string
	if err := conn.QueryRow(`SELECT type FROM pragma_table_info('transactions') WHERE name = 'date'`).Scan(&declared); err != nil {
		t.Fatalf("table_info: %v", err)
	}
	if declared != "TEXT" {
		t.Fatalf("expected date declared TEXT, got %q", declared)
	}

	var indexes int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_transactions_account_id_deleted_at'`).Scan(&indexes); err != nil {
		t.Fatalf("index lookup: %v", err)
	}
	if indexes != 1 {
		t.Fatalf("expected idx_transactions_account_id_deleted_at to be recreated")
	}

	type txnRow struct {
		id, accountID                               string
		categoryID, payeeID, parentID, transferID   sql.NullString
		date, notes, nodeID                         string
		amount, cleared, physical, counter, version int64
		deletedAt                                   sql.NullInt64
	}
	rows, err := conn.Query(`SELECT id, account_id, category_id, payee_id, parent_id, date, amount, cleared, notes, transfer_id, hlc_physical, hlc_counter, hlc_node_id, server_version, deleted_at FROM transactions ORDER BY id`)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	defer rows.Close()
	var got []txnRow
	for rows.Next() {
		var r txnRow
		if err := rows.Scan(&r.id, &r.accountID, &r.categoryID, &r.payeeID, &r.parentID, &r.date, &r.amount, &r.cleared, &r.notes, &r.transferID,
			&r.physical, &r.counter, &r.nodeID, &r.version, &r.deletedAt); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, r)
	}

	want := []txnRow{
		{id: "t1", accountID: "acc-1", categoryID: sql.NullString{String: "cat-1", Valid: true}, payeeID: sql.NullString{String: "payee-1", Valid: true},
			date: "2026-01-15", amount: -500, cleared: 1, notes: "groceries", physical: 1000, counter: 2, nodeID: "node-a", version: 7},
		{id: "t2", accountID: "acc-2", transferID: sql.NullString{String: "xfer-1", Valid: true},
			date: "2026-02-28", amount: 1500, cleared: 0, notes: "", physical: 1001, counter: 0, nodeID: "node-b", version: 8,
			deletedAt: sql.NullInt64{Int64: 1234, Valid: true}},
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d rows, got %d: %+v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d changed by the rebuild\n  want: %+v\n  got:  %+v", i, want[i], got[i])
		}
	}
}
