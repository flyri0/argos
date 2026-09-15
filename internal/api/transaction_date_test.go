package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTransactionDate_RoundTripsAsYYYYMMDD(t *testing.T) {
	conn := newTestDB(t)
	sh := &SyncHandler{DB: conn}
	referenceSyncSetup(t, sh)
	expectApplied(t, postSync(t, sh, syncBody(refTxnUpsert("", refCatC, "", false, 2000))))

	var pulled map[string]any
	for _, c := range postSync(t, sh, `{"since": 0, "mutations": []}`).Changes {
		if row, ok := c.Row.(map[string]any); ok && c.Table == "transactions" && row["id"] == refTxn {
			pulled = row
		}
	}
	if pulled == nil {
		t.Fatalf("transaction not pulled")
	}
	if pulled["date"] != "2026-01-01" {
		t.Fatalf("expected /sync to return date 2026-01-01, got %v", pulled["date"])
	}

	req := httptest.NewRequest(http.MethodGet, "/api/transactions?account_id="+refAccount, nil)
	w := httptest.NewRecorder()
	(&TransactionsHandler{DB: conn}).List(w, req)
	var listed []transaction
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	if len(listed) != 1 || listed[0].Date != "2026-01-01" {
		t.Fatalf("expected GET /api/transactions to return date 2026-01-01, got %+v", listed)
	}

	// A client re-upserting the pulled row as-is (e.g. a cleared toggle with
	// a fresh HLC) must be accepted.
	pulled["hlc_physical"] = 3000
	pulled["cleared"] = true
	body, err := json.Marshal(map[string]any{
		"since":     0,
		"mutations": []map[string]any{{"table": "transactions", "op": "upsert", "group_id": nil, "row": pulled}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	expectApplied(t, postSync(t, sh, bytes.NewBuffer(body).String()))
}
