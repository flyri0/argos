package db

import (
	"context"
	"database/sql"
	"errors"
)

// ErrParentTransactionNotFound is returned when a transaction's parent_id
// (reserved for V1 splits, §5.2) names a transaction that doesn't exist.
var ErrParentTransactionNotFound = errors.New("parent transaction not found")

// ErrBudgetEntryConflict is returned when a budget_entries upsert's
// (category_id, month) pair already belongs to a different row (§5.2's
// unique constraint) — the incoming id can't be reconciled with the
// existing one without dropping data, so the mutation is rejected rather
// than silently overwriting it.
var ErrBudgetEntryConflict = errors.New("a budget entry for this category and month already exists under a different id")

// syncTables lists the tables /sync (§2.4) can mutate — every table in §5.2
// that carries the sync metadata columns (§5.1). server_meta and devices
// are deliberately excluded: neither is synced to clients.
var syncTables = map[string]bool{
	"accounts":        true,
	"category_groups": true,
	"categories":      true,
	"payees":          true,
	"transactions":    true,
	"budget_entries":  true,
}

// IsSyncTable reports whether table is one of the six syncable tables (§5.2)
// that /sync is allowed to read or write.
func IsSyncTable(table string) bool {
	return syncTables[table]
}

// SyncRowHLC is the current HLC triple of an existing syncable row, used to
// compare against an incoming mutation before applying it (§2.3).
type SyncRowHLC struct {
	Physical int64
	Counter  int64
	NodeID   string
}

// GetRowHLCTx returns the current HLC of the row with the given id in table,
// and whether such a row exists at all — a tombstone (deleted_at set) still
// carries a real HLC that a stale incoming mutation must not override, so
// deleted rows count as found. table must be one of syncTables.
func GetRowHLCTx(ctx context.Context, tx *sql.Tx, table, id string) (SyncRowHLC, bool, error) {
	var h SyncRowHLC
	err := tx.QueryRowContext(ctx, `SELECT hlc_physical, hlc_counter, hlc_node_id FROM `+table+` WHERE id = ?`, id).Scan(&h.Physical, &h.Counter, &h.NodeID)
	if errors.Is(err, sql.ErrNoRows) {
		return SyncRowHLC{}, false, nil
	}
	if err != nil {
		return SyncRowHLC{}, false, err
	}
	return h, true, nil
}

// SyncDelete carries the fields a /sync delete mutation's row supplies for
// any table (§2.4): the tombstone columns are identical across every
// syncable table, so one shape covers all of them.
type SyncDelete struct {
	ID          string
	DeletedAt   int64
	HLCPhysical int64
	HLCCounter  int64
	HLCNodeID   string
}

// SyncDeleteRow soft-deletes the row with the given id in table, assigning
// it a fresh server_version, regardless of whether it was already deleted
// (a re-applied tombstone is a harmless no-op change). Returns ErrNotFound
// if no row with that id exists in table at all, or ErrIncomeGroupRequired
// if table is "category_groups" and the row is the current income group
// (§5.2: it cannot be deleted). table must be one of syncTables — callers
// check IsSyncTable before calling.
func SyncDeleteRow(ctx context.Context, tx *sql.Tx, table string, in SyncDelete) error {
	exists, err := checkRowExistsTx(ctx, tx, table, in.ID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}

	if table == "category_groups" {
		var isIncome int64
		var deletedAt sql.NullInt64
		err := tx.QueryRowContext(ctx, `SELECT is_income, deleted_at FROM category_groups WHERE id = ?`, in.ID).Scan(&isIncome, &deletedAt)
		if err != nil {
			return err
		}
		if isIncome != 0 && !deletedAt.Valid {
			return ErrIncomeGroupRequired
		}
	}

	version, err := nextServerVersion(ctx, tx)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `UPDATE `+table+` SET deleted_at = ?, hlc_physical = ?, hlc_counter = ?, hlc_node_id = ?, server_version = ? WHERE id = ?`,
		in.DeletedAt, in.HLCPhysical, in.HLCCounter, in.HLCNodeID, version, in.ID)
	return err
}

// SyncAccount is the full-row upsert shape for /sync (§2.4): unlike
// AccountUpdate, every business column is required on every write, since a
// sync upsert always replaces the row's full current state rather than
// patching it.
type SyncAccount struct {
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

// SyncUpsertAccount inserts or fully replaces the accounts row identified by
// in.ID, assigning it a fresh server_version. A row that previously existed
// as a tombstone is revived (deleted_at cleared), since the upsert row
// shape carries no deleted_at of its own (§2.4) — only a delete mutation
// sets that.
func SyncUpsertAccount(ctx context.Context, tx *sql.Tx, in SyncAccount) error {
	version, err := nextServerVersion(ctx, tx)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO accounts (id, name, type, on_budget, closed, currency, notes, hlc_physical, hlc_counter, hlc_node_id, server_version, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name, type = excluded.type, on_budget = excluded.on_budget, closed = excluded.closed,
			currency = excluded.currency, notes = excluded.notes, hlc_physical = excluded.hlc_physical,
			hlc_counter = excluded.hlc_counter, hlc_node_id = excluded.hlc_node_id, server_version = excluded.server_version,
			deleted_at = NULL`,
		in.ID, in.Name, in.Type, in.OnBudget, in.Closed, in.Currency, in.Notes, in.HLCPhysical, in.HLCCounter, in.HLCNodeID, version)
	return err
}

// SyncCategoryGroup is the full-row upsert shape for /sync (§2.4).
type SyncCategoryGroup struct {
	ID          string
	Name        string
	IsIncome    bool
	SortOrder   int
	HLCPhysical int64
	HLCCounter  int64
	HLCNodeID   string
}

// ErrIncomeGroupExists is returned by SyncUpsertCategoryGroup when the
// mutation would mark a second, different category_groups row as income —
// §5.2 requires exactly one income group to exist.
var ErrIncomeGroupExists = errors.New("an income category group already exists")

// ErrIncomeGroupRequired is returned by SyncUpsertCategoryGroup or
// SyncDeleteRow when the mutation would leave zero income category groups —
// either by un-marking the current one or deleting it. §5.2 states the
// income group "cannot be deleted", and un-marking it is equivalent: both
// leave the budget with no income group at all.
var ErrIncomeGroupRequired = errors.New("the income category group cannot be deleted or unmarked")

// SyncUpsertCategoryGroup inserts or fully replaces the category_groups row
// identified by in.ID, assigning it a fresh server_version. Returns
// ErrIncomeGroupExists or ErrIncomeGroupRequired if the write would violate
// §5.2's "exactly one income group" invariant.
func SyncUpsertCategoryGroup(ctx context.Context, tx *sql.Tx, in SyncCategoryGroup) error {
	if in.IsIncome {
		var other string
		err := tx.QueryRowContext(ctx, `SELECT id FROM category_groups WHERE is_income = 1 AND deleted_at IS NULL AND id != ?`, in.ID).Scan(&other)
		if err == nil {
			return ErrIncomeGroupExists
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	} else {
		var currentIsIncome int64
		var deletedAt sql.NullInt64
		err := tx.QueryRowContext(ctx, `SELECT is_income, deleted_at FROM category_groups WHERE id = ?`, in.ID).Scan(&currentIsIncome, &deletedAt)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && currentIsIncome != 0 && !deletedAt.Valid {
			return ErrIncomeGroupRequired
		}
	}

	version, err := nextServerVersion(ctx, tx)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO category_groups (id, name, is_income, sort_order, hlc_physical, hlc_counter, hlc_node_id, server_version, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name, is_income = excluded.is_income, sort_order = excluded.sort_order,
			hlc_physical = excluded.hlc_physical, hlc_counter = excluded.hlc_counter, hlc_node_id = excluded.hlc_node_id,
			server_version = excluded.server_version, deleted_at = NULL`,
		in.ID, in.Name, in.IsIncome, in.SortOrder, in.HLCPhysical, in.HLCCounter, in.HLCNodeID, version)
	return err
}

// SyncCategory is the full-row upsert shape for /sync (§2.4).
type SyncCategory struct {
	ID          string
	GroupID     string
	Name        string
	Hidden      bool
	SortOrder   int
	Notes       sql.NullString
	HLCPhysical int64
	HLCCounter  int64
	HLCNodeID   string
}

// SyncUpsertCategory inserts or fully replaces the categories row identified
// by in.ID, assigning it a fresh server_version. Returns
// ErrCategoryGroupNotFound if in.GroupID doesn't name an existing group.
func SyncUpsertCategory(ctx context.Context, tx *sql.Tx, in SyncCategory) error {
	if _, err := getCategoryGroupTx(ctx, tx, in.GroupID); err != nil {
		return err
	}

	version, err := nextServerVersion(ctx, tx)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO categories (id, group_id, name, hidden, sort_order, notes, hlc_physical, hlc_counter, hlc_node_id, server_version, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)
		ON CONFLICT(id) DO UPDATE SET
			group_id = excluded.group_id, name = excluded.name, hidden = excluded.hidden, sort_order = excluded.sort_order,
			notes = excluded.notes, hlc_physical = excluded.hlc_physical, hlc_counter = excluded.hlc_counter,
			hlc_node_id = excluded.hlc_node_id, server_version = excluded.server_version, deleted_at = NULL`,
		in.ID, in.GroupID, in.Name, in.Hidden, in.SortOrder, in.Notes, in.HLCPhysical, in.HLCCounter, in.HLCNodeID, version)
	return err
}

// SyncPayee is the full-row upsert shape for /sync (§2.4).
type SyncPayee struct {
	ID          string
	Name        string
	HLCPhysical int64
	HLCCounter  int64
	HLCNodeID   string
}

// SyncUpsertPayee inserts or fully replaces the payees row identified by
// in.ID, assigning it a fresh server_version.
func SyncUpsertPayee(ctx context.Context, tx *sql.Tx, in SyncPayee) error {
	version, err := nextServerVersion(ctx, tx)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO payees (id, name, hlc_physical, hlc_counter, hlc_node_id, server_version, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, NULL)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name, hlc_physical = excluded.hlc_physical, hlc_counter = excluded.hlc_counter,
			hlc_node_id = excluded.hlc_node_id, server_version = excluded.server_version, deleted_at = NULL`,
		in.ID, in.Name, in.HLCPhysical, in.HLCCounter, in.HLCNodeID, version)
	return err
}

// SyncTransaction is the full-row upsert shape for /sync (§2.4). Unlike the
// REST create/update paths, transfers aren't a distinct shape here: each
// side of a transfer is its own upsert mutation (sharing transfer_id, per
// §5.2), bundled into one atomic group by the client (§2.3).
type SyncTransaction struct {
	ID          string
	AccountID   string
	CategoryID  sql.NullString
	PayeeID     sql.NullString
	ParentID    sql.NullString
	Date        string
	Amount      int64
	Cleared     bool
	Notes       string
	TransferID  sql.NullString
	HLCPhysical int64
	HLCCounter  int64
	HLCNodeID   string
}

// SyncUpsertTransaction inserts or fully replaces the transactions row
// identified by in.ID, assigning it a fresh server_version. Returns
// ErrAccountNotFound, ErrCategoryNotFound, ErrPayeeNotFound, or
// ErrParentTransactionNotFound if the corresponding reference doesn't name
// an existing row. Unlike the REST create path, a closed account does not
// reject the write here — per this milestone, mutations are applied as
// given, with no business-rule validation beyond referential existence.
func SyncUpsertTransaction(ctx context.Context, tx *sql.Tx, in SyncTransaction) error {
	exists, err := checkRowExistsTx(ctx, tx, "accounts", in.AccountID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrAccountNotFound
	}
	if in.CategoryID.Valid {
		exists, err := checkRowExistsTx(ctx, tx, "categories", in.CategoryID.String)
		if err != nil {
			return err
		}
		if !exists {
			return ErrCategoryNotFound
		}
	}
	if in.PayeeID.Valid {
		exists, err := checkRowExistsTx(ctx, tx, "payees", in.PayeeID.String)
		if err != nil {
			return err
		}
		if !exists {
			return ErrPayeeNotFound
		}
	}
	if in.ParentID.Valid {
		exists, err := checkRowExistsTx(ctx, tx, "transactions", in.ParentID.String)
		if err != nil {
			return err
		}
		if !exists {
			return ErrParentTransactionNotFound
		}
	}

	version, err := nextServerVersion(ctx, tx)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO transactions (id, account_id, category_id, payee_id, parent_id, date, amount, cleared, notes, transfer_id, hlc_physical, hlc_counter, hlc_node_id, server_version, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)
		ON CONFLICT(id) DO UPDATE SET
			account_id = excluded.account_id, category_id = excluded.category_id, payee_id = excluded.payee_id,
			parent_id = excluded.parent_id, date = excluded.date, amount = excluded.amount, cleared = excluded.cleared,
			notes = excluded.notes, transfer_id = excluded.transfer_id, hlc_physical = excluded.hlc_physical,
			hlc_counter = excluded.hlc_counter, hlc_node_id = excluded.hlc_node_id, server_version = excluded.server_version,
			deleted_at = NULL`,
		in.ID, in.AccountID, in.CategoryID, in.PayeeID, in.ParentID, in.Date, in.Amount, in.Cleared, in.Notes, in.TransferID,
		in.HLCPhysical, in.HLCCounter, in.HLCNodeID, version)
	return err
}

// SyncBudgetEntry is the full-row upsert shape for /sync (§2.4).
type SyncBudgetEntry struct {
	ID          string
	CategoryID  string
	Month       string
	Budgeted    int64
	HLCPhysical int64
	HLCCounter  int64
	HLCNodeID   string
}

// SyncUpsertBudgetEntry inserts or fully replaces the budget_entries row
// identified by in.ID, assigning it a fresh server_version. Returns
// ErrCategoryNotFound if in.CategoryID doesn't name an existing category, or
// ErrBudgetEntryConflict if (category_id, month) already belongs to a
// different row (§5.2's unique constraint).
func SyncUpsertBudgetEntry(ctx context.Context, tx *sql.Tx, in SyncBudgetEntry) error {
	exists, err := checkRowExistsTx(ctx, tx, "categories", in.CategoryID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrCategoryNotFound
	}

	var conflictingID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM budget_entries WHERE category_id = ? AND month = ? AND id != ?`, in.CategoryID, in.Month, in.ID).Scan(&conflictingID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		return ErrBudgetEntryConflict
	}

	version, err := nextServerVersion(ctx, tx)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO budget_entries (id, category_id, month, budgeted, hlc_physical, hlc_counter, hlc_node_id, server_version, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL)
		ON CONFLICT(id) DO UPDATE SET
			category_id = excluded.category_id, month = excluded.month, budgeted = excluded.budgeted,
			hlc_physical = excluded.hlc_physical, hlc_counter = excluded.hlc_counter, hlc_node_id = excluded.hlc_node_id,
			server_version = excluded.server_version, deleted_at = NULL`,
		in.ID, in.CategoryID, in.Month, in.Budgeted, in.HLCPhysical, in.HLCCounter, in.HLCNodeID, version)
	return err
}

// CurrentServerVersion returns the whole-database server_version high-water
// mark (§5.1) — the same value nextServerVersion would assign minus one, or
// 0 if no syncable row has ever been written.
func CurrentServerVersion(ctx context.Context, conn *sql.DB) (int64, error) {
	var version int64
	err := conn.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(v), 0) FROM (
			SELECT MAX(server_version) AS v FROM accounts
			UNION ALL SELECT MAX(server_version) FROM category_groups
			UNION ALL SELECT MAX(server_version) FROM categories
			UNION ALL SELECT MAX(server_version) FROM payees
			UNION ALL SELECT MAX(server_version) FROM transactions
			UNION ALL SELECT MAX(server_version) FROM budget_entries
		)`).Scan(&version)
	return version, err
}

// ListAccountsSince returns every accounts row (deleted or not — a
// tombstone is itself a change that must propagate, §2.3) with
// server_version greater than since, ordered by server_version.
func ListAccountsSince(ctx context.Context, conn *sql.DB, since int64) ([]Account, error) {
	rows, err := conn.QueryContext(ctx, `SELECT `+accountColumns+` FROM accounts WHERE server_version > ? ORDER BY server_version`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Account, 0)
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListCategoryGroupsSince returns every category_groups row (deleted or
// not) with server_version greater than since, ordered by server_version.
func ListCategoryGroupsSince(ctx context.Context, conn *sql.DB, since int64) ([]CategoryGroup, error) {
	rows, err := conn.QueryContext(ctx, `SELECT `+categoryGroupColumns+` FROM category_groups WHERE server_version > ? ORDER BY server_version`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]CategoryGroup, 0)
	for rows.Next() {
		g, err := scanCategoryGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ListCategoriesSince returns every categories row (deleted or not) with
// server_version greater than since, ordered by server_version.
func ListCategoriesSince(ctx context.Context, conn *sql.DB, since int64) ([]Category, error) {
	rows, err := conn.QueryContext(ctx, `SELECT `+categoryColumns+` FROM categories WHERE server_version > ? ORDER BY server_version`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Category, 0)
	for rows.Next() {
		c, err := scanCategory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListPayeesSince returns every payees row (deleted or not) with
// server_version greater than since, ordered by server_version.
func ListPayeesSince(ctx context.Context, conn *sql.DB, since int64) ([]Payee, error) {
	rows, err := conn.QueryContext(ctx, `SELECT `+payeeColumns+` FROM payees WHERE server_version > ? ORDER BY server_version`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Payee, 0)
	for rows.Next() {
		p, err := scanPayee(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListTransactionsSince returns every transactions row (deleted or not) with
// server_version greater than since, ordered by server_version.
func ListTransactionsSince(ctx context.Context, conn *sql.DB, since int64) ([]Transaction, error) {
	rows, err := conn.QueryContext(ctx, `SELECT `+transactionColumns+` FROM transactions WHERE server_version > ? ORDER BY server_version`, since)
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

// ListBudgetEntriesSince returns every budget_entries row (deleted or not)
// with server_version greater than since, ordered by server_version.
func ListBudgetEntriesSince(ctx context.Context, conn *sql.DB, since int64) ([]BudgetEntry, error) {
	rows, err := conn.QueryContext(ctx, `SELECT `+budgetEntryColumns+` FROM budget_entries WHERE server_version > ? ORDER BY server_version`, since)
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
