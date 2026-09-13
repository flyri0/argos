package db

import (
	"context"
	"database/sql"
	"errors"
)

// ErrCategoryGroupNotFound is returned when a category references a group
// id that doesn't exist (or is soft-deleted).
var ErrCategoryGroupNotFound = errors.New("category group not found")

// ErrCategoryInUse is returned from DeleteCategory when the category has
// referencing transactions or budget_entries and no reassignTo was given.
var ErrCategoryInUse = errors.New("category in use")

// ErrReassignTargetNotFound is returned when reassignTo doesn't name an
// existing, non-deleted category.
var ErrReassignTargetNotFound = errors.New("reassign target not found")

// CategoryGroup is a category_groups row (§5.2) plus its sync metadata (§5.1).
type CategoryGroup struct {
	ID            string
	Name          string
	IsIncome      bool
	SortOrder     int
	HLCPhysical   int64
	HLCCounter    int64
	HLCNodeID     string
	ServerVersion int64
	DeletedAt     sql.NullInt64
}

// Category is a categories row (§5.2) plus its sync metadata (§5.1).
type Category struct {
	ID            string
	GroupID       string
	Name          string
	Hidden        bool
	SortOrder     int
	Notes         sql.NullString
	HLCPhysical   int64
	HLCCounter    int64
	HLCNodeID     string
	ServerVersion int64
	DeletedAt     sql.NullInt64
}

// NewCategory carries everything the caller must supply to create a
// category. id and the hlc_* triple are the device's own, never
// server-assigned (§5.1).
type NewCategory struct {
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

// CategoryUpdate carries a partial normal update (rename/hide/reorder,
// §7): nil fields are left unchanged. Moving a category to a different
// group is not part of this update — not asked for by this prompt.
type CategoryUpdate struct {
	Name        *string
	Hidden      *bool
	SortOrder   *int
	Notes       *sql.NullString
	HLCPhysical int64
	HLCCounter  int64
	HLCNodeID   string
}

const categoryGroupColumns = `id, name, is_income, sort_order, hlc_physical, hlc_counter, hlc_node_id, server_version, deleted_at`

func scanCategoryGroup(s scanner) (CategoryGroup, error) {
	var g CategoryGroup
	var isIncome int64
	if err := s.Scan(&g.ID, &g.Name, &isIncome, &g.SortOrder,
		&g.HLCPhysical, &g.HLCCounter, &g.HLCNodeID, &g.ServerVersion, &g.DeletedAt); err != nil {
		return CategoryGroup{}, err
	}
	g.IsIncome = isIncome != 0
	return g, nil
}

// ListCategoryGroups returns every non-deleted category group, ordered by
// sort_order (§5.2).
func ListCategoryGroups(ctx context.Context, conn *sql.DB) ([]CategoryGroup, error) {
	rows, err := conn.QueryContext(ctx, `SELECT `+categoryGroupColumns+` FROM category_groups WHERE deleted_at IS NULL ORDER BY sort_order`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	groups := make([]CategoryGroup, 0)
	for rows.Next() {
		g, err := scanCategoryGroup(rows)
		if err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

// getCategoryGroupTx returns the non-deleted category group with the given
// id within tx, or ErrCategoryGroupNotFound.
func getCategoryGroupTx(ctx context.Context, tx *sql.Tx, id string) (CategoryGroup, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+categoryGroupColumns+` FROM category_groups WHERE id = ? AND deleted_at IS NULL`, id)
	g, err := scanCategoryGroup(row)
	if errors.Is(err, sql.ErrNoRows) {
		return CategoryGroup{}, ErrCategoryGroupNotFound
	}
	return g, err
}

const categoryColumns = `id, group_id, name, hidden, sort_order, notes, hlc_physical, hlc_counter, hlc_node_id, server_version, deleted_at`

func scanCategory(s scanner) (Category, error) {
	var c Category
	var hidden int64
	if err := s.Scan(&c.ID, &c.GroupID, &c.Name, &hidden, &c.SortOrder, &c.Notes,
		&c.HLCPhysical, &c.HLCCounter, &c.HLCNodeID, &c.ServerVersion, &c.DeletedAt); err != nil {
		return Category{}, err
	}
	c.Hidden = hidden != 0
	return c, nil
}

// ListCategories returns every non-deleted category, ordered by sort_order
// (§5.2). Callers bucket these by GroupID to nest them under their group.
func ListCategories(ctx context.Context, conn *sql.DB) ([]Category, error) {
	rows, err := conn.QueryContext(ctx, `SELECT `+categoryColumns+` FROM categories WHERE deleted_at IS NULL ORDER BY sort_order`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	categories := make([]Category, 0)
	for rows.Next() {
		c, err := scanCategory(rows)
		if err != nil {
			return nil, err
		}
		categories = append(categories, c)
	}
	return categories, rows.Err()
}

// GetCategory returns the non-deleted category with the given id, or ErrNotFound.
func GetCategory(ctx context.Context, conn *sql.DB, id string) (Category, error) {
	row := conn.QueryRowContext(ctx, `SELECT `+categoryColumns+` FROM categories WHERE id = ? AND deleted_at IS NULL`, id)
	c, err := scanCategory(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Category{}, ErrNotFound
	}
	return c, err
}

func getCategoryTx(ctx context.Context, tx *sql.Tx, id string) (Category, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+categoryColumns+` FROM categories WHERE id = ? AND deleted_at IS NULL`, id)
	c, err := scanCategory(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Category{}, ErrNotFound
	}
	return c, err
}

// CreateCategory inserts a new category inside an existing, non-deleted
// group, with a freshly assigned server_version. Returns
// ErrCategoryGroupNotFound if in.GroupID doesn't name such a group, or
// ErrAlreadyExists if in.ID is already in use.
func CreateCategory(ctx context.Context, conn *sql.DB, in NewCategory) (Category, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return Category{}, err
	}
	defer tx.Rollback()

	if _, err := getCategoryGroupTx(ctx, tx, in.GroupID); err != nil {
		return Category{}, err
	}

	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM categories WHERE id = ?)`, in.ID).Scan(&exists); err != nil {
		return Category{}, err
	}
	if exists {
		return Category{}, ErrAlreadyExists
	}

	version, err := nextServerVersion(ctx, tx)
	if err != nil {
		return Category{}, err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO categories (id, group_id, name, hidden, sort_order, notes, hlc_physical, hlc_counter, hlc_node_id, server_version, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
		in.ID, in.GroupID, in.Name, in.Hidden, in.SortOrder, in.Notes, in.HLCPhysical, in.HLCCounter, in.HLCNodeID, version)
	if err != nil {
		return Category{}, err
	}

	c, err := getCategoryTx(ctx, tx, in.ID)
	if err != nil {
		return Category{}, err
	}
	if err := tx.Commit(); err != nil {
		return Category{}, err
	}
	return c, nil
}

// UpdateCategory applies a partial normal update (rename/hide/reorder) to
// the non-deleted category with the given id, assigning it a fresh
// server_version. Returns ErrNotFound if no such category exists.
func UpdateCategory(ctx context.Context, conn *sql.DB, id string, in CategoryUpdate) (Category, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return Category{}, err
	}
	defer tx.Rollback()

	current, err := getCategoryTx(ctx, tx, id)
	if err != nil {
		return Category{}, err
	}

	if in.Name != nil {
		current.Name = *in.Name
	}
	if in.Hidden != nil {
		current.Hidden = *in.Hidden
	}
	if in.SortOrder != nil {
		current.SortOrder = *in.SortOrder
	}
	if in.Notes != nil {
		current.Notes = *in.Notes
	}

	version, err := nextServerVersion(ctx, tx)
	if err != nil {
		return Category{}, err
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE categories
		SET name = ?, hidden = ?, sort_order = ?, notes = ?,
		    hlc_physical = ?, hlc_counter = ?, hlc_node_id = ?, server_version = ?
		WHERE id = ?`,
		current.Name, current.Hidden, current.SortOrder, current.Notes,
		in.HLCPhysical, in.HLCCounter, in.HLCNodeID, version, id)
	if err != nil {
		return Category{}, err
	}

	c, err := getCategoryTx(ctx, tx, id)
	if err != nil {
		return Category{}, err
	}
	if err := tx.Commit(); err != nil {
		return Category{}, err
	}
	return c, nil
}

// budgetEntryRow is the minimal shape needed to reassign a category's
// budget_entries rows to another category (§5.4).
type budgetEntryRow struct {
	ID       string
	Month    string
	Budgeted int64
}

// DeleteCategory soft-deletes the non-deleted category with the given id
// (§5.1). Per §5.4, a category can only be deleted outright if it's never
// been used: no non-deleted transaction references it, and it holds no
// budget_entries row (any month, regardless of amount — leftover envelope
// balance lives there, and the rollover/available engine that would let us
// reduce this to a single number doesn't exist yet). If it has been used,
// reassignTo must name another existing, non-deleted category; every
// referencing transaction and every budget_entries row move to it in the
// same transaction, before the source is soft-deleted. Returns ErrNotFound,
// ErrCategoryInUse (reassignTo required), or ErrReassignTargetNotFound.
func DeleteCategory(ctx context.Context, conn *sql.DB, id string, reassignTo *string, hlcPhysical, hlcCounter int64, hlcNodeID string) (Category, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return Category{}, err
	}
	defer tx.Rollback()

	if _, err := getCategoryTx(ctx, tx, id); err != nil {
		return Category{}, err
	}

	var txnCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM transactions WHERE category_id = ? AND deleted_at IS NULL`, id).Scan(&txnCount); err != nil {
		return Category{}, err
	}

	entryRows, err := tx.QueryContext(ctx, `SELECT id, month, budgeted FROM budget_entries WHERE category_id = ? AND deleted_at IS NULL`, id)
	if err != nil {
		return Category{}, err
	}
	var entries []budgetEntryRow
	for entryRows.Next() {
		var e budgetEntryRow
		if err := entryRows.Scan(&e.ID, &e.Month, &e.Budgeted); err != nil {
			entryRows.Close()
			return Category{}, err
		}
		entries = append(entries, e)
	}
	if err := entryRows.Close(); err != nil {
		return Category{}, err
	}
	if err := entryRows.Err(); err != nil {
		return Category{}, err
	}

	inUse := txnCount > 0 || len(entries) > 0

	if inUse {
		if reassignTo == nil {
			return Category{}, ErrCategoryInUse
		}
		if _, err := getCategoryTx(ctx, tx, *reassignTo); err != nil {
			if errors.Is(err, ErrNotFound) {
				return Category{}, ErrReassignTargetNotFound
			}
			return Category{}, err
		}

		version, err := nextServerVersion(ctx, tx)
		if err != nil {
			return Category{}, err
		}

		if txnCount > 0 {
			if _, err := tx.ExecContext(ctx, `
				UPDATE transactions
				SET category_id = ?, hlc_physical = ?, hlc_counter = ?, hlc_node_id = ?, server_version = ?
				WHERE category_id = ? AND deleted_at IS NULL`,
				*reassignTo, hlcPhysical, hlcCounter, hlcNodeID, version, id); err != nil {
				return Category{}, err
			}
		}

		for _, e := range entries {
			var targetEntryID string
			var targetBudgeted int64
			err := tx.QueryRowContext(ctx, `SELECT id, budgeted FROM budget_entries WHERE category_id = ? AND month = ? AND deleted_at IS NULL`, *reassignTo, e.Month).
				Scan(&targetEntryID, &targetBudgeted)
			switch {
			case errors.Is(err, sql.ErrNoRows):
				if _, err := tx.ExecContext(ctx, `
					UPDATE budget_entries
					SET category_id = ?, hlc_physical = ?, hlc_counter = ?, hlc_node_id = ?, server_version = ?
					WHERE id = ?`,
					*reassignTo, hlcPhysical, hlcCounter, hlcNodeID, version, e.ID); err != nil {
					return Category{}, err
				}
			case err != nil:
				return Category{}, err
			default:
				if _, err := tx.ExecContext(ctx, `
					UPDATE budget_entries
					SET budgeted = ?, hlc_physical = ?, hlc_counter = ?, hlc_node_id = ?, server_version = ?
					WHERE id = ?`,
					targetBudgeted+e.Budgeted, hlcPhysical, hlcCounter, hlcNodeID, version, targetEntryID); err != nil {
					return Category{}, err
				}
				if _, err := tx.ExecContext(ctx, `
					UPDATE budget_entries
					SET deleted_at = ?, hlc_physical = ?, hlc_counter = ?, hlc_node_id = ?, server_version = ?
					WHERE id = ?`,
					hlcPhysical, hlcPhysical, hlcCounter, hlcNodeID, version, e.ID); err != nil {
					return Category{}, err
				}
			}
		}
	}

	version, err := nextServerVersion(ctx, tx)
	if err != nil {
		return Category{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE categories
		SET deleted_at = ?, hlc_physical = ?, hlc_counter = ?, hlc_node_id = ?, server_version = ?
		WHERE id = ?`,
		hlcPhysical, hlcPhysical, hlcCounter, hlcNodeID, version, id); err != nil {
		return Category{}, err
	}

	row := tx.QueryRowContext(ctx, `SELECT `+categoryColumns+` FROM categories WHERE id = ?`, id)
	c, err := scanCategory(row)
	if err != nil {
		return Category{}, err
	}
	if err := tx.Commit(); err != nil {
		return Category{}, err
	}
	return c, nil
}
