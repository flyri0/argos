package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"argos/internal/db"
)

func TestBudgetGet_IncludesToBudget(t *testing.T) {
	conn := newTestDB(t)
	h := &BudgetHandler{DB: conn}
	sh := &SyncHandler{DB: conn}

	// One on-budget account with a $1,000.00 balance, one off-budget
	// account with a $5,000.00 balance that must NOT count toward
	// to_budget (§5.3), and one category group (category_groups has no
	// direct db.Create helper — it's only ever mutated via /sync, per
	// project_spec.md §5.2's note that there's no REST CRUD for it).
	postSync(t, sh, `{
		"since": 0,
		"mutations": [
			{"table": "accounts", "op": "upsert", "group_id": null, "row": {
				"id": "11111111-1111-1111-1111-111111111111",
				"name": "Checking", "type": "checking", "on_budget": true, "closed": false, "currency": "USD",
				"hlc_physical": 1000, "hlc_counter": 0, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
			}},
			{"table": "accounts", "op": "upsert", "group_id": null, "row": {
				"id": "77777777-7777-7777-7777-777777777777",
				"name": "Investment", "type": "investment", "on_budget": false, "closed": false, "currency": "USD",
				"hlc_physical": 1000, "hlc_counter": 1, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
			}},
			{"table": "category_groups", "op": "upsert", "group_id": null, "row": {
				"id": "33333333-3333-3333-3333-333333333333",
				"name": "Bills", "is_income": false, "sort_order": 0,
				"hlc_physical": 1000, "hlc_counter": 2, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
			}}
		]
	}`)

	category, err := db.CreateCategory(t.Context(), conn, db.NewCategory{
		ID:          "44444444-4444-4444-4444-444444444444",
		GroupID:     "33333333-3333-3333-3333-333333333333",
		Name:        "Groceries",
		HLCPhysical: 1000, HLCCounter: 3, HLCNodeID: "22222222-2222-2222-2222-222222222222",
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	if _, err := db.CreateTransaction(t.Context(), conn, db.NewTransaction{
		ID:          "55555555-5555-5555-5555-555555555555",
		AccountID:   "11111111-1111-1111-1111-111111111111",
		CategoryID:  sql.NullString{String: category.ID, Valid: true},
		Date:        "2026-01-15",
		Amount:      100000,
		HLCPhysical: 1000, HLCCounter: 4, HLCNodeID: "22222222-2222-2222-2222-222222222222",
	}); err != nil {
		t.Fatalf("CreateTransaction (on-budget): %v", err)
	}
	if _, err := db.CreateTransaction(t.Context(), conn, db.NewTransaction{
		ID:          "66666666-6666-6666-6666-666666666666",
		AccountID:   "77777777-7777-7777-7777-777777777777",
		Date:        "2026-01-15",
		Amount:      500000,
		HLCPhysical: 1000, HLCCounter: 5, HLCNodeID: "22222222-2222-2222-2222-222222222222",
	}); err != nil {
		t.Fatalf("CreateTransaction (off-budget): %v", err)
	}

	if err := db.SetBudgetedAmount(t.Context(), conn,
		"88888888-8888-8888-8888-888888888888", category.ID, "2026-01",
		20000, 1000, 6, "22222222-2222-2222-2222-222222222222"); err != nil {
		t.Fatalf("SetBudgetedAmount: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/budget/2026-01", nil)
	req.SetPathValue("month", "2026-01")
	w := httptest.NewRecorder()
	h.Get(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var out monthBudget
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// The 100000 inflow was categorized into Groceries (a non-income
	// category), so it sits in that envelope: available = 20000 budgeted +
	// 100000 activity = 120000. §5.3's invariant is to_budget + Σ non-income
	// available = Σ on-budget balance, so to_budget = 100000 - 120000 =
	// -20000. The off-budget account's 500000 balance appears in neither.
	if len(out.Categories) != 1 || out.Categories[0].Available != 120000 {
		t.Fatalf("expected the one category's available to be 120000, got %+v", out.Categories)
	}
	if out.ToBudget != -20000 {
		t.Fatalf("expected to_budget -20000, got %d", out.ToBudget)
	}
}

func TestBudgetGet_IncomeCategoryInflowReachesToBudgetThroughBalance(t *testing.T) {
	conn := newTestDB(t)
	h := &BudgetHandler{DB: conn}
	sh := &SyncHandler{DB: conn}

	postSync(t, sh, `{
		"since": 0,
		"mutations": [
			{"table": "accounts", "op": "upsert", "group_id": null, "row": {
				"id": "11111111-1111-1111-1111-111111111111",
				"name": "Checking", "type": "checking", "on_budget": true, "closed": false, "currency": "USD",
				"hlc_physical": 1000, "hlc_counter": 0, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
			}},
			{"table": "category_groups", "op": "upsert", "group_id": null, "row": {
				"id": "33333333-3333-3333-3333-333333333333",
				"name": "Income", "is_income": true, "sort_order": 0,
				"hlc_physical": 1000, "hlc_counter": 1, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
			}}
		]
	}`)
	if _, err := db.CreateCategory(t.Context(), conn, db.NewCategory{
		ID:          "44444444-4444-4444-4444-444444444444",
		GroupID:     "33333333-3333-3333-3333-333333333333",
		Name:        "Salary",
		HLCPhysical: 1000, HLCCounter: 2, HLCNodeID: "22222222-2222-2222-2222-222222222222",
	}); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	if _, err := db.CreateTransaction(t.Context(), conn, db.NewTransaction{
		ID:          "55555555-5555-5555-5555-555555555555",
		AccountID:   "11111111-1111-1111-1111-111111111111",
		CategoryID:  sql.NullString{String: "44444444-4444-4444-4444-444444444444", Valid: true},
		Date:        "2026-01-15",
		Amount:      100000,
		HLCPhysical: 1000, HLCCounter: 3, HLCNodeID: "22222222-2222-2222-2222-222222222222",
	}); err != nil {
		t.Fatalf("CreateTransaction: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/budget/2026-01", nil)
	req.SetPathValue("month", "2026-01")
	w := httptest.NewRecorder()
	h.Get(w, req)

	var out monthBudget
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Salary's available (100000) is not subtracted — the money is counted
	// once, through the balance.
	if out.ToBudget != 100000 {
		t.Fatalf("expected to_budget 100000, got %d", out.ToBudget)
	}
}

func TestBudgetSet_RejectsIncomeCategory(t *testing.T) {
	conn := newTestDB(t)
	h := &BudgetHandler{DB: conn}
	sh := &SyncHandler{DB: conn}

	postSync(t, sh, `{
		"since": 0,
		"mutations": [
			{"table": "category_groups", "op": "upsert", "group_id": null, "row": {
				"id": "33333333-3333-3333-3333-333333333333",
				"name": "Income", "is_income": true, "sort_order": 0,
				"hlc_physical": 1000, "hlc_counter": 0, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
			}},
			{"table": "categories", "op": "upsert", "group_id": null, "row": {
				"id": "44444444-4444-4444-4444-444444444444", "group_id": "33333333-3333-3333-3333-333333333333",
				"name": "Salary", "hidden": false, "sort_order": 0,
				"hlc_physical": 1000, "hlc_counter": 1, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
			}}
		]
	}`)

	req := httptest.NewRequest(http.MethodPut, "/api/budget/2026-01/44444444-4444-4444-4444-444444444444", strings.NewReader(`{
		"id": "88888888-8888-8888-8888-888888888888", "budgeted": 5000,
		"hlc_physical": 1000, "hlc_counter": 2, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
	}`))
	req.SetPathValue("month", "2026-01")
	req.SetPathValue("category_id", "44444444-4444-4444-4444-444444444444")
	w := httptest.NewRecorder()
	h.Set(w, req)

	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"VALIDATION_ERROR"`) {
		t.Fatalf("expected 400 VALIDATION_ERROR, got %d: %s", w.Code, w.Body.String())
	}

	resp := postSync(t, sh, `{
		"since": 0,
		"mutations": [
			{"table": "budget_entries", "op": "upsert", "group_id": null, "row": {
				"id": "88888888-8888-8888-8888-888888888888", "category_id": "44444444-4444-4444-4444-444444444444",
				"month": "2026-01", "budgeted": 5000,
				"hlc_physical": 1000, "hlc_counter": 3, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
			}}
		]
	}`)
	if len(resp.Results) != 1 || resp.Results[0].Status != "rejected_invalid" ||
		resp.Results[0].Error == nil || resp.Results[0].Error.Code != "SYNC_MUTATION_INVALID" {
		t.Fatalf("expected rejected_invalid SYNC_MUTATION_INVALID, got %+v", resp.Results)
	}
}

// budgetCategoriesSetup creates one non-income group with two categories.
func budgetCategoriesSetup(t *testing.T, conn *sql.DB) (source, target string) {
	t.Helper()
	postSync(t, &SyncHandler{DB: conn}, `{
		"since": 0,
		"mutations": [
			{"table": "category_groups", "op": "upsert", "group_id": null, "row": {
				"id": "33333333-3333-3333-3333-333333333333", "name": "Bills", "is_income": false, "sort_order": 0,
				"hlc_physical": 1000, "hlc_counter": 0, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
			}}
		]
	}`)
	source, target = "44444444-4444-4444-4444-444444444444", "99999999-9999-9999-9999-999999999999"
	for i, id := range []string{source, target} {
		if _, err := db.CreateCategory(t.Context(), conn, db.NewCategory{
			ID: id, GroupID: "33333333-3333-3333-3333-333333333333", Name: id,
			HLCPhysical: 1000, HLCCounter: int64(1 + i), HLCNodeID: "22222222-2222-2222-2222-222222222222",
		}); err != nil {
			t.Fatalf("CreateCategory: %v", err)
		}
	}
	return source, target
}

func TestSetBudgetedAmount_RebudgetAfterZeroRevivesRow(t *testing.T) {
	conn := newTestDB(t)
	category, _ := budgetCategoriesSetup(t, conn)
	const node = "22222222-2222-2222-2222-222222222222"

	steps := []struct {
		id       string
		budgeted int64
	}{
		{"88888888-8888-8888-8888-888888888888", 100},
		{"88888888-8888-8888-8888-888888888888", 0},
		// A fresh caller-supplied id must not trip the UNIQUE constraint
		// against the tombstone.
		{"77777777-7777-7777-7777-777777777777", 50},
	}
	for i, s := range steps {
		if err := db.SetBudgetedAmount(t.Context(), conn, s.id, category, "2026-01", s.budgeted, 2000, int64(i), node); err != nil {
			t.Fatalf("SetBudgetedAmount step %d (%d): %v", i, s.budgeted, err)
		}
	}

	var count int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM budget_entries WHERE category_id = ?`, category).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	var id string
	var budgeted int64
	var deletedAt sql.NullInt64
	if err := conn.QueryRow(`SELECT id, budgeted, deleted_at FROM budget_entries WHERE category_id = ?`, category).
		Scan(&id, &budgeted, &deletedAt); err != nil {
		t.Fatalf("select: %v", err)
	}
	if count != 1 || id != "88888888-8888-8888-8888-888888888888" || budgeted != 50 || deletedAt.Valid {
		t.Fatalf("expected one revived row budgeted 50, got count=%d id=%s budgeted=%d deleted=%v", count, id, budgeted, deletedAt.Valid)
	}
}

func TestDeleteCategory_ReassignRevivesTargetTombstone(t *testing.T) {
	conn := newTestDB(t)
	source, target := budgetCategoriesSetup(t, conn)
	const node = "22222222-2222-2222-2222-222222222222"
	const targetEntry = "88888888-8888-8888-8888-888888888888"
	const sourceEntry = "77777777-7777-7777-7777-777777777777"

	if err := db.SetBudgetedAmount(t.Context(), conn, targetEntry, target, "2026-01", 900, 2000, 0, node); err != nil {
		t.Fatalf("budget target: %v", err)
	}
	if err := db.SetBudgetedAmount(t.Context(), conn, targetEntry, target, "2026-01", 0, 2000, 1, node); err != nil {
		t.Fatalf("zero target: %v", err)
	}
	if err := db.SetBudgetedAmount(t.Context(), conn, sourceEntry, source, "2026-01", 300, 2000, 2, node); err != nil {
		t.Fatalf("budget source: %v", err)
	}

	if _, err := db.DeleteCategory(t.Context(), conn, source, &target, 3000, 0, node); err != nil {
		t.Fatalf("DeleteCategory: %v", err)
	}

	var budgeted int64
	var deletedAt sql.NullInt64
	if err := conn.QueryRow(`SELECT budgeted, deleted_at FROM budget_entries WHERE id = ?`, targetEntry).Scan(&budgeted, &deletedAt); err != nil {
		t.Fatalf("target row: %v", err)
	}
	if budgeted != 300 || deletedAt.Valid {
		t.Fatalf("expected target revived with 300, got budgeted=%d deleted=%v", budgeted, deletedAt.Valid)
	}
	var sourceCategory string
	var sourceDeleted sql.NullInt64
	if err := conn.QueryRow(`SELECT category_id, deleted_at FROM budget_entries WHERE id = ?`, sourceEntry).Scan(&sourceCategory, &sourceDeleted); err != nil {
		t.Fatalf("source row: %v", err)
	}
	if sourceCategory != source || !sourceDeleted.Valid {
		t.Fatalf("expected source row soft-deleted on its own category, got category=%s deleted=%v", sourceCategory, sourceDeleted.Valid)
	}
}

func TestDeleteCategory_ReassignCreatesTargetRowWithUUIDv5(t *testing.T) {
	conn := newTestDB(t)
	source, target := budgetCategoriesSetup(t, conn)
	const node = "22222222-2222-2222-2222-222222222222"

	if err := db.SetBudgetedAmount(t.Context(), conn, "77777777-7777-7777-7777-777777777777", source, "2026-02", 400, 2000, 0, node); err != nil {
		t.Fatalf("budget source: %v", err)
	}
	if _, err := db.DeleteCategory(t.Context(), conn, source, &target, 3000, 0, node); err != nil {
		t.Fatalf("DeleteCategory: %v", err)
	}

	var budgeted int64
	if err := conn.QueryRow(`SELECT budgeted FROM budget_entries WHERE id = ? AND category_id = ? AND deleted_at IS NULL`,
		db.BudgetEntryID(target, "2026-02"), target).Scan(&budgeted); err != nil {
		t.Fatalf("target row: %v", err)
	}
	if budgeted != 400 {
		t.Fatalf("expected 400, got %d", budgeted)
	}
}

func TestBudgetEntryID(t *testing.T) {
	// Same value asserted by web/src/db/budget.test.ts, so both sides mint
	// the same id for a pair.
	if got := db.BudgetEntryID("11111111-1111-1111-1111-111111111111", "2026-03"); got != "ee5574d2-1dda-57f6-a1a4-4b604765be00" {
		t.Fatalf("unexpected UUIDv5: %s", got)
	}
}

func TestBudgetGet_ToBudgetExcludesFutureMonthsBudgeted(t *testing.T) {
	conn := newTestDB(t)
	h := &BudgetHandler{DB: conn}
	sh := &SyncHandler{DB: conn}

	postSync(t, sh, `{
		"since": 0,
		"mutations": [
			{"table": "accounts", "op": "upsert", "group_id": null, "row": {
				"id": "11111111-1111-1111-1111-111111111111",
				"name": "Checking", "type": "checking", "on_budget": true, "closed": false, "currency": "USD",
				"hlc_physical": 1000, "hlc_counter": 0, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
			}},
			{"table": "category_groups", "op": "upsert", "group_id": null, "row": {
				"id": "33333333-3333-3333-3333-333333333333",
				"name": "Bills", "is_income": false, "sort_order": 0,
				"hlc_physical": 1000, "hlc_counter": 1, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
			}}
		]
	}`)

	category, err := db.CreateCategory(t.Context(), conn, db.NewCategory{
		ID:          "44444444-4444-4444-4444-444444444444",
		GroupID:     "33333333-3333-3333-3333-333333333333",
		Name:        "Groceries",
		HLCPhysical: 1000, HLCCounter: 2, HLCNodeID: "22222222-2222-2222-2222-222222222222",
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	if _, err := db.CreateTransaction(t.Context(), conn, db.NewTransaction{
		ID:          "55555555-5555-5555-5555-555555555555",
		AccountID:   "11111111-1111-1111-1111-111111111111",
		Date:        "2026-01-15",
		Amount:      100000,
		HLCPhysical: 1000, HLCCounter: 3, HLCNodeID: "22222222-2222-2222-2222-222222222222",
	}); err != nil {
		t.Fatalf("CreateTransaction: %v", err)
	}

	// Budgeted a month AHEAD of the one being requested — must not count
	// toward "to date" yet.
	if err := db.SetBudgetedAmount(t.Context(), conn,
		"88888888-8888-8888-8888-888888888888", category.ID, "2026-02",
		30000, 1000, 4, "22222222-2222-2222-2222-222222222222"); err != nil {
		t.Fatalf("SetBudgetedAmount: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/budget/2026-01", nil)
	req.SetPathValue("month", "2026-01")
	w := httptest.NewRecorder()
	h.Get(w, req)

	var out monthBudget
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.ToBudget != 100000 {
		t.Fatalf("expected to_budget 100000 (next month's budgeted amount excluded), got %d", out.ToBudget)
	}
}
