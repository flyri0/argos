package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"

	"argos/internal/budget"
	"argos/internal/db"
	hlc "argos/internal/sync"
)

// Multi-device convergence harness. Each simDevice mirrors the web client:
// a local replica (web/src/db), an outbox, writes with the same semantics as
// web/src/db/{helpers,transfers,reassign,budget}.ts, a persisted HLC
// (web/src/db/hlc.ts), and a sync step applying the response exactly like
// web/src/sync/engine.ts. Scenarios drive several devices against one real
// server database and check that every device ends up holding exactly the
// server's rows, with the financial invariants intact everywhere.

var convTables = []string{"accounts", "category_groups", "categories", "payees", "transactions", "budget_entries"}

type devRow = map[string]any

type simOutboxEntry struct {
	table   string
	op      string
	groupID *string
	row     devRow
	synced  bool
}

type simDevice struct {
	name   string
	nodeID string
	// The device's clock is base + offset + ticks; every reading advances it
	// by 1ms so writes are strictly ordered and runs are deterministic.
	base   time.Time
	offset time.Duration
	ticks  time.Duration

	rows   map[string]map[string]devRow
	outbox []*simOutboxEntry
	cursor int64
	syncID string

	last      hlc.HLC
	persisted hlc.HLC // what localStorage would hold (§2.3)

	results []syncResult // every result this device received, for assertions
}

type simSyncOpts struct {
	dropResponse bool // the server applied the push, but the client never sees the body
	firstUnits   int  // push only the first N units (0 = all)
}

type convSnapshot map[string]map[string]devRow

func newSimDevice(name string, base time.Time, offset time.Duration) *simDevice {
	d := &simDevice{name: name, nodeID: uuid.NewString(), base: base, offset: offset, rows: map[string]map[string]devRow{}}
	for _, table := range convTables {
		d.rows[table] = map[string]devRow{}
	}
	d.last = hlc.HLC{NodeID: d.nodeID}
	d.persisted = d.last
	return d
}

func convMonth() string { return time.Now().Format("2006-01") }

func (d *simDevice) nowMs() int64 {
	d.ticks += time.Millisecond
	return d.base.Add(d.offset + d.ticks).UnixMilli()
}

// nextHLC mirrors nextHlc: advance on the wall clock, but never to or below
// anything already produced or observed.
func (d *simDevice) nextHLC() hlc.HLC {
	next := hlc.Now(d.nodeID, d.nowMs())
	if hlc.Compare(next, d.last) <= 0 {
		next = hlc.Observe(d.last, next)
	}
	d.last = next
	d.persisted = next
	return next
}

func (d *simDevice) observe(received hlc.HLC) {
	d.last = hlc.Observe(d.last, received)
	d.persisted = d.last
}

// reload simulates a page reload: in-memory clock state is gone, and the
// persisted value is what the clock resumes from.
func (d *simDevice) reload() {
	d.last = d.persisted
}

func rowInt(r devRow, key string) int64 {
	switch v := r[key].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	}
	return 0
}

func rowStr(r devRow, key string) string {
	s, _ := r[key].(string)
	return s
}

func rowBool(r devRow, key string) bool {
	b, _ := r[key].(bool)
	return b
}

func rowDeleted(r devRow) bool { return r["deleted_at"] != nil }

func rowHLC(r devRow) hlc.HLC {
	return hlc.HLC{Physical: rowInt(r, "hlc_physical"), Counter: rowInt(r, "hlc_counter"), NodeID: rowStr(r, "hlc_node_id")}
}

// truncateTransactionDate mirrors normalizeTransactionDate (§2.4).
func truncateTransactionDate(r devRow) {
	if s, ok := r["date"].(string); ok && len(s) > 10 {
		r["date"] = s[:10]
	}
}

func cloneRow(r devRow) devRow {
	out := make(devRow, len(r))
	for k, v := range r {
		out[k] = v
	}
	return out
}

// normalizeRow round-trips a row through JSON (numbers kept exact) so rows
// built locally, decoded from a response, or read from the server compare
// equal field by field.
func normalizeRow(v any) devRow {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var out devRow
	if err := dec.Decode(&out); err != nil {
		panic(err)
	}
	return out
}

func sortedRowIDs(m map[string]devRow) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func newGroupID() *string {
	id := uuid.NewString()
	return &id
}

// --- local writes (web/src/db semantics) ---

// enqueue mirrors enqueueRowMutation: a tombstoned row becomes a "delete"
// carrying only the tombstone columns; anything else is a full-row upsert.
func (d *simDevice) enqueue(table string, r devRow, groupID *string) {
	e := &simOutboxEntry{table: table, groupID: groupID}
	if rowDeleted(r) {
		e.op = "delete"
		e.row = devRow{
			"id": r["id"], "deleted_at": r["deleted_at"],
			"hlc_physical": r["hlc_physical"], "hlc_counter": r["hlc_counter"], "hlc_node_id": r["hlc_node_id"],
		}
	} else {
		e.op = "upsert"
		e.row = cloneRow(r)
		if table == "transactions" {
			truncateTransactionDate(e.row)
		}
	}
	d.outbox = append(d.outbox, e)
}

// put stamps a fresh HLC on r, stores it locally, and enqueues it.
func (d *simDevice) put(table string, r devRow, groupID *string) {
	stamp := d.nextHLC()
	r["hlc_physical"], r["hlc_counter"], r["hlc_node_id"] = stamp.Physical, stamp.Counter, stamp.NodeID
	d.rows[table][rowStr(r, "id")] = cloneRow(r)
	d.enqueue(table, r, groupID)
}

func (d *simDevice) local(table, id string) devRow {
	r, ok := d.rows[table][id]
	if !ok {
		panic(fmt.Sprintf("%s: no local %s row %s", d.name, table, id))
	}
	return cloneRow(r)
}

func (d *simDevice) update(table, id string, changes devRow, groupID *string) {
	r := d.local(table, id)
	for k, v := range changes {
		r[k] = v
	}
	d.put(table, r, groupID)
}

func (d *simDevice) softDelete(table, id string, groupID *string) {
	r := d.local(table, id)
	r["deleted_at"] = d.nowMs()
	d.put(table, r, groupID)
}

func (d *simDevice) createAccount(name string) string {
	id := uuid.NewString()
	d.put("accounts", devRow{
		"id": id, "name": name, "type": "checking", "on_budget": true, "closed": false,
		"currency": "USD", "notes": nil, "deleted_at": nil,
	}, nil)
	return id
}

func (d *simDevice) createGroup(name string) string {
	id := uuid.NewString()
	d.put("category_groups", devRow{"id": id, "name": name, "is_income": false, "sort_order": 0, "deleted_at": nil}, nil)
	return id
}

func (d *simDevice) createCategory(groupID, name string) string {
	id := uuid.NewString()
	d.put("categories", devRow{
		"id": id, "group_id": groupID, "name": name, "hidden": false, "sort_order": 0, "notes": nil, "deleted_at": nil,
	}, nil)
	return id
}

func (d *simDevice) createPayee(name string) string {
	id := uuid.NewString()
	d.put("payees", devRow{"id": id, "name": name, "deleted_at": nil}, nil)
	return id
}

func (d *simDevice) createTransaction(accountID, categoryID string, amount int64) string {
	id := uuid.NewString()
	var category any
	if categoryID != "" {
		category = categoryID
	}
	d.put("transactions", devRow{
		"id": id, "account_id": accountID, "category_id": category, "payee_id": nil, "parent_id": nil,
		"date": convMonth() + "-01", "amount": amount, "cleared": false, "notes": "", "transfer_id": nil, "deleted_at": nil,
	}, nil)
	return id
}

// createTransfer mirrors createTransfer: two legs, one fresh group.
func (d *simDevice) createTransfer(fromAccount, toAccount string, amount int64) (string, string) {
	group := newGroupID()
	transferID := uuid.NewString()
	leg := func(accountID string, amount int64) string {
		id := uuid.NewString()
		d.put("transactions", devRow{
			"id": id, "account_id": accountID, "category_id": nil, "payee_id": nil, "parent_id": nil,
			"date": convMonth() + "-01", "amount": amount, "cleared": false, "notes": "", "transfer_id": transferID, "deleted_at": nil,
		}, group)
		return id
	}
	return leg(fromAccount, amount), leg(toAccount, -amount)
}

func (d *simDevice) liveSibling(legID string) string {
	transferID := rowStr(d.rows["transactions"][legID], "transfer_id")
	for _, id := range sortedRowIDs(d.rows["transactions"]) {
		r := d.rows["transactions"][id]
		if id != legID && rowStr(r, "transfer_id") == transferID && !rowDeleted(r) {
			return id
		}
	}
	return ""
}

// updateTransfer mirrors updateTransfer: both legs, one fresh group.
func (d *simDevice) updateTransfer(legID string, amount int64) {
	group := newGroupID()
	sibling := d.liveSibling(legID)
	d.update("transactions", legID, devRow{"amount": amount}, group)
	if sibling != "" {
		d.update("transactions", sibling, devRow{"amount": -amount}, group)
	}
}

// deleteTransfer mirrors deleteTransaction on a transfer leg.
func (d *simDevice) deleteTransfer(legID string) {
	group := newGroupID()
	sibling := d.liveSibling(legID)
	d.softDelete("transactions", legID, group)
	if sibling != "" {
		d.softDelete("transactions", sibling, group)
	}
}

// setTransferLegCleared mirrors setTransferLegCleared: the sibling is only
// re-stamped, but travels in the same group.
func (d *simDevice) setTransferLegCleared(legID string, cleared bool) {
	group := newGroupID()
	sibling := d.liveSibling(legID)
	d.update("transactions", legID, devRow{"cleared": cleared}, group)
	if sibling != "" {
		d.update("transactions", sibling, devRow{}, group)
	}
}

// findBudgetPair mirrors findBudgetEntryForPair: the live row, else the most
// recent tombstone.
func (d *simDevice) findBudgetPair(categoryID, month string) devRow {
	var best devRow
	for _, id := range sortedRowIDs(d.rows["budget_entries"]) {
		r := d.rows["budget_entries"][id]
		if rowStr(r, "category_id") != categoryID || rowStr(r, "month") != month {
			continue
		}
		if !rowDeleted(r) {
			return r
		}
		if best == nil || hlc.Compare(rowHLC(r), rowHLC(best)) > 0 {
			best = r
		}
	}
	return best
}

// setBudgetedAmount mirrors setBudgetedAmount.
func (d *simDevice) setBudgetedAmount(categoryID, month string, amount int64) {
	existing := d.findBudgetPair(categoryID, month)
	switch {
	case existing == nil:
		if amount == 0 {
			return
		}
		d.put("budget_entries", devRow{
			"id": db.BudgetEntryID(categoryID, month), "category_id": categoryID, "month": month, "budgeted": amount, "deleted_at": nil,
		}, nil)
	case rowDeleted(existing):
		if amount == 0 {
			return
		}
		d.update("budget_entries", rowStr(existing, "id"), devRow{"budgeted": amount, "deleted_at": nil}, nil)
	case amount == 0:
		d.softDelete("budget_entries", rowStr(existing, "id"), nil)
	default:
		d.update("budget_entries", rowStr(existing, "id"), devRow{"budgeted": amount}, nil)
	}
}

// reassignCategory mirrors reassignCategory: move live transactions, merge
// each month's budget entry into the target, delete the source — one group.
func (d *simDevice) reassignCategory(sourceID, targetID string) {
	group := newGroupID()
	for _, id := range sortedRowIDs(d.rows["transactions"]) {
		r := d.rows["transactions"][id]
		if !rowDeleted(r) && rowStr(r, "category_id") == sourceID {
			d.update("transactions", id, devRow{"category_id": targetID}, group)
		}
	}
	for _, id := range sortedRowIDs(d.rows["budget_entries"]) {
		entry := d.rows["budget_entries"][id]
		if rowDeleted(entry) || rowStr(entry, "category_id") != sourceID {
			continue
		}
		month, amount := rowStr(entry, "month"), rowInt(entry, "budgeted")
		if target := d.findBudgetPair(targetID, month); target == nil {
			d.put("budget_entries", devRow{
				"id": db.BudgetEntryID(targetID, month), "category_id": targetID, "month": month, "budgeted": amount, "deleted_at": nil,
			}, group)
		} else {
			base := int64(0)
			if !rowDeleted(target) {
				base = rowInt(target, "budgeted")
			}
			d.update("budget_entries", rowStr(target, "id"), devRow{"budgeted": base + amount, "deleted_at": nil}, group)
		}
		d.softDelete("budget_entries", id, group)
	}
	d.softDelete("categories", sourceID, group)
}

// --- sync (web/src/sync/engine.ts semantics) ---

// groupOutbox mirrors groupOutboxEntries / the server's groupMutations.
func groupOutbox(entries []*simOutboxEntry) [][]*simOutboxEntry {
	var units [][]*simOutboxEntry
	unitByGroup := map[string]int{}
	for _, e := range entries {
		if e.groupID == nil {
			units = append(units, []*simOutboxEntry{e})
			continue
		}
		if idx, ok := unitByGroup[*e.groupID]; ok {
			units[idx] = append(units[idx], e)
			continue
		}
		unitByGroup[*e.groupID] = len(units)
		units = append(units, []*simOutboxEntry{e})
	}
	return units
}

func (d *simDevice) sync(t *testing.T, h *SyncHandler, opts simSyncOpts) syncResponse {
	t.Helper()

	var pending []*simOutboxEntry
	for _, e := range d.outbox {
		if !e.synced {
			pending = append(pending, e)
		}
	}
	units := groupOutbox(pending)
	if opts.firstUnits > 0 && opts.firstUnits < len(units) {
		units = units[:opts.firstUnits]
		included := map[*simOutboxEntry]bool{}
		for _, u := range units {
			for _, e := range u {
				included[e] = true
			}
		}
		var kept []*simOutboxEntry
		for _, e := range pending {
			if included[e] {
				kept = append(kept, e)
			}
		}
		pending = kept
	}

	type wireMutation struct {
		Table   string  `json:"table"`
		Op      string  `json:"op"`
		GroupID *string `json:"group_id"`
		Row     devRow  `json:"row"`
	}
	mutations := make([]wireMutation, 0, len(pending))
	for _, e := range pending {
		mutations = append(mutations, wireMutation{Table: e.table, Op: e.op, GroupID: e.groupID, Row: e.row})
	}
	body, err := json.Marshal(map[string]any{"since": d.cursor, "mutations": mutations})
	if err != nil {
		t.Fatalf("%s: marshal sync request: %v", d.name, err)
	}

	w := httptest.NewRecorder()
	h.Handle(w, httptest.NewRequest(http.MethodPost, "/sync", bytes.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("%s: /sync returned %d: %s", d.name, w.Code, w.Body.String())
	}
	dec := json.NewDecoder(w.Body)
	dec.UseNumber()
	var resp syncResponse
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("%s: decode sync response: %v", d.name, err)
	}
	if len(resp.Results) != len(units) {
		t.Fatalf("%s: expected %d results, got %d", d.name, len(units), len(resp.Results))
	}
	d.results = append(d.results, resp.Results...)

	if opts.dropResponse {
		return resp
	}
	if d.syncID != "" && d.syncID != resp.SyncID {
		t.Fatalf("%s: unexpected sync_id change", d.name)
	}
	d.syncID = resp.SyncID

	// Changes first (including the winning rows for stale units), then results.
	returned := map[string]bool{}
	for _, c := range resp.Changes {
		r := normalizeRow(c.Row)
		if c.Table == "transactions" {
			truncateTransactionDate(r)
		}
		d.observe(rowHLC(r))
		id := rowStr(r, "id")
		returned[c.Table+":"+id] = true
		d.rows[c.Table][id] = r
		if c.Table == "budget_entries" {
			for otherID, other := range d.rows["budget_entries"] {
				if otherID != id && !rowDeleted(other) &&
					rowStr(other, "category_id") == rowStr(r, "category_id") && rowStr(other, "month") == rowStr(r, "month") {
					other["deleted_at"] = d.nowMs()
				}
			}
		}
	}
	// Mirrors purgeLocalOnlyRows: a rolled-back unit's row that the server
	// didn't return never existed there, so drop it if it was never
	// acknowledged and nothing still waiting to sync refers to it.
	pushed := map[*simOutboxEntry]bool{}
	for _, u := range units {
		for _, e := range u {
			pushed[e] = true
		}
	}
	for i, u := range units {
		if !returnsCanonicalRows(resp.Results[i]) {
			continue
		}
		for _, e := range u {
			id := rowStr(e.row, "id")
			if returned[e.table+":"+id] {
				continue
			}
			waiting := false
			for _, other := range d.outbox {
				if !other.synced && !pushed[other] && other.table == e.table && rowStr(other.row, "id") == id {
					waiting = true
					break
				}
			}
			if local, ok := d.rows[e.table][id]; ok && !waiting && local["server_version"] == nil {
				delete(d.rows[e.table], id)
			}
		}
	}

	// Mirrors markOutboxResults: applied, stale, and conflict-coded rejections
	// are terminal; any other rejected_invalid stays queued.
	for i, u := range units {
		if resp.Results[i].Status != "applied" && !returnsCanonicalRows(resp.Results[i]) {
			continue
		}
		for _, e := range u {
			e.synced = true
		}
	}
	d.cursor = resp.ServerVersion
	return resp
}

// --- assertions ---

func deviceSnapshot(d *simDevice) convSnapshot {
	snap := convSnapshot{}
	for _, table := range convTables {
		snap[table] = map[string]devRow{}
		for id, r := range d.rows[table] {
			n := normalizeRow(r)
			delete(n, "server_version")
			snap[table][id] = n
		}
	}
	return snap
}

func serverSnapshot(t *testing.T, h *SyncHandler) convSnapshot {
	t.Helper()
	changes, err := h.changesSince(context.Background(), 0)
	if err != nil {
		t.Fatalf("changesSince(0): %v", err)
	}
	snap := convSnapshot{}
	for _, table := range convTables {
		snap[table] = map[string]devRow{}
	}
	for _, c := range changes {
		r := normalizeRow(c.Row)
		delete(r, "server_version")
		snap[c.Table][rowStr(r, "id")] = r
	}
	return snap
}

func serverRow(t *testing.T, h *SyncHandler, table, id string) devRow {
	t.Helper()
	r, ok := serverSnapshot(t, h)[table][id]
	if !ok {
		t.Fatalf("no server %s row %s", table, id)
	}
	return r
}

func compareSnapshots(t *testing.T, label string, server, device convSnapshot) {
	t.Helper()
	for _, table := range convTables {
		for _, id := range sortedRowIDs(server[table]) {
			want := server[table][id]
			got, ok := device[table][id]
			if !ok {
				t.Errorf("%s: missing %s row %s (server: %v)", label, table, id, want)
				continue
			}
			if !reflect.DeepEqual(want, got) {
				t.Errorf("%s: %s row %s differs\n  server: %v\n  device: %v", label, table, id, want, got)
			}
		}
		for _, id := range sortedRowIDs(device[table]) {
			if _, ok := server[table][id]; !ok {
				t.Errorf("%s: %s row %s exists only on the device: %v", label, table, id, device[table][id])
			}
		}
	}
}

// assertFinancialInvariants checks the transfer pair invariant (§5.2) and
// the to_budget invariant (§5.3) on a snapshot, and returns its to_budget for
// the current month.
func assertFinancialInvariants(t *testing.T, label string, s convSnapshot) int64 {
	t.Helper()

	legs := map[string][]devRow{}
	for _, r := range s["transactions"] {
		if transferID := rowStr(r, "transfer_id"); transferID != "" {
			legs[transferID] = append(legs[transferID], r)
		}
	}
	for transferID, pair := range legs {
		if len(pair) != 2 {
			t.Errorf("%s: transfer %s has %d legs", label, transferID, len(pair))
			continue
		}
		a, b := pair[0], pair[1]
		switch {
		case rowStr(a, "account_id") == rowStr(b, "account_id"):
			t.Errorf("%s: transfer %s legs share an account", label, transferID)
		case rowDeleted(a) && rowDeleted(b):
		case !rowDeleted(a) && !rowDeleted(b) && rowInt(a, "amount")+rowInt(b, "amount") == 0:
		default:
			t.Errorf("%s: transfer %s legs are not a valid pair: %v / %v", label, transferID, a, b)
		}
	}

	month := convMonth()
	onBudget := map[string]bool{}
	for id, r := range s["accounts"] {
		if !rowDeleted(r) && rowBool(r, "on_budget") {
			onBudget[id] = true
		}
	}
	incomeGroups := map[string]bool{}
	for id, r := range s["category_groups"] {
		if !rowDeleted(r) && rowBool(r, "is_income") {
			incomeGroups[id] = true
		}
	}
	var txns []budget.Transaction
	for _, r := range s["transactions"] {
		bt := budget.Transaction{
			AccountID: rowStr(r, "account_id"), CategoryID: rowStr(r, "category_id"), Date: rowStr(r, "date"),
			Amount: rowInt(r, "amount"), OnBudget: onBudget[rowStr(r, "account_id")],
		}
		if rowDeleted(r) {
			deletedAt := rowInt(r, "deleted_at")
			bt.DeletedAt = &deletedAt
		}
		txns = append(txns, bt)
	}
	var entries []budget.BudgetEntry
	for _, r := range s["budget_entries"] {
		if !rowDeleted(r) {
			entries = append(entries, budget.BudgetEntry{CategoryID: rowStr(r, "category_id"), Month: rowStr(r, "month"), Budgeted: rowInt(r, "budgeted")})
		}
	}

	var available []int64
	var availableSum int64
	for id, r := range s["categories"] {
		if rowDeleted(r) || incomeGroups[rowStr(r, "group_id")] {
			continue
		}
		a := budget.RollupCategory(entries, txns, id, month).Available
		available = append(available, a)
		availableSum += a
	}
	var balances []int64
	var balanceSum int64
	for id := range onBudget {
		b := budget.BalanceThrough(id, month, txns)
		balances = append(balances, b)
		balanceSum += b
	}
	toBudget := budget.ToBudget(balances, available)
	if toBudget+availableSum != balanceSum {
		t.Errorf("%s: to_budget invariant broken: %d + %d != %d", label, toBudget, availableSum, balanceSum)
	}
	return toBudget
}

func serverToBudget(t *testing.T, h *SyncHandler) int64 {
	t.Helper()
	month := convMonth()
	req := httptest.NewRequest(http.MethodGet, "/api/budget/"+month, nil)
	req.SetPathValue("month", month)
	w := httptest.NewRecorder()
	(&BudgetHandler{DB: h.DB}).Get(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/budget/%s returned %d: %s", month, w.Code, w.Body.String())
	}
	var out monthBudget
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode budget: %v", err)
	}
	return out.ToBudget
}

// assertConverged gives every device a final sync, then requires each to hold
// exactly the server's rows (server_version aside) and the same to_budget as
// the server computes. If a final sync still pushed something that applied,
// every device syncs once more so the earlier ones see it.
func assertConverged(t *testing.T, h *SyncHandler, devices ...*simDevice) {
	t.Helper()
	for pass := 0; pass < 2; pass++ {
		pushed := false
		for _, d := range devices {
			for _, r := range d.sync(t, h, simSyncOpts{}).Results {
				if r.Status == "applied" {
					pushed = true
				}
			}
		}
		if !pushed {
			break
		}
	}

	server := serverSnapshot(t, h)
	want := assertFinancialInvariants(t, "server", server)
	if got := serverToBudget(t, h); got != want {
		t.Errorf("server GET /api/budget to_budget %d != to_budget computed from its rows %d", got, want)
	}
	for _, d := range devices {
		snap := deviceSnapshot(d)
		compareSnapshots(t, d.name, server, snap)
		if got := assertFinancialInvariants(t, d.name, snap); got != want {
			t.Errorf("%s: to_budget %d != server's %d", d.name, got, want)
		}
	}
}

func assertNoUnexpectedInvalid(t *testing.T, allowedCodes []string, devices ...*simDevice) {
	t.Helper()
	allowed := map[string]bool{}
	for _, code := range allowedCodes {
		allowed[code] = true
	}
	for _, d := range devices {
		for _, r := range d.results {
			if r.Status != "rejected_invalid" {
				continue
			}
			// Conflict rejections are an expected outcome of concurrent edits:
			// the losing device rolls back (§2.4).
			if r.Error == nil || (!allowed[r.Error.Code] && !conflictCodes[r.Error.Code]) {
				t.Errorf("%s: unexpected rejected_invalid for %s %s: %+v", d.name, r.Table, r.ID, r.Error)
			}
		}
	}
}

func finishScenario(t *testing.T, h *SyncHandler, devices ...*simDevice) {
	t.Helper()
	assertConverged(t, h, devices...)
	assertNoUnexpectedInvalid(t, nil, devices...)
}

func expectResultStatuses(t *testing.T, label string, resp syncResponse, want ...string) {
	t.Helper()
	got := make([]string, 0, len(resp.Results))
	for _, r := range resp.Results {
		got = append(got, r.Status)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: expected results %v, got %v (%+v)", label, want, got, resp.Results)
	}
}

var convOrders = []struct {
	name   string
	aFirst bool
}{
	{"A_syncs_first", true},
	{"B_syncs_first", false},
}

func syncInOrder(t *testing.T, h *SyncHandler, aFirst bool, a, b *simDevice) {
	t.Helper()
	if aFirst {
		a.sync(t, h, simSyncOpts{})
		b.sync(t, h, simSyncOpts{})
	} else {
		b.sync(t, h, simSyncOpts{})
		a.sync(t, h, simSyncOpts{})
	}
}

// convBase keeps every device clock within the server's clock-skew bound.
func convBase() time.Time { return time.Now().Add(-time.Minute) }

// --- scenarios ---

func TestSyncConvergence_A_IndependentCreates(t *testing.T) {
	for _, order := range convOrders {
		t.Run(order.name, func(t *testing.T) {
			h, base := newTestSyncHandler(t), convBase()
			a, b := newSimDevice("A", base, 0), newSimDevice("B", base, time.Second)

			a.createPayee("X")
			b.createPayee("Y")
			syncInOrder(t, h, order.aFirst, a, b)

			finishScenario(t, h, a, b)
		})
	}
}

func TestSyncConvergence_B_ConcurrentUpdates(t *testing.T) {
	for _, order := range convOrders {
		t.Run(order.name, func(t *testing.T) {
			h, base := newTestSyncHandler(t), convBase()
			a, b := newSimDevice("A", base, 0), newSimDevice("B", base, time.Second)
			x := a.createPayee("X")
			a.sync(t, h, simSyncOpts{})
			b.sync(t, h, simSyncOpts{})

			a.update("payees", x, devRow{"name": "from A"}, nil)
			b.update("payees", x, devRow{"name": "from B"}, nil)
			want := "from A"
			if hlc.Compare(rowHLC(b.rows["payees"][x]), rowHLC(a.rows["payees"][x])) > 0 {
				want = "from B"
			}
			syncInOrder(t, h, order.aFirst, a, b)

			finishScenario(t, h, a, b)
			if got := rowStr(serverRow(t, h, "payees", x), "name"); got != want {
				t.Fatalf("expected the newer write %q to win, got %q", want, got)
			}
		})
	}
}

func TestSyncConvergence_C_DeleteVersusUpdate(t *testing.T) {
	for _, newer := range []string{"update_newer", "delete_newer"} {
		for _, order := range convOrders {
			t.Run(newer+"/"+order.name, func(t *testing.T) {
				h, base := newTestSyncHandler(t), convBase()
				aOffset, bOffset := time.Duration(0), time.Second
				if newer == "delete_newer" {
					aOffset, bOffset = time.Second, 0
				}
				a, b := newSimDevice("A", base, aOffset), newSimDevice("B", base, bOffset)
				x := a.createPayee("X")
				a.sync(t, h, simSyncOpts{})
				b.sync(t, h, simSyncOpts{})

				a.softDelete("payees", x, nil)
				b.update("payees", x, devRow{"name": "renamed by B"}, nil)
				syncInOrder(t, h, order.aFirst, a, b)

				finishScenario(t, h, a, b)
				r := serverRow(t, h, "payees", x)
				if newer == "update_newer" && (rowDeleted(r) || rowStr(r, "name") != "renamed by B") {
					t.Fatalf("expected the newer rename to win, got %v", r)
				}
				if newer == "delete_newer" && !rowDeleted(r) {
					t.Fatalf("expected the newer delete to win, got %v", r)
				}
			})
		}
	}
}

func TestSyncConvergence_D_LostResponse(t *testing.T) {
	t.Run("standalone_mutation", func(t *testing.T) {
		h, base := newTestSyncHandler(t), convBase()
		a, b := newSimDevice("A", base, 0), newSimDevice("B", base, time.Second)

		x := a.createPayee("X")
		a.sync(t, h, simSyncOpts{dropResponse: true})
		a.update("payees", x, devRow{"name": "renamed before the retry"}, nil)
		resp := a.sync(t, h, simSyncOpts{})
		expectResultStatuses(t, "retry", resp, "rejected_stale", "applied")
		b.sync(t, h, simSyncOpts{})

		finishScenario(t, h, a, b)
		if got := rowStr(serverRow(t, h, "payees", x), "name"); got != "renamed before the retry" {
			t.Fatalf("expected the later rename to survive, got %q", got)
		}
	})

	t.Run("transfer_group_then_edit_before_retry", func(t *testing.T) {
		h, base := newTestSyncHandler(t), convBase()
		a, b := newSimDevice("A", base, 0), newSimDevice("B", base, time.Second)
		checking, savings := a.createAccount("Checking"), a.createAccount("Savings")
		a.sync(t, h, simSyncOpts{})
		b.sync(t, h, simSyncOpts{})

		leg1, leg2 := a.createTransfer(checking, savings, -1000)
		a.sync(t, h, simSyncOpts{dropResponse: true})
		a.updateTransfer(leg1, -2500)
		resp := a.sync(t, h, simSyncOpts{})
		expectResultStatuses(t, "retry", resp, "rejected_stale", "applied")
		b.sync(t, h, simSyncOpts{})

		finishScenario(t, h, a, b)
		if l1, l2 := rowInt(serverRow(t, h, "transactions", leg1), "amount"), rowInt(serverRow(t, h, "transactions", leg2), "amount"); l1 != -2500 || l2 != 2500 {
			t.Fatalf("expected the edit to survive the retry, got %d / %d", l1, l2)
		}
	})
}

func TestSyncConvergence_E_CrashMidBatch(t *testing.T) {
	h, base := newTestSyncHandler(t), convBase()
	a, b := newSimDevice("A", base, 0), newSimDevice("B", base, time.Second)

	checking, savings := a.createAccount("Checking"), a.createAccount("Savings")
	p1 := a.createPayee("P1")
	a.createPayee("P2")
	a.createPayee("P3")
	a.createPayee("P4")
	a.createTransfer(checking, savings, -1000)
	a.update("payees", p1, devRow{"name": "P1 renamed"}, nil)

	a.sync(t, h, simSyncOpts{firstUnits: 3, dropResponse: true})
	resp := a.sync(t, h, simSyncOpts{})
	expectResultStatuses(t, "full batch", resp,
		"rejected_stale", "rejected_stale", "rejected_stale", "applied", "applied", "applied", "applied", "applied")
	b.sync(t, h, simSyncOpts{})

	finishScenario(t, h, a, b)
}

func TestSyncConvergence_F_LongOfflineDevice(t *testing.T) {
	h, base := newTestSyncHandler(t), convBase()
	a := newSimDevice("A", base, -30*24*time.Hour) // its edits were made 30 days ago
	b := newSimDevice("B", base, 0)

	payees := make([]string, 100)
	for i := range payees {
		payees[i] = a.createPayee(fmt.Sprintf("payee %d", i))
	}
	a.sync(t, h, simSyncOpts{})
	b.sync(t, h, simSyncOpts{})

	// A goes offline and keeps editing.
	a.update("payees", payees[0], devRow{"name": "A's offline edit"}, nil)
	z := a.createPayee("created offline")

	// Meanwhile the server receives 2000 changes from B.
	writes := 0
	for round := 0; round < 20; round++ {
		for i, id := range payees {
			b.update("payees", id, devRow{"name": fmt.Sprintf("B round %d payee %d", round, i)}, nil)
			writes++
			if writes%500 == 0 {
				b.sync(t, h, simSyncOpts{})
			}
		}
	}

	resp := a.sync(t, h, simSyncOpts{})
	expectResultStatuses(t, "offline push", resp, "rejected_stale", "applied")

	finishScenario(t, h, a, b)
	if got := rowStr(serverRow(t, h, "payees", payees[0]), "name"); got != "B round 19 payee 0" {
		t.Fatalf("expected B's newer edit to win, got %q", got)
	}
	if rowDeleted(serverRow(t, h, "payees", z)) {
		t.Fatalf("expected A's offline create to apply")
	}
}

func TestSyncConvergence_G_Clocks(t *testing.T) {
	t.Run("ten_years_ahead_is_rejected", func(t *testing.T) {
		h, base := newTestSyncHandler(t), convBase()
		a := newSimDevice("A", base, 0)
		skewed := newSimDevice("Skewed", base, 10*365*24*time.Hour)
		x := a.createPayee("X")
		a.sync(t, h, simSyncOpts{})
		skewed.sync(t, h, simSyncOpts{})
		before := serverRow(t, h, "payees", x)

		skewed.update("payees", x, devRow{"name": "from the future"}, nil)
		resp := skewed.sync(t, h, simSyncOpts{})
		if len(resp.Results) != 1 || resp.Results[0].Status != "rejected_invalid" ||
			resp.Results[0].Error == nil || resp.Results[0].Error.Code != "CLOCK_SKEW_TOO_LARGE" {
			t.Fatalf("expected CLOCK_SKEW_TOO_LARGE, got %+v", resp.Results)
		}
		if after := serverRow(t, h, "payees", x); !reflect.DeepEqual(before, after) {
			t.Fatalf("expected the server unchanged\n  before: %v\n  after: %v", before, after)
		}

		// The skewed device keeps its rejected edit in the outbox by design
		// (rejected_invalid is retried, §2.4), so only the healthy device is
		// expected to converge.
		assertConverged(t, h, a)
		assertNoUnexpectedInvalid(t, nil, a)
		assertNoUnexpectedInvalid(t, []string{"CLOCK_SKEW_TOO_LARGE"}, skewed)
	})

	t.Run("hours_behind_after_reload_still_wins", func(t *testing.T) {
		h, base := newTestSyncHandler(t), convBase()
		a := newSimDevice("A", base, 0)
		b := newSimDevice("B", base, -3*time.Hour)
		x := a.createPayee("X")
		a.sync(t, h, simSyncOpts{})
		b.sync(t, h, simSyncOpts{})

		b.reload()
		b.update("payees", x, devRow{"name": "B after reload"}, nil)
		resp := b.sync(t, h, simSyncOpts{})
		expectResultStatuses(t, "lagging device push", resp, "applied")

		finishScenario(t, h, a, b)
		if got := rowStr(serverRow(t, h, "payees", x), "name"); got != "B after reload" {
			t.Fatalf("expected the lagging device's edit to win, got %q", got)
		}
	})
}

func TestSyncConvergence_H_TransferPairs(t *testing.T) {
	setup := func(t *testing.T) (*SyncHandler, *simDevice, *simDevice, string, string) {
		h, base := newTestSyncHandler(t), convBase()
		a, b := newSimDevice("A", base, 0), newSimDevice("B", base, time.Second)
		checking, savings := a.createAccount("Checking"), a.createAccount("Savings")
		leg1, leg2 := a.createTransfer(checking, savings, -1000)
		a.sync(t, h, simSyncOpts{})
		b.sync(t, h, simSyncOpts{})
		return h, a, b, leg1, leg2
	}

	for _, order := range convOrders {
		t.Run("edit_group_vs_cleared_toggle/"+order.name, func(t *testing.T) {
			h, a, b, leg1, leg2 := setup(t)
			a.updateTransfer(leg1, -2500)
			b.setTransferLegCleared(leg1, true) // newer: B's clock is ahead
			syncInOrder(t, h, order.aFirst, a, b)

			finishScenario(t, h, a, b)
			l1, l2 := serverRow(t, h, "transactions", leg1), serverRow(t, h, "transactions", leg2)
			if rowInt(l1, "amount") != -1000 || rowInt(l2, "amount") != 1000 || !rowBool(l1, "cleared") || rowBool(l2, "cleared") {
				t.Fatalf("expected B's newer pair write to win, got %v / %v", l1, l2)
			}
		})

		t.Run("delete_group_vs_cleared_toggle/"+order.name, func(t *testing.T) {
			h, a, b, leg1, leg2 := setup(t)
			a.deleteTransfer(leg1)
			b.setTransferLegCleared(leg1, true) // newer: B's clock is ahead
			syncInOrder(t, h, order.aFirst, a, b)

			finishScenario(t, h, a, b)
			l1, l2 := serverRow(t, h, "transactions", leg1), serverRow(t, h, "transactions", leg2)
			if rowDeleted(l1) || rowDeleted(l2) || !rowBool(l1, "cleared") {
				t.Fatalf("expected B's newer pair write to revive both legs, got %v / %v", l1, l2)
			}
		})
	}
}

func TestSyncConvergence_I_ReassignVersusOfflineEdit(t *testing.T) {
	for _, newer := range []string{"edit_newer", "reassign_newer"} {
		for _, order := range convOrders {
			t.Run(newer+"/"+order.name, func(t *testing.T) {
				h, base := newTestSyncHandler(t), convBase()
				aOffset, bOffset := time.Duration(0), time.Second
				if newer == "reassign_newer" {
					aOffset, bOffset = time.Second, 0
				}
				a, b := newSimDevice("A", base, aOffset), newSimDevice("B", base, bOffset)
				account := a.createAccount("Checking")
				group := a.createGroup("Bills")
				catC, catD := a.createCategory(group, "C"), a.createCategory(group, "D")
				txn := a.createTransaction(account, catC, -500)
				a.setBudgetedAmount(catC, convMonth(), 1000)
				a.sync(t, h, simSyncOpts{})
				b.sync(t, h, simSyncOpts{})

				a.reassignCategory(catC, catD)
				b.update("transactions", txn, devRow{"amount": -700}, nil)
				syncInOrder(t, h, order.aFirst, a, b)

				finishScenario(t, h, a, b)
			})
		}
	}
}

func TestSyncConvergence_J_Budget(t *testing.T) {
	setup := func(t *testing.T) (*SyncHandler, *simDevice, *simDevice, string) {
		h, base := newTestSyncHandler(t), convBase()
		a, b := newSimDevice("A", base, 0), newSimDevice("B", base, time.Second)
		account := a.createAccount("Checking")
		group := a.createGroup("Bills")
		category := a.createCategory(group, "Groceries")
		a.createTransaction(account, "", 5000)
		a.sync(t, h, simSyncOpts{})
		b.sync(t, h, simSyncOpts{})
		return h, a, b, category
	}

	t.Run("zero_then_rebudget_on_one_device", func(t *testing.T) {
		h, a, b, category := setup(t)
		month := convMonth()
		a.setBudgetedAmount(category, month, 100)
		a.sync(t, h, simSyncOpts{})
		a.setBudgetedAmount(category, month, 0)
		a.setBudgetedAmount(category, month, 50)
		resp := a.sync(t, h, simSyncOpts{})
		expectResultStatuses(t, "zero then re-budget", resp, "applied", "applied")
		b.sync(t, h, simSyncOpts{})

		finishScenario(t, h, a, b)
		r := serverRow(t, h, "budget_entries", db.BudgetEntryID(category, month))
		if rowDeleted(r) || rowInt(r, "budgeted") != 50 {
			t.Fatalf("expected the entry revived at 50, got %v", r)
		}
	})

	for _, order := range convOrders {
		t.Run("first_budget_on_two_offline_devices/"+order.name, func(t *testing.T) {
			h, a, b, category := setup(t)
			month := convMonth()
			a.setBudgetedAmount(category, month, 100)
			b.setBudgetedAmount(category, month, 200) // newer: B's clock is ahead
			syncInOrder(t, h, order.aFirst, a, b)

			finishScenario(t, h, a, b)
			r := serverRow(t, h, "budget_entries", db.BudgetEntryID(category, month))
			if rowDeleted(r) || rowInt(r, "budgeted") != 200 {
				t.Fatalf("expected the newer 200 to win, got %v", r)
			}
		})
	}
}
