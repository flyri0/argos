package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"argos/internal/db"
)

func newTestSyncHandler(t *testing.T) *SyncHandler {
	t.Helper()
	path := filepath.Join(t.TempDir(), "argos.db")
	if err := db.Migrate(path); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	conn, err := db.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return &SyncHandler{DB: conn}
}

func postSync(t *testing.T, h *SyncHandler, body string) syncResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/sync", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.Handle(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp syncResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, w.Body.String())
	}
	return resp
}

// budgetEntrySyncSetup creates the group and category the budget_entries
// identity tests below budget against.
func budgetEntrySyncSetup(t *testing.T, h *SyncHandler) {
	t.Helper()
	postSync(t, h, `{
		"since": 0,
		"mutations": [
			{"table": "category_groups", "op": "upsert", "group_id": null, "row": {
				"id": "33333333-3333-3333-3333-333333333333", "name": "Bills", "is_income": false, "sort_order": 0,
				"hlc_physical": 500, "hlc_counter": 0, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
			}},
			{"table": "categories", "op": "upsert", "group_id": null, "row": {
				"id": "44444444-4444-4444-4444-444444444444", "group_id": "33333333-3333-3333-3333-333333333333",
				"name": "Groceries", "hidden": false, "sort_order": 0,
				"hlc_physical": 500, "hlc_counter": 1, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
			}}
		]
	}`)
}

func budgetEntryUpsertBody(id string, budgeted, hlcPhysical int64) string {
	return fmt.Sprintf(`{"since": 0, "mutations": [
		{"table": "budget_entries", "op": "upsert", "group_id": null, "row": {
			"id": %q, "category_id": "44444444-4444-4444-4444-444444444444", "month": "2026-03", "budgeted": %d,
			"hlc_physical": %d, "hlc_counter": 0, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
		}}
	]}`, id, budgeted, hlcPhysical)
}

type storedBudgetEntry struct {
	ID        string
	Budgeted  int64
	DeletedAt *int64
}

func budgetEntriesForPair(t *testing.T, h *SyncHandler) []storedBudgetEntry {
	t.Helper()
	rows, err := h.DB.Query(`SELECT id, budgeted, deleted_at FROM budget_entries
		WHERE category_id = '44444444-4444-4444-4444-444444444444' AND month = '2026-03'`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	var out []storedBudgetEntry
	for rows.Next() {
		var e storedBudgetEntry
		if err := rows.Scan(&e.ID, &e.Budgeted, &e.DeletedAt); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, e)
	}
	return out
}

func TestSync_BudgetEntryRevivedAfterDelete(t *testing.T) {
	h := newTestSyncHandler(t)
	budgetEntrySyncSetup(t, h)
	const r1 = "55555555-5555-5555-5555-555555555555"

	postSync(t, h, budgetEntryUpsertBody(r1, 100, 1000))
	postSync(t, h, fmt.Sprintf(`{"since": 0, "mutations": [
		{"table": "budget_entries", "op": "delete", "group_id": null, "row": {
			"id": %q, "deleted_at": 1500, "hlc_physical": 1500, "hlc_counter": 0, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
		}}
	]}`, r1))
	resp := postSync(t, h, budgetEntryUpsertBody(r1, 50, 2000))

	if len(resp.Results) != 1 || resp.Results[0].Status != "applied" {
		t.Fatalf("expected applied, got %+v", resp.Results)
	}
	got := budgetEntriesForPair(t, h)
	if len(got) != 1 || got[0].ID != r1 || got[0].Budgeted != 50 || got[0].DeletedAt != nil {
		t.Fatalf("expected one revived row %s budgeted 50, got %+v", r1, got)
	}
}

func TestSync_BudgetEntrySameKeyDifferentID_NewerWins(t *testing.T) {
	h := newTestSyncHandler(t)
	budgetEntrySyncSetup(t, h)
	const r1 = "55555555-5555-5555-5555-555555555555"
	const r2 = "66666666-6666-6666-6666-666666666666"

	postSync(t, h, budgetEntryUpsertBody(r1, 100, 1000))
	resp := postSync(t, h, budgetEntryUpsertBody(r2, 70, 2000))

	if len(resp.Results) != 1 || resp.Results[0].Status != "applied" {
		t.Fatalf("expected applied, got %+v", resp.Results)
	}
	got := budgetEntriesForPair(t, h)
	if len(got) != 1 || got[0].ID != r1 || got[0].Budgeted != 70 {
		t.Fatalf("expected the newer write applied onto %s with no %s row, got %+v", r1, r2, got)
	}
}

func TestSync_BudgetEntrySameKeyDifferentID_OlderIsStale(t *testing.T) {
	h := newTestSyncHandler(t)
	budgetEntrySyncSetup(t, h)
	const r1 = "55555555-5555-5555-5555-555555555555"
	const r2 = "66666666-6666-6666-6666-666666666666"

	postSync(t, h, budgetEntryUpsertBody(r1, 100, 2000))
	resp := postSync(t, h, budgetEntryUpsertBody(r2, 70, 1000))

	if len(resp.Results) != 1 || resp.Results[0].Status != "rejected_stale" {
		t.Fatalf("expected rejected_stale, got %+v", resp.Results)
	}
	got := budgetEntriesForPair(t, h)
	if len(got) != 1 || got[0].ID != r1 || got[0].Budgeted != 100 {
		t.Fatalf("expected %s untouched, got %+v", r1, got)
	}
}

func TestSync_UpsertAccountThenPullsItBack(t *testing.T) {
	h := newTestSyncHandler(t)

	resp := postSync(t, h, `{
		"since": 0,
		"mutations": [
			{"table": "accounts", "op": "upsert", "group_id": null, "row": {
				"id": "11111111-1111-1111-1111-111111111111",
				"name": "Checking", "type": "checking", "on_budget": true, "closed": false, "currency": "USD",
				"hlc_physical": 1000, "hlc_counter": 0, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
			}}
		]
	}`)

	if len(resp.Results) != 1 || resp.Results[0].Status != "applied" {
		t.Fatalf("expected one applied result, got %+v", resp.Results)
	}
	if resp.ServerVersion != 1 {
		t.Fatalf("expected server_version 1, got %d", resp.ServerVersion)
	}
	if len(resp.Changes) != 1 {
		t.Fatalf("expected 1 change, got %d: %+v", len(resp.Changes), resp.Changes)
	}

	// A second sync from since=0 should still see the same row.
	resp2 := postSync(t, h, `{"since": 0, "mutations": []}`)
	if len(resp2.Changes) != 1 {
		t.Fatalf("expected 1 change on re-pull, got %d", len(resp2.Changes))
	}

	// Advancing the cursor to the latest version should see nothing new.
	resp3 := postSync(t, h, `{"since": 1, "mutations": []}`)
	if len(resp3.Changes) != 0 {
		t.Fatalf("expected 0 changes after cursor, got %d", len(resp3.Changes))
	}
}

func TestSync_InvalidReferenceRejectedIndependently(t *testing.T) {
	h := newTestSyncHandler(t)

	resp := postSync(t, h, `{
		"since": 0,
		"mutations": [
			{"table": "payees", "op": "upsert", "group_id": null, "row": {
				"id": "33333333-3333-3333-3333-333333333333", "name": "Landlord",
				"hlc_physical": 1000, "hlc_counter": 0, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
			}},
			{"table": "transactions", "op": "upsert", "group_id": null, "row": {
				"id": "44444444-4444-4444-4444-444444444444",
				"account_id": "99999999-9999-9999-9999-999999999999",
				"date": "2026-01-01", "amount": -500, "cleared": false, "notes": "",
				"hlc_physical": 1000, "hlc_counter": 0, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
			}}
		]
	}`)

	if len(resp.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(resp.Results))
	}
	if resp.Results[0].Status != "applied" {
		t.Fatalf("expected payee upsert to apply, got %+v", resp.Results[0])
	}
	if resp.Results[1].Status != "rejected_invalid" || resp.Results[1].Error == nil || resp.Results[1].Error.Code != "SYNC_MUTATION_INVALID" {
		t.Fatalf("expected transaction upsert rejected_invalid, got %+v", resp.Results[1])
	}
}

func TestSync_LogsPushSummaryAndRejectedMutations(t *testing.T) {
	conn := newTestDB(t)
	var out bytes.Buffer
	h := &SyncHandler{DB: conn, Logger: testLoggerTo(&out)}

	postSync(t, h, `{
		"since": 0,
		"mutations": [
			{"table": "payees", "op": "upsert", "group_id": null, "row": {
				"id": "33333333-3333-3333-3333-333333333333", "name": "Landlord",
				"hlc_physical": 1000, "hlc_counter": 0, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
			}},
			{"table": "transactions", "op": "upsert", "group_id": null, "row": {
				"id": "44444444-4444-4444-4444-444444444444",
				"account_id": "99999999-9999-9999-9999-999999999999",
				"date": "2026-01-01", "amount": -500, "cleared": false, "notes": "",
				"hlc_physical": 1000, "hlc_counter": 0, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
			}}
		]
	}`)

	log := out.String()
	for _, want := range []string{
		`msg="sync push"`, "mutations=2", "applied=1", "rejected_invalid=1",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("expected sync push summary to contain %q, got %q", want, log)
		}
	}
	if !strings.Contains(log, `msg="sync mutation rejected"`) || !strings.Contains(log, "error_code=SYNC_MUTATION_INVALID") {
		t.Fatalf("expected the rejected mutation to be logged with its error code, got %q", log)
	}
}

func TestSync_GroupIsAtomic(t *testing.T) {
	h := newTestSyncHandler(t)

	groupID := "group-1"
	resp := postSync(t, h, `{
		"since": 0,
		"mutations": [
			{"table": "accounts", "op": "upsert", "group_id": "`+groupID+`", "row": {
				"id": "11111111-1111-1111-1111-111111111111",
				"name": "Checking", "type": "checking", "on_budget": true, "closed": false, "currency": "USD",
				"hlc_physical": 1000, "hlc_counter": 0, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
			}},
			{"table": "transactions", "op": "upsert", "group_id": "`+groupID+`", "row": {
				"id": "44444444-4444-4444-4444-444444444444",
				"account_id": "99999999-9999-9999-9999-999999999999",
				"date": "2026-01-01", "amount": -500, "cleared": false, "notes": "",
				"hlc_physical": 1000, "hlc_counter": 0, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
			}}
		]
	}`)

	if len(resp.Results) != 1 {
		t.Fatalf("expected 1 grouped result, got %d: %+v", len(resp.Results), resp.Results)
	}
	if resp.Results[0].Status != "rejected_invalid" {
		t.Fatalf("expected group rejected_invalid, got %+v", resp.Results[0])
	}
	if len(resp.Changes) != 0 {
		t.Fatalf("expected no changes committed from a failed group, got %d", len(resp.Changes))
	}
}

func TestSync_DeleteMutation(t *testing.T) {
	h := newTestSyncHandler(t)

	postSync(t, h, `{
		"since": 0,
		"mutations": [
			{"table": "payees", "op": "upsert", "group_id": null, "row": {
				"id": "33333333-3333-3333-3333-333333333333", "name": "Landlord",
				"hlc_physical": 1000, "hlc_counter": 0, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
			}}
		]
	}`)

	resp := postSync(t, h, `{
		"since": 0,
		"mutations": [
			{"table": "payees", "op": "delete", "group_id": null, "row": {
				"id": "33333333-3333-3333-3333-333333333333", "deleted_at": 2000,
				"hlc_physical": 2000, "hlc_counter": 0, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
			}}
		]
	}`)

	if resp.Results[0].Status != "applied" {
		t.Fatalf("expected delete applied, got %+v", resp.Results[0])
	}

	var found *syncRowPayee
	for _, c := range resp.Changes {
		if c.Table == "payees" {
			b, _ := json.Marshal(c.Row)
			var p syncRowPayee
			json.Unmarshal(b, &p)
			found = &p
		}
	}
	if found == nil || found.DeletedAt == nil {
		t.Fatalf("expected payee change with deleted_at set, got %+v", found)
	}
}

func TestSync_StaleMutationRejected(t *testing.T) {
	h := newTestSyncHandler(t)

	postSync(t, h, `{
		"since": 0,
		"mutations": [
			{"table": "payees", "op": "upsert", "group_id": null, "row": {
				"id": "33333333-3333-3333-3333-333333333333", "name": "Landlord",
				"hlc_physical": 2000, "hlc_counter": 5, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
			}}
		]
	}`)

	// A lone write with an HLC equal to the existing row's is skipped as
	// already applied, and a unit whose every member was skipped is still
	// reported rejected_stale (§2.4).
	resp := postSync(t, h, `{
		"since": 0,
		"mutations": [
			{"table": "payees", "op": "upsert", "group_id": null, "row": {
				"id": "33333333-3333-3333-3333-333333333333", "name": "Landlord (stale rename)",
				"hlc_physical": 2000, "hlc_counter": 5, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
			}}
		]
	}`)

	if resp.Results[0].Status != "rejected_stale" {
		t.Fatalf("expected rejected_stale, got %+v", resp.Results[0])
	}
	if resp.Results[0].Error != nil {
		t.Fatalf("rejected_stale must not carry an error object (§2.4), got %+v", resp.Results[0].Error)
	}

	// The stale mutation must not have overwritten the row.
	resp2 := postSync(t, h, `{"since": 0, "mutations": []}`)
	var found *syncRowPayee
	for _, c := range resp2.Changes {
		if c.Table == "payees" {
			b, _ := json.Marshal(c.Row)
			var p syncRowPayee
			json.Unmarshal(b, &p)
			found = &p
		}
	}
	if found == nil || found.Name != "Landlord" {
		t.Fatalf("expected stale mutation to leave row unchanged, got %+v", found)
	}
}

func TestSync_StaleResponseIncludesWinningRowBelowCursor(t *testing.T) {
	h := newTestSyncHandler(t)
	const x = "33333333-3333-3333-3333-333333333333"

	// Device B writes X; a pull then advances this device's cursor past it.
	postSync(t, h, `{"since": 0, "mutations": [
		{"table": "payees", "op": "upsert", "group_id": null, "row": {
			"id": "`+x+`", "name": "From device B",
			"hlc_physical": 2000, "hlc_counter": 0, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
		}}
	]}`)
	cursor := postSync(t, h, `{"since": 0, "mutations": []}`).ServerVersion

	resp := postSync(t, h, fmt.Sprintf(`{"since": %d, "mutations": [
		{"table": "payees", "op": "upsert", "group_id": null, "row": {
			"id": %q, "name": "Older local rename",
			"hlc_physical": 1000, "hlc_counter": 0, "hlc_node_id": "44444444-4444-4444-4444-444444444444"
		}}
	]}`, cursor, x))

	if len(resp.Results) != 1 || resp.Results[0].Status != "rejected_stale" {
		t.Fatalf("expected rejected_stale, got %+v", resp.Results)
	}
	var matches []syncRowPayee
	for _, c := range resp.Changes {
		if c.Table != "payees" {
			continue
		}
		b, _ := json.Marshal(c.Row)
		var p syncRowPayee
		json.Unmarshal(b, &p)
		if p.ID == x {
			matches = append(matches, p)
		}
	}
	if len(matches) != 1 || matches[0].HLCPhysical != 2000 || matches[0].Name != "From device B" {
		t.Fatalf("expected X once with the winning HLC 2000, got %+v", matches)
	}
	if matches[0].ServerVersion > cursor {
		t.Fatalf("test setup: expected X at or below the cursor %d, got server_version %d", cursor, matches[0].ServerVersion)
	}
}

func TestSync_StaleGroupReturnsAllMemberRows(t *testing.T) {
	h := newTestSyncHandler(t)
	const (
		node     = "22222222-2222-2222-2222-222222222222"
		account  = "11111111-1111-1111-1111-111111111111"
		group    = "33333333-3333-3333-3333-333333333333"
		catC     = "cccccccc-cccc-cccc-cccc-cccccccccccc"
		catD     = "dddddddd-dddd-dddd-dddd-dddddddddddd"
		txnT1    = "a1a1a1a1-a1a1-a1a1-a1a1-a1a1a1a1a1a1"
		txnT2    = "b2b2b2b2-b2b2-b2b2-b2b2-b2b2b2b2b2b2"
		reassign = "e3e3e3e3-e3e3-e3e3-e3e3-e3e3e3e3e3e3"
	)
	txn := func(groupID, id, category string, amount, hlc int64) string {
		return fmt.Sprintf(`{"table": "transactions", "op": "upsert", "group_id": %s, "row": {
			"id": %q, "account_id": %q, "category_id": %q, "date": "2026-01-01", "amount": %d, "cleared": false, "notes": "",
			"hlc_physical": %d, "hlc_counter": 0, "hlc_node_id": %q
		}}`, jsonGroupID(groupID), id, account, category, amount, hlc, node)
	}
	category := func(id, name string, counter int) string {
		return fmt.Sprintf(`{"table": "categories", "op": "upsert", "group_id": null, "row": {
			"id": %q, "group_id": %q, "name": %q, "hidden": false, "sort_order": 0,
			"hlc_physical": 1000, "hlc_counter": %d, "hlc_node_id": %q
		}}`, id, group, name, counter, node)
	}

	postSync(t, h, syncBody(
		fmt.Sprintf(`{"table": "accounts", "op": "upsert", "group_id": null, "row": {
			"id": %q, "name": "Checking", "type": "checking", "on_budget": true, "closed": false, "currency": "USD",
			"hlc_physical": 1000, "hlc_counter": 0, "hlc_node_id": %q
		}}`, account, node),
		fmt.Sprintf(`{"table": "category_groups", "op": "upsert", "group_id": null, "row": {
			"id": %q, "name": "Bills", "is_income": false, "sort_order": 0,
			"hlc_physical": 1000, "hlc_counter": 0, "hlc_node_id": %q
		}}`, group, node),
		category(catC, "C", 1),
		category(catD, "D", 2),
		txn("", txnT1, catC, -100, 1000),
		txn("", txnT2, catC, -200, 1000),
	))
	// T1 is edited elsewhere with a newer HLC than the reassignment below.
	cursor := postSync(t, h, syncBody(txn("", txnT1, catC, -700, 3000))).ServerVersion

	resp := postSync(t, h, fmt.Sprintf(`{"since": %d, "mutations": [%s, %s, %s]}`, cursor,
		txn(reassign, txnT1, catD, -100, 2000),
		txn(reassign, txnT2, catD, -200, 2001),
		fmt.Sprintf(`{"table": "categories", "op": "delete", "group_id": %q, "row": {
			"id": %q, "deleted_at": 2002, "hlc_physical": 2002, "hlc_counter": 0, "hlc_node_id": %q
		}}`, reassign, catC, node),
	))

	if len(resp.Results) != 1 || resp.Results[0].Status != "rejected_stale" {
		t.Fatalf("expected rejected_stale, got %+v", resp.Results)
	}

	counts := map[string]int{}
	var t1, t2 syncRowTransaction
	var c syncRowCategory
	for _, ch := range resp.Changes {
		b, _ := json.Marshal(ch.Row)
		switch ch.Table {
		case "transactions":
			var row syncRowTransaction
			json.Unmarshal(b, &row)
			counts[row.ID]++
			if row.ID == txnT1 {
				t1 = row
			} else if row.ID == txnT2 {
				t2 = row
			}
		case "categories":
			var row syncRowCategory
			json.Unmarshal(b, &row)
			counts[row.ID]++
			if row.ID == catC {
				c = row
			}
		default:
			t.Fatalf("unexpected change for table %s", ch.Table)
		}
	}

	if len(resp.Changes) != 3 || counts[txnT1] != 1 || counts[txnT2] != 1 || counts[catC] != 1 {
		t.Fatalf("expected T1, T2, and C exactly once each, got counts %v in %d changes", counts, len(resp.Changes))
	}
	if t1.CategoryID == nil || *t1.CategoryID != catC || t1.Amount != -700 || t1.HLCPhysical != 3000 {
		t.Fatalf("expected T1 in its unchanged server state, got %+v", t1)
	}
	if t2.CategoryID == nil || *t2.CategoryID != catC || t2.HLCPhysical != 1000 {
		t.Fatalf("expected T2 in its unchanged server state, got %+v", t2)
	}
	if c.DeletedAt != nil {
		t.Fatalf("expected category C still live, got %+v", c)
	}
}

const (
	transferLeg1 = "55555555-5555-5555-5555-555555555555"
	transferLeg2 = "66666666-6666-6666-6666-666666666666"
)

// transferSyncSetup creates the two on-budget accounts the transfer group
// tests below move money between.
func transferSyncSetup(t *testing.T, h *SyncHandler) {
	t.Helper()
	postSync(t, h, `{
		"since": 0,
		"mutations": [
			{"table": "accounts", "op": "upsert", "group_id": null, "row": {
				"id": "11111111-1111-1111-1111-111111111111",
				"name": "Checking", "type": "checking", "on_budget": true, "closed": false, "currency": "USD",
				"hlc_physical": 500, "hlc_counter": 0, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
			}},
			{"table": "accounts", "op": "upsert", "group_id": null, "row": {
				"id": "77777777-7777-7777-7777-777777777777",
				"name": "Savings", "type": "savings", "on_budget": true, "closed": false, "currency": "USD",
				"hlc_physical": 500, "hlc_counter": 1, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
			}}
		]
	}`)
}

// transferLegMutation is one leg of a transfer as a /sync mutation; leg 1
// lives in Checking, leg 2 in Savings.
func transferLegMutation(groupID, legID string, amount, hlcPhysical int64) string {
	account := "11111111-1111-1111-1111-111111111111"
	if legID == transferLeg2 {
		account = "77777777-7777-7777-7777-777777777777"
	}
	return fmt.Sprintf(`{"table": "transactions", "op": "upsert", "group_id": %q, "row": {
		"id": %q, "account_id": %q, "transfer_id": "abababab-abab-abab-abab-abababababab",
		"date": "2026-01-01", "amount": %d, "cleared": false, "notes": "",
		"hlc_physical": %d, "hlc_counter": 0, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
	}}`, groupID, legID, account, amount, hlcPhysical)
}

// transferLegUpsert is a transfer leg upsert with a chosen cleared flag; an
// empty groupID sends it as a lone (null group) mutation.
func transferLegUpsert(groupID, legID string, amount, hlcPhysical int64, cleared bool) string {
	account := "11111111-1111-1111-1111-111111111111"
	if legID == transferLeg2 {
		account = "77777777-7777-7777-7777-777777777777"
	}
	return fmt.Sprintf(`{"table": "transactions", "op": "upsert", "group_id": %s, "row": {
		"id": %q, "account_id": %q, "transfer_id": "abababab-abab-abab-abab-abababababab",
		"date": "2026-01-01", "amount": %d, "cleared": %t, "notes": "",
		"hlc_physical": %d, "hlc_counter": 0, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
	}}`, jsonGroupID(groupID), legID, account, amount, cleared, hlcPhysical)
}

func transferLegDelete(groupID, legID string, hlcPhysical int64) string {
	return fmt.Sprintf(`{"table": "transactions", "op": "delete", "group_id": %s, "row": {
		"id": %q, "deleted_at": %d,
		"hlc_physical": %d, "hlc_counter": 0, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
	}}`, jsonGroupID(groupID), legID, hlcPhysical, hlcPhysical)
}

func jsonGroupID(groupID string) string {
	if groupID == "" {
		return "null"
	}
	return fmt.Sprintf("%q", groupID)
}

type transferLegRow struct {
	Amount    int64
	Cleared   bool
	Deleted   bool
	HLCPhysic int64
}

func transferLegRowState(t *testing.T, h *SyncHandler, legID string) transferLegRow {
	t.Helper()
	var r transferLegRow
	var deletedAt sql.NullInt64
	if err := h.DB.QueryRow(`SELECT amount, cleared, deleted_at, hlc_physical FROM transactions WHERE id = ?`, legID).
		Scan(&r.Amount, &r.Cleared, &deletedAt, &r.HLCPhysic); err != nil {
		t.Fatalf("query leg %s: %v", legID, err)
	}
	r.Deleted = deletedAt.Valid
	return r
}

func expectTransferPairInvalid(t *testing.T, resp syncResponse) {
	t.Helper()
	if len(resp.Results) != 1 || resp.Results[0].Status != "rejected_invalid" ||
		resp.Results[0].Error == nil || resp.Results[0].Error.Code != "TRANSFER_PAIR_INVALID" {
		t.Fatalf("expected rejected_invalid TRANSFER_PAIR_INVALID, got %+v", resp.Results)
	}
}

func expectApplied(t *testing.T, resp syncResponse) {
	t.Helper()
	if len(resp.Results) != 1 || resp.Results[0].Status != "applied" {
		t.Fatalf("expected applied, got %+v", resp.Results)
	}
}

// createTransferViaSync creates a balanced transfer: leg 1 -1000 in
// Checking, leg 2 +1000 in Savings, both at hlc 1000.
func createTransferViaSync(t *testing.T, h *SyncHandler) {
	t.Helper()
	transferSyncSetup(t, h)
	const group = "c1c1c1c1-c1c1-c1c1-c1c1-c1c1c1c1c1c1"
	expectApplied(t, postSync(t, h, syncBody(
		transferLegUpsert(group, transferLeg1, -1000, 1000, false),
		transferLegUpsert(group, transferLeg2, 1000, 1000, false),
	)))
}

func TestSync_TransferGroupCreatingBothLegsApplies(t *testing.T) {
	h := newTestSyncHandler(t)
	createTransferViaSync(t, h)

	if l1, l2 := transferLegRowState(t, h, transferLeg1), transferLegRowState(t, h, transferLeg2); l1.Amount != -1000 || l2.Amount != 1000 {
		t.Fatalf("expected a balanced pair, got %+v / %+v", l1, l2)
	}
}

func TestSync_TransferLoneLegUpsertUnbalancingPairRejected(t *testing.T) {
	h := newTestSyncHandler(t)
	createTransferViaSync(t, h)

	resp := postSync(t, h, syncBody(transferLegUpsert("", transferLeg1, -1500, 2000, false)))

	expectTransferPairInvalid(t, resp)
	if l1 := transferLegRowState(t, h, transferLeg1); l1.Amount != -1000 || l1.HLCPhysic != 1000 {
		t.Fatalf("expected leg 1 unchanged, got %+v", l1)
	}
	if l2 := transferLegRowState(t, h, transferLeg2); l2.Amount != 1000 || l2.HLCPhysic != 1000 {
		t.Fatalf("expected leg 2 unchanged, got %+v", l2)
	}
}

func TestSync_TransferGroupWithUnbalancedAmountsRejected(t *testing.T) {
	h := newTestSyncHandler(t)
	transferSyncSetup(t, h)
	const group = "c1c1c1c1-c1c1-c1c1-c1c1-c1c1c1c1c1c1"

	resp := postSync(t, h, syncBody(
		transferLegUpsert(group, transferLeg1, -1000, 1000, false),
		transferLegUpsert(group, transferLeg2, 900, 1000, false),
	))

	expectTransferPairInvalid(t, resp)
	var count int
	if err := h.DB.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no legs committed, got %d", count)
	}
}

func TestSync_TransferLoneLegDeleteRejected(t *testing.T) {
	h := newTestSyncHandler(t)
	createTransferViaSync(t, h)

	resp := postSync(t, h, syncBody(transferLegDelete("", transferLeg1, 2000)))

	expectTransferPairInvalid(t, resp)
	if l1 := transferLegRowState(t, h, transferLeg1); l1.Deleted {
		t.Fatalf("expected leg 1 still live, got %+v", l1)
	}
}

func TestSync_TransferGroupDeletingBothLegsApplies(t *testing.T) {
	h := newTestSyncHandler(t)
	createTransferViaSync(t, h)
	const group = "d2d2d2d2-d2d2-d2d2-d2d2-d2d2d2d2d2d2"

	expectApplied(t, postSync(t, h, syncBody(
		transferLegDelete(group, transferLeg1, 2000),
		transferLegDelete(group, transferLeg2, 2000),
	)))
	if l1, l2 := transferLegRowState(t, h, transferLeg1), transferLegRowState(t, h, transferLeg2); !l1.Deleted || !l2.Deleted {
		t.Fatalf("expected both legs deleted, got %+v / %+v", l1, l2)
	}
}

// Scenario 1: device A edits the transfer 1000→1500 as a group; device B,
// which never saw the edit, toggles cleared on leg 1 alone with a newer HLC
// and the old amount. Applying it would leave -1000 / +1500.
func TestSync_TransferEditThenLoneNewerClearedUpsertRejected(t *testing.T) {
	h := newTestSyncHandler(t)
	createTransferViaSync(t, h)
	const editGroup = "d2d2d2d2-d2d2-d2d2-d2d2-d2d2d2d2d2d2"

	expectApplied(t, postSync(t, h, syncBody(
		transferLegUpsert(editGroup, transferLeg1, -1500, 2000, false),
		transferLegUpsert(editGroup, transferLeg2, 1500, 2000, false),
	)))

	resp := postSync(t, h, syncBody(transferLegUpsert("", transferLeg1, -1000, 3000, true)))

	expectTransferPairInvalid(t, resp)
	if l1 := transferLegRowState(t, h, transferLeg1); l1.Amount != -1500 || l1.Cleared {
		t.Fatalf("expected leg 1 to keep the edit, got %+v", l1)
	}
}

// Scenario 2: device A deletes the transfer as a group; device B, which
// never saw the delete, toggles cleared on leg 1 — now sent as a newer group
// re-stamping both legs. The pair is revived together and stays balanced.
func TestSync_TransferDeleteThenNewerClearedPairGroupRevivesBoth(t *testing.T) {
	h := newTestSyncHandler(t)
	createTransferViaSync(t, h)
	const deleteGroup = "d2d2d2d2-d2d2-d2d2-d2d2-d2d2d2d2d2d2"
	const toggleGroup = "e3e3e3e3-e3e3-e3e3-e3e3-e3e3e3e3e3e3"

	expectApplied(t, postSync(t, h, syncBody(
		transferLegDelete(deleteGroup, transferLeg1, 2000),
		transferLegDelete(deleteGroup, transferLeg2, 2000),
	)))

	expectApplied(t, postSync(t, h, syncBody(
		transferLegUpsert(toggleGroup, transferLeg1, -1000, 3000, true),
		transferLegUpsert(toggleGroup, transferLeg2, 1000, 3000, false),
	)))

	l1, l2 := transferLegRowState(t, h, transferLeg1), transferLegRowState(t, h, transferLeg2)
	if l1.Deleted || l2.Deleted || l1.Amount+l2.Amount != 0 || !l1.Cleared || l2.Cleared {
		t.Fatalf("expected both legs live, balanced, only leg 1 cleared; got %+v / %+v", l1, l2)
	}
}

func syncBody(mutations ...string) string {
	return `{"since": 0, "mutations": [` + strings.Join(mutations, ",") + `]}`
}

func transferLegState(t *testing.T, h *SyncHandler, legID string) (amount, hlcPhysical int64) {
	t.Helper()
	if err := h.DB.QueryRow(`SELECT amount, hlc_physical FROM transactions WHERE id = ?`, legID).Scan(&amount, &hlcPhysical); err != nil {
		t.Fatalf("query leg %s: %v", legID, err)
	}
	return amount, hlcPhysical
}

func TestSync_GroupRetryWithIdenticalHLCIsStaleAndHarmless(t *testing.T) {
	h := newTestSyncHandler(t)
	transferSyncSetup(t, h)
	const group = "c1c1c1c1-c1c1-c1c1-c1c1-c1c1c1c1c1c1"
	body := syncBody(
		transferLegMutation(group, transferLeg1, -1000, 1000),
		transferLegMutation(group, transferLeg2, 1000, 1001),
	)

	if resp := postSync(t, h, body); len(resp.Results) != 1 || resp.Results[0].Status != "applied" {
		t.Fatalf("expected first push applied, got %+v", resp.Results)
	}
	resp := postSync(t, h, body)

	if len(resp.Results) != 1 || resp.Results[0].Status != "rejected_stale" {
		t.Fatalf("expected identical retry rejected_stale, got %+v", resp.Results)
	}
	if amount, hlc := transferLegState(t, h, transferLeg1); amount != -1000 || hlc != 1000 {
		t.Fatalf("leg 1 changed: amount=%d hlc=%d", amount, hlc)
	}
	if amount, hlc := transferLegState(t, h, transferLeg2); amount != 1000 || hlc != 1001 {
		t.Fatalf("leg 2 changed: amount=%d hlc=%d", amount, hlc)
	}
}

func TestSync_GroupWithAlreadyAppliedAndNewerMembersApplies(t *testing.T) {
	h := newTestSyncHandler(t)
	transferSyncSetup(t, h)
	const createGroup = "c1c1c1c1-c1c1-c1c1-c1c1-c1c1c1c1c1c1"
	const retryGroup = "d2d2d2d2-d2d2-d2d2-d2d2-d2d2d2d2d2d2"

	postSync(t, h, syncBody(
		transferLegMutation(createGroup, transferLeg1, -1000, 1000),
		transferLegMutation(createGroup, transferLeg2, 1000, 2000),
	))

	// A lost response left the create unsynced, and a later edit merged
	// into the same unit: the create members are already applied and must
	// not discard the edit.
	resp := postSync(t, h, syncBody(
		transferLegMutation(retryGroup, transferLeg1, -1000, 1000),
		transferLegMutation(retryGroup, transferLeg2, 1000, 2000),
		transferLegMutation(retryGroup, transferLeg1, -2500, 5000),
		transferLegMutation(retryGroup, transferLeg2, 2500, 6000),
	))

	if len(resp.Results) != 1 || resp.Results[0].Status != "applied" {
		t.Fatalf("expected applied, got %+v", resp.Results)
	}
	if amount, hlc := transferLegState(t, h, transferLeg1); amount != -2500 || hlc != 5000 {
		t.Fatalf("expected leg 1 at the h5 edit, got amount=%d hlc=%d", amount, hlc)
	}
	if amount, hlc := transferLegState(t, h, transferLeg2); amount != 2500 || hlc != 6000 {
		t.Fatalf("expected leg 2 at the h6 edit, got amount=%d hlc=%d", amount, hlc)
	}
}

func TestSync_GroupWithStrictlyOlderMemberIsStale(t *testing.T) {
	h := newTestSyncHandler(t)
	transferSyncSetup(t, h)
	const createGroup = "c1c1c1c1-c1c1-c1c1-c1c1-c1c1c1c1c1c1"
	const editGroup = "d2d2d2d2-d2d2-d2d2-d2d2-d2d2d2d2d2d2"

	postSync(t, h, syncBody(
		transferLegMutation(createGroup, transferLeg1, -1000, 2000),
		transferLegMutation(createGroup, transferLeg2, 1000, 2000),
	))

	resp := postSync(t, h, syncBody(
		transferLegMutation(editGroup, transferLeg1, -3000, 1000), // strictly older
		transferLegMutation(editGroup, transferLeg2, 3000, 3000),  // newer
	))

	if len(resp.Results) != 1 || resp.Results[0].Status != "rejected_stale" {
		t.Fatalf("expected rejected_stale, got %+v", resp.Results)
	}
	if resp.Results[0].Error != nil {
		t.Fatalf("rejected_stale must not carry an error object (§2.4), got %+v", resp.Results[0].Error)
	}
	if amount, hlc := transferLegState(t, h, transferLeg2); amount != 1000 || hlc != 2000 {
		t.Fatalf("expected leg 2 untouched by the rejected unit, got amount=%d hlc=%d", amount, hlc)
	}
}

func TestSync_ClockSkewTooLargeRejected(t *testing.T) {
	h := newTestSyncHandler(t)

	farFuture := time.Now().Add(10 * time.Minute).UnixMilli()
	resp := postSync(t, h, fmt.Sprintf(`{
		"since": 0,
		"mutations": [
			{"table": "payees", "op": "upsert", "group_id": null, "row": {
				"id": "33333333-3333-3333-3333-333333333333", "name": "Landlord",
				"hlc_physical": %d, "hlc_counter": 0, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
			}}
		]
	}`, farFuture))

	if resp.Results[0].Status != "rejected_invalid" || resp.Results[0].Error == nil || resp.Results[0].Error.Code != "CLOCK_SKEW_TOO_LARGE" {
		t.Fatalf("expected rejected_invalid/CLOCK_SKEW_TOO_LARGE, got %+v", resp.Results[0])
	}
	if len(resp.Changes) != 0 {
		t.Fatalf("expected no changes committed from a clock-skewed mutation, got %d", len(resp.Changes))
	}
}

func TestSync_SecondIncomeGroupRejected(t *testing.T) {
	h := newTestSyncHandler(t)

	postSync(t, h, `{
		"since": 0,
		"mutations": [
			{"table": "category_groups", "op": "upsert", "group_id": null, "row": {
				"id": "55555555-5555-5555-5555-555555555555", "name": "Income", "is_income": true, "sort_order": 0,
				"hlc_physical": 1000, "hlc_counter": 0, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
			}}
		]
	}`)

	resp := postSync(t, h, `{
		"since": 0,
		"mutations": [
			{"table": "category_groups", "op": "upsert", "group_id": null, "row": {
				"id": "66666666-6666-6666-6666-666666666666", "name": "Other income", "is_income": true, "sort_order": 1,
				"hlc_physical": 2000, "hlc_counter": 0, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
			}}
		]
	}`)

	if resp.Results[0].Status != "rejected_invalid" || resp.Results[0].Error == nil || resp.Results[0].Error.Code != "SYNC_MUTATION_INVALID" {
		t.Fatalf("expected second income group rejected_invalid/SYNC_MUTATION_INVALID, got %+v", resp.Results[0])
	}
}

func TestSync_IncomeGroupCannotBeUnmarkedOrDeleted(t *testing.T) {
	h := newTestSyncHandler(t)

	postSync(t, h, `{
		"since": 0,
		"mutations": [
			{"table": "category_groups", "op": "upsert", "group_id": null, "row": {
				"id": "55555555-5555-5555-5555-555555555555", "name": "Income", "is_income": true, "sort_order": 0,
				"hlc_physical": 1000, "hlc_counter": 0, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
			}}
		]
	}`)

	unmark := postSync(t, h, `{
		"since": 0,
		"mutations": [
			{"table": "category_groups", "op": "upsert", "group_id": null, "row": {
				"id": "55555555-5555-5555-5555-555555555555", "name": "Income", "is_income": false, "sort_order": 0,
				"hlc_physical": 2000, "hlc_counter": 0, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
			}}
		]
	}`)
	if unmark.Results[0].Status != "rejected_invalid" || unmark.Results[0].Error == nil || unmark.Results[0].Error.Code != "SYNC_MUTATION_INVALID" {
		t.Fatalf("expected un-marking the income group rejected_invalid/SYNC_MUTATION_INVALID, got %+v", unmark.Results[0])
	}

	del := postSync(t, h, `{
		"since": 0,
		"mutations": [
			{"table": "category_groups", "op": "delete", "group_id": null, "row": {
				"id": "55555555-5555-5555-5555-555555555555", "deleted_at": 3000,
				"hlc_physical": 3000, "hlc_counter": 0, "hlc_node_id": "22222222-2222-2222-2222-222222222222"
			}}
		]
	}`)
	if del.Results[0].Status != "rejected_invalid" || del.Results[0].Error == nil || del.Results[0].Error.Code != "SYNC_MUTATION_INVALID" {
		t.Fatalf("expected deleting the income group rejected_invalid/SYNC_MUTATION_INVALID, got %+v", del.Results[0])
	}
}

func TestSync_ServerMetaGeneratedOnceAndStable(t *testing.T) {
	h := newTestSyncHandler(t)

	resp1 := postSync(t, h, `{"since": 0, "mutations": []}`)
	if resp1.SyncID == "" {
		t.Fatalf("expected a non-empty sync_id on first sync, got %+v", resp1)
	}
	if _, err := uuid.Parse(resp1.SyncID); err != nil {
		t.Fatalf("expected sync_id to be a uuid, got %q: %v", resp1.SyncID, err)
	}
	if resp1.SchemaVersion != 1 {
		t.Fatalf("expected schema_version 1 on first sync, got %d", resp1.SchemaVersion)
	}

	resp2 := postSync(t, h, `{"since": 0, "mutations": []}`)
	if resp2.SyncID != resp1.SyncID {
		t.Fatalf("expected sync_id to stay stable across syncs, got %q then %q", resp1.SyncID, resp2.SyncID)
	}
	if resp2.SchemaVersion != resp1.SchemaVersion {
		t.Fatalf("expected schema_version to stay stable across syncs, got %d then %d", resp1.SchemaVersion, resp2.SchemaVersion)
	}
}
