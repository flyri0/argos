package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"argos/internal/db"
)

// These tests reuse the seed helpers and ids from sync_test.go
// (referenceSyncSetup, refTxnUpsert, transferLeg1/2, ...): seeding through
// /sync gives every row a known HLC to write against.

const restSecondAccount = "1b1b1b1b-1b1b-1b1b-1b1b-1b1b1b1b1b1b"

func restRequest(method, target, body string, pathValues ...string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	for i := 0; i+1 < len(pathValues); i += 2 {
		req.SetPathValue(pathValues[i], pathValues[i+1])
	}
	return req
}

func expectStatusAndCode(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if w.Code != status || !strings.Contains(w.Body.String(), `"`+code+`"`) {
		t.Fatalf("expected %d %s, got %d: %s", status, code, w.Code, w.Body.String())
	}
}

func hlcBody(fields string, hlcPhysical int64) string {
	return fmt.Sprintf(`{%s, "hlc_physical": %d, "hlc_counter": 0, "hlc_node_id": %q}`, fields, hlcPhysical, refNode)
}

func queryString(t *testing.T, conn *sql.DB, query string, args ...any) string {
	t.Helper()
	var s string
	if err := conn.QueryRow(query, args...).Scan(&s); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return s
}

func queryInt(t *testing.T, conn *sql.DB, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := conn.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return n
}

// restSeed runs referenceSyncSetup (account, group, categories C/D, payees
// P/Q, all at HLC 1000) against conn.
func restSeed(t *testing.T) (*sql.DB, *SyncHandler) {
	t.Helper()
	conn := newTestDB(t)
	sh := &SyncHandler{DB: conn}
	referenceSyncSetup(t, sh)
	return conn, sh
}

func TestREST_AccountPatchWithOlderHLCIsStale(t *testing.T) {
	conn, _ := restSeed(t)
	w := httptest.NewRecorder()

	(&AccountsHandler{DB: conn}).Update(w, restRequest(http.MethodPatch, "/api/accounts/"+refAccount,
		hlcBody(`"name": "Older rename"`, 500), "id", refAccount))

	expectStatusAndCode(t, w, http.StatusConflict, "STALE_WRITE")
	if name := queryString(t, conn, `SELECT name FROM accounts WHERE id = ?`, refAccount); name != "Checking" {
		t.Fatalf("expected account unchanged, got name %q", name)
	}
}

func TestREST_CategoryPatchWithOlderHLCIsStale(t *testing.T) {
	conn, _ := restSeed(t)
	w := httptest.NewRecorder()

	(&CategoriesHandler{DB: conn}).Update(w, restRequest(http.MethodPatch, "/api/categories/"+refCatC,
		hlcBody(`"name": "Older rename"`, 500), "id", refCatC))

	expectStatusAndCode(t, w, http.StatusConflict, "STALE_WRITE")
	if name := queryString(t, conn, `SELECT name FROM categories WHERE id = ?`, refCatC); name != refCatC {
		t.Fatalf("expected category unchanged, got name %q", name)
	}
}

func TestREST_PayeePatchWithOlderHLCIsStale(t *testing.T) {
	conn, _ := restSeed(t)
	w := httptest.NewRecorder()

	(&PayeesHandler{DB: conn}).Update(w, restRequest(http.MethodPatch, "/api/payees/"+refPayeeP,
		hlcBody(`"name": "Older rename"`, 500), "id", refPayeeP))

	expectStatusAndCode(t, w, http.StatusConflict, "STALE_WRITE")
	if name := queryString(t, conn, `SELECT name FROM payees WHERE id = ?`, refPayeeP); name != refPayeeP {
		t.Fatalf("expected payee unchanged, got name %q", name)
	}
}

func TestREST_TransactionPatchWithOlderHLCIsStale(t *testing.T) {
	conn, sh := restSeed(t)
	expectApplied(t, postSync(t, sh, syncBody(refTxnUpsert("", refCatC, "", false, 2000))))
	w := httptest.NewRecorder()

	(&TransactionsHandler{DB: conn}).Update(w, restRequest(http.MethodPatch, "/api/transactions/"+refTxn,
		hlcBody(`"amount": -999`, 1500), "id", refTxn))

	expectStatusAndCode(t, w, http.StatusConflict, "STALE_WRITE")
	if amount := queryInt(t, conn, `SELECT amount FROM transactions WHERE id = ?`, refTxn); amount != -500 {
		t.Fatalf("expected transaction unchanged, got amount %d", amount)
	}
}

func TestREST_BudgetPutWithOlderHLCIsStale(t *testing.T) {
	conn, sh := restSeed(t)
	expectApplied(t, postSync(t, sh, syncBody(fmt.Sprintf(`{"table": "budget_entries", "op": "upsert", "group_id": null, "row": {
		"id": %q, "category_id": %q, "month": "2026-01", "budgeted": 100,
		"hlc_physical": 2000, "hlc_counter": 0, "hlc_node_id": %q
	}}`, refEntry, refCatC, refNode))))
	w := httptest.NewRecorder()

	(&BudgetHandler{DB: conn}).Set(w, restRequest(http.MethodPut, "/api/budget/2026-01/"+refCatC,
		hlcBody(fmt.Sprintf(`"id": %q, "budgeted": 700`, refEntry), 1500), "month", "2026-01", "category_id", refCatC))

	expectStatusAndCode(t, w, http.StatusConflict, "STALE_WRITE")
	if budgeted := queryInt(t, conn, `SELECT budgeted FROM budget_entries WHERE id = ?`, refEntry); budgeted != 100 {
		t.Fatalf("expected budget entry unchanged, got budgeted %d", budgeted)
	}
}

// restTransferSeed adds a second account and a REST-created transfer:
// leg 1 -1000 in the setup account, leg 2 +1000 in the second one, HLC 1000.
func restTransferSeed(t *testing.T) (*sql.DB, *SyncHandler) {
	t.Helper()
	conn, sh := restSeed(t)
	expectApplied(t, postSync(t, sh, syncBody(fmt.Sprintf(`{"table": "accounts", "op": "upsert", "group_id": null, "row": {
		"id": %q, "name": "Savings", "type": "savings", "on_budget": true, "closed": false, "currency": "USD",
		"hlc_physical": 1000, "hlc_counter": 9, "hlc_node_id": %q
	}}`, restSecondAccount, refNode))))

	if _, _, err := db.CreateTransfer(t.Context(), conn, db.NewTransfer{
		ID: transferLeg1, AccountID: refAccount,
		TransferTransactionID: transferLeg2, TransferAccountID: restSecondAccount,
		Date: "2026-01-01", Amount: -1000,
		HLCPhysical: 1000, HLCCounter: 0, HLCNodeID: refNode,
		TransferHLCPhysical: 1000, TransferHLCCounter: 1, TransferHLCNodeID: refNode,
	}); err != nil {
		t.Fatalf("CreateTransfer: %v", err)
	}
	return conn, sh
}

func TestREST_DeleteTransferLegDeletesBothLegs(t *testing.T) {
	conn, sh := restTransferSeed(t)
	w := httptest.NewRecorder()

	(&TransactionsHandler{DB: conn}).Delete(w, restRequest(http.MethodDelete, "/api/transactions/"+transferLeg2,
		fmt.Sprintf(`{"hlc_physical": 2000, "hlc_counter": 0, "hlc_node_id": %q}`, refNode), "id", transferLeg2))

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}
	if !rowIsDeleted(t, sh, "transactions", transferLeg1) || !rowIsDeleted(t, sh, "transactions", transferLeg2) {
		t.Fatalf("expected both transfer legs deleted")
	}
}

func TestREST_PatchTransferLegAmountMirrorsSibling(t *testing.T) {
	conn, _ := restTransferSeed(t)
	w := httptest.NewRecorder()

	(&TransactionsHandler{DB: conn}).Update(w, restRequest(http.MethodPatch, "/api/transactions/"+transferLeg1,
		hlcBody(`"amount": -1500, "cleared": true`, 2000), "id", transferLeg1))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	leg1 := queryInt(t, conn, `SELECT amount FROM transactions WHERE id = ?`, transferLeg1)
	leg2 := queryInt(t, conn, `SELECT amount FROM transactions WHERE id = ?`, transferLeg2)
	if leg1 != -1500 || leg2 != 1500 {
		t.Fatalf("expected -1500 / +1500, got %d / %d", leg1, leg2)
	}
	if cleared := queryInt(t, conn, `SELECT cleared FROM transactions WHERE id = ?`, transferLeg2); cleared != 0 {
		t.Fatalf("expected cleared to apply to leg 1 only")
	}
}

func TestREST_PatchTransferLegAccountIDRejected(t *testing.T) {
	conn, _ := restTransferSeed(t)
	w := httptest.NewRecorder()

	(&TransactionsHandler{DB: conn}).Update(w, restRequest(http.MethodPatch, "/api/transactions/"+transferLeg1,
		hlcBody(fmt.Sprintf(`"account_id": %q`, restSecondAccount), 2000), "id", transferLeg1))

	expectStatusAndCode(t, w, http.StatusBadRequest, "VALIDATION_ERROR")
	if account := queryString(t, conn, `SELECT account_id FROM transactions WHERE id = ?`, transferLeg1); account != refAccount {
		t.Fatalf("expected leg 1 unchanged, got account %s", account)
	}
}

func TestREST_DeleteCategoryReassignWithNewerMovedTransactionIsStale(t *testing.T) {
	conn, sh := restSeed(t)
	const newerTxn = "7b7b7b7b-7b7b-7b7b-7b7b-7b7b7b7b7b7b"
	expectApplied(t, postSync(t, sh, syncBody(refTxnUpsert("", refCatC, "", false, 1000))))
	expectApplied(t, postSync(t, sh, syncBody(fmt.Sprintf(`{"table": "transactions", "op": "upsert", "group_id": null, "row": {
		"id": %q, "account_id": %q, "category_id": %q, "date": "2026-01-01", "amount": -300, "cleared": false, "notes": "",
		"hlc_physical": 5000, "hlc_counter": 0, "hlc_node_id": %q
	}}`, newerTxn, refAccount, refCatC, refNode))))
	w := httptest.NewRecorder()

	(&CategoriesHandler{DB: conn}).Update(w, restRequest(http.MethodPatch, "/api/categories/"+refCatC,
		hlcBody(fmt.Sprintf(`"delete": true, "reassign_to": %q`, refCatD), 3000), "id", refCatC))

	expectStatusAndCode(t, w, http.StatusConflict, "STALE_WRITE")
	if rowIsDeleted(t, sh, "categories", refCatC) {
		t.Fatalf("expected category C not deleted")
	}
	for _, id := range []string{refTxn, newerTxn} {
		if category := queryString(t, conn, `SELECT category_id FROM transactions WHERE id = ?`, id); category != refCatC {
			t.Fatalf("expected transaction %s still in C, got %s", id, category)
		}
	}
	if hlc := queryInt(t, conn, `SELECT hlc_physical FROM transactions WHERE id = ?`, refTxn); hlc != 1000 {
		t.Fatalf("expected the older moved transaction untouched, got hlc %d", hlc)
	}
}
