package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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

	// A second write with an HLC equal to (not just less than) the existing
	// row's must also be rejected as stale (§2.3's "greater or equal" rule).
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
