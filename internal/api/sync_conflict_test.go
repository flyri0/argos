package api

import (
	"fmt"
	"testing"
)

// changeRows returns the rows in resp.Changes for table, keyed by id.
func changeRows(resp syncResponse, table string) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, c := range resp.Changes {
		if row, ok := c.Row.(map[string]any); ok && c.Table == table {
			out[row["id"].(string)] = row
		}
	}
	return out
}

func TestSync_ConflictRejectionsReturnCanonicalRows(t *testing.T) {
	t.Run("reference_deleted_returns_the_transaction", func(t *testing.T) {
		h := newTestSyncHandler(t)
		referenceSyncSetup(t, h)
		expectApplied(t, postSync(t, h, syncBody(refTxnUpsert("", refCatC, "", false, 2000))))
		expectApplied(t, postSync(t, h, syncBody(refDelete("", "categories", refCatD, 2500))))
		cursor := postSync(t, h, `{"since": 0, "mutations": []}`).ServerVersion

		resp := postSync(t, h, fmt.Sprintf(`{"since": %d, "mutations": [%s]}`, cursor, refTxnUpsert("", refCatD, "", false, 3000)))

		expectRejectedCode(t, resp, "REFERENCE_DELETED")
		if len(resp.Changes) != 1 {
			t.Fatalf("expected exactly the transaction back, got %d changes: %+v", len(resp.Changes), resp.Changes)
		}
		txn, ok := changeRows(resp, "transactions")[refTxn]
		if !ok || txn["category_id"] != refCatC {
			t.Fatalf("expected the stored transaction (still in C), got %+v", resp.Changes)
		}
	})

	t.Run("category_in_use_returns_the_category", func(t *testing.T) {
		h := newTestSyncHandler(t)
		referenceSyncSetup(t, h)
		expectApplied(t, postSync(t, h, syncBody(refTxnUpsert("", refCatC, "", false, 2000))))
		cursor := postSync(t, h, `{"since": 0, "mutations": []}`).ServerVersion

		resp := postSync(t, h, fmt.Sprintf(`{"since": %d, "mutations": [%s]}`, cursor, refDelete("", "categories", refCatC, 3000)))

		expectRejectedCode(t, resp, "CATEGORY_IN_USE_NEEDS_REASSIGN")
		if len(resp.Changes) != 1 {
			t.Fatalf("expected exactly the category back, got %d changes: %+v", len(resp.Changes), resp.Changes)
		}
		category, ok := changeRows(resp, "categories")[refCatC]
		if !ok || category["deleted_at"] != nil {
			t.Fatalf("expected the live category C, got %+v", resp.Changes)
		}
	})

	t.Run("malformed_mutation_returns_no_extra_rows", func(t *testing.T) {
		h := newTestSyncHandler(t)
		referenceSyncSetup(t, h)
		expectApplied(t, postSync(t, h, syncBody(refTxnUpsert("", refCatC, "", false, 2000))))
		cursor := postSync(t, h, `{"since": 0, "mutations": []}`).ServerVersion

		resp := postSync(t, h, fmt.Sprintf(`{"since": %d, "mutations": [
			{"table": "transactions", "op": "upsert", "group_id": null, "row": {
				"id": %q, "account_id": %q, "category_id": %q, "payee_id": null,
				"date": "not-a-date", "amount": -500, "cleared": false, "notes": "",
				"hlc_physical": 3000, "hlc_counter": 0, "hlc_node_id": %q
			}}
		]}`, cursor, refTxn, refAccount, refCatC, refNode))

		expectRejectedCode(t, resp, "SYNC_MUTATION_INVALID")
		if len(resp.Changes) != 0 {
			t.Fatalf("expected no rows back for a non-conflict rejection, got %+v", resp.Changes)
		}
	})
}
