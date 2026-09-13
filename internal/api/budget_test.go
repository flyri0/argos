package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

	// to_budget = on-budget balance (100000) - budgeted to date (20000).
	// The off-budget account's 500000 balance must not appear in this figure.
	if out.ToBudget != 80000 {
		t.Fatalf("expected to_budget 80000, got %d", out.ToBudget)
	}
	// The category's own available (budgeted + activity, §5.3) is a
	// different figure from to_budget — 20000 budgeted + 100000 activity.
	if len(out.Categories) != 1 || out.Categories[0].Available != 120000 {
		t.Fatalf("expected the one category's available to be 120000, got %+v", out.Categories)
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
