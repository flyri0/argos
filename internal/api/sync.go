package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"argos/internal/db"
	"argos/internal/sync"
)

// maxClockSkew is the bound (§2.3) beyond which an incoming HLC's physical
// component is rejected rather than trusted: a device this far ahead of the
// server's own clock is treated as badly drifted rather than silently
// allowed to win every future conflict.
const maxClockSkew = 5 * time.Minute

// errRejectedStale signals that an existing row's HLC was greater than or
// equal to the incoming mutation's (§2.3) — expected conflict-resolution
// behavior, not an error, so it is reported as "rejected_stale" rather than
// via the "rejected_invalid" + error-object shape (§2.4).
var errRejectedStale = errors.New("existing row has a greater or equal HLC")

// clockSkewError signals an incoming HLC whose physical component is more
// than maxClockSkew ahead of the server's own clock — reported as
// "rejected_invalid" with error code CLOCK_SKEW_TOO_LARGE (§7.2) rather
// than the generic SYNC_MUTATION_INVALID.
type clockSkewError struct{ msg string }

func (e *clockSkewError) Error() string { return e.msg }

// SyncHandler implements POST /sync (§2.4), backed by internal/db.
type SyncHandler struct {
	DB *sql.DB
}

// RegisterSyncRoutes registers POST /sync.
func RegisterSyncRoutes(mux *http.ServeMux, conn *sql.DB) {
	h := &SyncHandler{DB: conn}
	mux.HandleFunc("POST /sync", h.Handle)
}

// syncRequest is the exact request shape from §2.4.
type syncRequest struct {
	Since     int64          `json:"since"`
	Mutations []syncMutation `json:"mutations"`
}

type syncMutation struct {
	Table   string          `json:"table"`
	Op      string          `json:"op"`
	GroupID *string         `json:"group_id"`
	Row     json.RawMessage `json:"row"`
}

// syncResponse is the exact response shape from §2.4. SyncID and
// SchemaVersion are placeholder/zero values in this milestone — wiring them
// to server_meta is Milestone 13.
type syncResponse struct {
	ServerVersion int64        `json:"server_version"`
	SyncID        string       `json:"sync_id"`
	SchemaVersion int64        `json:"schema_version"`
	Results       []syncResult `json:"results"`
	Changes       []syncChange `json:"changes"`
}

type syncResult struct {
	Table  string        `json:"table"`
	ID     string        `json:"id"`
	Status string        `json:"status"`
	Error  *apiErrorBody `json:"error,omitempty"`
}

type syncChange struct {
	Table string `json:"table"`
	Row   any    `json:"row"`
}

// syncUnit is one atomically-applied batch of mutations: a single
// standalone mutation, or every mutation sharing one non-null group_id
// (§2.3), applied together in one transaction. table/id identify the
// group's first member, since results report grouped mutations "once under
// the group's first member" (§2.4).
type syncUnit struct {
	table     string
	id        string
	mutations []syncMutation
}

func (h *SyncHandler) Handle(w http.ResponseWriter, r *http.Request) {
	var req syncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "request body is not valid JSON")
		return
	}

	units := groupMutations(req.Mutations)
	now := time.Now()

	results := make([]syncResult, 0, len(units))
	for _, u := range units {
		results = append(results, h.applyUnit(r.Context(), u, now))
	}

	serverVersion, err := db.CurrentServerVersion(r.Context(), h.DB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	changes, err := h.changesSince(r.Context(), req.Since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, syncResponse{
		ServerVersion: serverVersion,
		SyncID:        "",
		SchemaVersion: 0,
		Results:       results,
		Changes:       changes,
	})
}

// groupMutations splits a flat mutation list into atomic units (§2.3): a
// mutation with a null group_id is its own unit, in place; mutations
// sharing a non-null group_id are collected into one unit positioned at
// that group_id's first occurrence.
func groupMutations(mutations []syncMutation) []syncUnit {
	units := make([]syncUnit, 0, len(mutations))
	unitByGroup := make(map[string]int)

	for _, m := range mutations {
		if m.GroupID == nil {
			units = append(units, syncUnit{table: m.Table, id: rowID(m.Row), mutations: []syncMutation{m}})
			continue
		}
		if idx, ok := unitByGroup[*m.GroupID]; ok {
			units[idx].mutations = append(units[idx].mutations, m)
			continue
		}
		unitByGroup[*m.GroupID] = len(units)
		units = append(units, syncUnit{table: m.Table, id: rowID(m.Row), mutations: []syncMutation{m}})
	}

	return units
}

// rowID best-effort extracts "id" from a mutation's row for error reporting
// even when the row otherwise fails to validate.
func rowID(row json.RawMessage) string {
	var probe struct {
		ID string `json:"id"`
	}
	json.Unmarshal(row, &probe)
	return probe.ID
}

// applyUnit applies every mutation in u within one transaction: all succeed
// together, or the whole unit is reported as a single "rejected_invalid"
// result and none of its mutations take effect (§2.3).
func (h *SyncHandler) applyUnit(ctx context.Context, u syncUnit, now time.Time) syncResult {
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return syncResult{Table: u.table, ID: u.id, Status: "rejected_invalid", Error: &apiErrorBody{Code: "INTERNAL_ERROR", Message: err.Error()}}
	}
	defer tx.Rollback()

	for _, m := range u.mutations {
		err := applyMutation(ctx, tx, m, now)
		switch {
		case err == nil:
			continue
		case errors.Is(err, errRejectedStale):
			return syncResult{Table: u.table, ID: u.id, Status: "rejected_stale"}
		default:
			var skew *clockSkewError
			if errors.As(err, &skew) {
				return syncResult{Table: u.table, ID: u.id, Status: "rejected_invalid", Error: &apiErrorBody{Code: "CLOCK_SKEW_TOO_LARGE", Message: skew.Error()}}
			}
			return syncResult{Table: u.table, ID: u.id, Status: "rejected_invalid", Error: &apiErrorBody{Code: "SYNC_MUTATION_INVALID", Message: err.Error()}}
		}
	}

	if err := tx.Commit(); err != nil {
		return syncResult{Table: u.table, ID: u.id, Status: "rejected_invalid", Error: &apiErrorBody{Code: "SYNC_MUTATION_INVALID", Message: err.Error()}}
	}

	return syncResult{Table: u.table, ID: u.id, Status: "applied"}
}

// applyMutation validates and applies one mutation's row within tx. A
// returned error is one of: errRejectedStale (§2.3, becomes
// "rejected_stale"), *clockSkewError (becomes "rejected_invalid" +
// CLOCK_SKEW_TOO_LARGE), or any other error meaning the row was
// structurally invalid or referenced a row that doesn't exist (becomes
// "rejected_invalid" + SYNC_MUTATION_INVALID) — never a raw SQL error
// leaking out as a 500.
func applyMutation(ctx context.Context, tx *sql.Tx, m syncMutation, now time.Time) error {
	if !db.IsSyncTable(m.Table) {
		return errors.New("unknown table: " + m.Table)
	}

	if err := checkIncomingHLC(ctx, tx, m, now); err != nil {
		return err
	}

	switch m.Op {
	case "upsert":
		return applyUpsert(ctx, tx, m.Table, m.Row)
	case "delete":
		return applyDelete(ctx, tx, m.Table, m.Row)
	default:
		return errors.New("unknown op: " + m.Op)
	}
}

// checkIncomingHLC applies the two HLC-based safeguards (§2.3) that must run
// before a mutation's row is otherwise validated or applied: reject an
// incoming HLC whose physical component is too far ahead of the server's own
// clock, and reject (as stale, not invalid) a mutation whose HLC does not
// order strictly after the existing row's. A row that fails to parse its id
// or HLC fields here is left for the table-specific apply*/validate step
// below to report with its usual, more specific error message.
func checkIncomingHLC(ctx context.Context, tx *sql.Tx, m syncMutation, now time.Time) error {
	var probe struct {
		ID          string `json:"id"`
		HLCPhysical *int64 `json:"hlc_physical"`
		HLCCounter  *int64 `json:"hlc_counter"`
		HLCNodeID   string `json:"hlc_node_id"`
	}
	if err := json.Unmarshal(m.Row, &probe); err != nil || probe.HLCPhysical == nil || probe.HLCCounter == nil || probe.HLCNodeID == "" {
		return nil
	}

	if time.UnixMilli(*probe.HLCPhysical).After(now.Add(maxClockSkew)) {
		return &clockSkewError{msg: "hlc_physical is more than 5 minutes ahead of the server's clock"}
	}

	if !uuidPattern.MatchString(probe.ID) {
		return nil
	}

	existing, found, err := db.GetRowHLCTx(ctx, tx, m.Table, probe.ID)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}

	incoming := sync.HLC{Physical: *probe.HLCPhysical, Counter: *probe.HLCCounter, NodeID: probe.HLCNodeID}
	existingHLC := sync.HLC{Physical: existing.Physical, Counter: existing.Counter, NodeID: existing.NodeID}
	if sync.Compare(existingHLC, incoming) >= 0 {
		return errRejectedStale
	}
	return nil
}

func applyDelete(ctx context.Context, tx *sql.Tx, table string, row json.RawMessage) error {
	var req struct {
		ID          string `json:"id"`
		DeletedAt   *int64 `json:"deleted_at"`
		HLCPhysical *int64 `json:"hlc_physical"`
		HLCCounter  *int64 `json:"hlc_counter"`
		HLCNodeID   string `json:"hlc_node_id"`
	}
	if err := json.Unmarshal(row, &req); err != nil {
		return errors.New("row is not a valid object")
	}
	if !uuidPattern.MatchString(req.ID) {
		return errors.New("id must be a uuid")
	}
	if req.DeletedAt == nil || req.HLCPhysical == nil || req.HLCCounter == nil || !uuidPattern.MatchString(req.HLCNodeID) {
		return errors.New("deleted_at, hlc_physical, hlc_counter, and hlc_node_id are required")
	}

	err := db.SyncDeleteRow(ctx, tx, table, db.SyncDelete{
		ID:          req.ID,
		DeletedAt:   *req.DeletedAt,
		HLCPhysical: *req.HLCPhysical,
		HLCCounter:  *req.HLCCounter,
		HLCNodeID:   req.HLCNodeID,
	})
	if errors.Is(err, db.ErrNotFound) {
		return errors.New("no " + table + " row with this id")
	}
	return err
}

func applyUpsert(ctx context.Context, tx *sql.Tx, table string, row json.RawMessage) error {
	switch table {
	case "accounts":
		return applyUpsertAccount(ctx, tx, row)
	case "category_groups":
		return applyUpsertCategoryGroup(ctx, tx, row)
	case "categories":
		return applyUpsertCategory(ctx, tx, row)
	case "payees":
		return applyUpsertPayee(ctx, tx, row)
	case "transactions":
		return applyUpsertTransaction(ctx, tx, row)
	case "budget_entries":
		return applyUpsertBudgetEntry(ctx, tx, row)
	default:
		return errors.New("unknown table: " + table)
	}
}

func requireHLC(hlcPhysical, hlcCounter *int64, hlcNodeID string) error {
	if hlcPhysical == nil || hlcCounter == nil || !uuidPattern.MatchString(hlcNodeID) {
		return errors.New("hlc_physical, hlc_counter, and hlc_node_id are required")
	}
	return nil
}

func applyUpsertAccount(ctx context.Context, tx *sql.Tx, row json.RawMessage) error {
	var req struct {
		ID          string  `json:"id"`
		Name        string  `json:"name"`
		Type        string  `json:"type"`
		OnBudget    *bool   `json:"on_budget"`
		Closed      *bool   `json:"closed"`
		Currency    string  `json:"currency"`
		Notes       *string `json:"notes"`
		HLCPhysical *int64  `json:"hlc_physical"`
		HLCCounter  *int64  `json:"hlc_counter"`
		HLCNodeID   string  `json:"hlc_node_id"`
	}
	if err := json.Unmarshal(row, &req); err != nil {
		return errors.New("row is not a valid accounts object")
	}
	if !uuidPattern.MatchString(req.ID) {
		return errors.New("id must be a uuid")
	}
	if !accountTypes[req.Type] {
		return errors.New("type must be one of checking, savings, credit, cash, investment, other")
	}
	if req.OnBudget == nil || req.Closed == nil {
		return errors.New("on_budget and closed are required")
	}
	if !currencyPattern.MatchString(req.Currency) {
		return errors.New("currency must be a 3-letter ISO 4217 code")
	}
	if err := requireHLC(req.HLCPhysical, req.HLCCounter, req.HLCNodeID); err != nil {
		return err
	}

	in := db.SyncAccount{
		ID: req.ID, Name: req.Name, Type: req.Type, OnBudget: *req.OnBudget, Closed: *req.Closed,
		Currency: req.Currency, HLCPhysical: *req.HLCPhysical, HLCCounter: *req.HLCCounter, HLCNodeID: req.HLCNodeID,
	}
	if req.Notes != nil {
		in.Notes = sql.NullString{String: *req.Notes, Valid: true}
	}
	return db.SyncUpsertAccount(ctx, tx, in)
}

func applyUpsertCategoryGroup(ctx context.Context, tx *sql.Tx, row json.RawMessage) error {
	var req struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		IsIncome    *bool  `json:"is_income"`
		SortOrder   *int   `json:"sort_order"`
		HLCPhysical *int64 `json:"hlc_physical"`
		HLCCounter  *int64 `json:"hlc_counter"`
		HLCNodeID   string `json:"hlc_node_id"`
	}
	if err := json.Unmarshal(row, &req); err != nil {
		return errors.New("row is not a valid category_groups object")
	}
	if !uuidPattern.MatchString(req.ID) {
		return errors.New("id must be a uuid")
	}
	if req.IsIncome == nil || req.SortOrder == nil {
		return errors.New("is_income and sort_order are required")
	}
	if err := requireHLC(req.HLCPhysical, req.HLCCounter, req.HLCNodeID); err != nil {
		return err
	}

	return db.SyncUpsertCategoryGroup(ctx, tx, db.SyncCategoryGroup{
		ID: req.ID, Name: req.Name, IsIncome: *req.IsIncome, SortOrder: *req.SortOrder,
		HLCPhysical: *req.HLCPhysical, HLCCounter: *req.HLCCounter, HLCNodeID: req.HLCNodeID,
	})
}

func applyUpsertCategory(ctx context.Context, tx *sql.Tx, row json.RawMessage) error {
	var req struct {
		ID          string  `json:"id"`
		GroupID     string  `json:"group_id"`
		Name        string  `json:"name"`
		Hidden      *bool   `json:"hidden"`
		SortOrder   *int    `json:"sort_order"`
		Notes       *string `json:"notes"`
		HLCPhysical *int64  `json:"hlc_physical"`
		HLCCounter  *int64  `json:"hlc_counter"`
		HLCNodeID   string  `json:"hlc_node_id"`
	}
	if err := json.Unmarshal(row, &req); err != nil {
		return errors.New("row is not a valid categories object")
	}
	if !uuidPattern.MatchString(req.ID) {
		return errors.New("id must be a uuid")
	}
	if !uuidPattern.MatchString(req.GroupID) {
		return errors.New("group_id must be a uuid")
	}
	if req.Hidden == nil || req.SortOrder == nil {
		return errors.New("hidden and sort_order are required")
	}
	if err := requireHLC(req.HLCPhysical, req.HLCCounter, req.HLCNodeID); err != nil {
		return err
	}

	in := db.SyncCategory{
		ID: req.ID, GroupID: req.GroupID, Name: req.Name, Hidden: *req.Hidden, SortOrder: *req.SortOrder,
		HLCPhysical: *req.HLCPhysical, HLCCounter: *req.HLCCounter, HLCNodeID: req.HLCNodeID,
	}
	if req.Notes != nil {
		in.Notes = sql.NullString{String: *req.Notes, Valid: true}
	}

	err := db.SyncUpsertCategory(ctx, tx, in)
	if errors.Is(err, db.ErrCategoryGroupNotFound) {
		return errors.New("group_id does not reference an existing category group")
	}
	return err
}

func applyUpsertPayee(ctx context.Context, tx *sql.Tx, row json.RawMessage) error {
	var req struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		HLCPhysical *int64 `json:"hlc_physical"`
		HLCCounter  *int64 `json:"hlc_counter"`
		HLCNodeID   string `json:"hlc_node_id"`
	}
	if err := json.Unmarshal(row, &req); err != nil {
		return errors.New("row is not a valid payees object")
	}
	if !uuidPattern.MatchString(req.ID) {
		return errors.New("id must be a uuid")
	}
	if err := requireHLC(req.HLCPhysical, req.HLCCounter, req.HLCNodeID); err != nil {
		return err
	}

	return db.SyncUpsertPayee(ctx, tx, db.SyncPayee{
		ID: req.ID, Name: req.Name, HLCPhysical: *req.HLCPhysical, HLCCounter: *req.HLCCounter, HLCNodeID: req.HLCNodeID,
	})
}

func applyUpsertTransaction(ctx context.Context, tx *sql.Tx, row json.RawMessage) error {
	var req struct {
		ID          string  `json:"id"`
		AccountID   string  `json:"account_id"`
		CategoryID  *string `json:"category_id"`
		PayeeID     *string `json:"payee_id"`
		ParentID    *string `json:"parent_id"`
		Date        string  `json:"date"`
		Amount      *int64  `json:"amount"`
		Cleared     *bool   `json:"cleared"`
		Notes       string  `json:"notes"`
		TransferID  *string `json:"transfer_id"`
		HLCPhysical *int64  `json:"hlc_physical"`
		HLCCounter  *int64  `json:"hlc_counter"`
		HLCNodeID   string  `json:"hlc_node_id"`
	}
	if err := json.Unmarshal(row, &req); err != nil {
		return errors.New("row is not a valid transactions object")
	}
	if !uuidPattern.MatchString(req.ID) {
		return errors.New("id must be a uuid")
	}
	if !uuidPattern.MatchString(req.AccountID) {
		return errors.New("account_id must be a uuid")
	}
	if !datePattern.MatchString(req.Date) {
		return errors.New("date must be in YYYY-MM-DD format")
	}
	if req.Amount == nil || req.Cleared == nil {
		return errors.New("amount and cleared are required")
	}
	if req.CategoryID != nil && !uuidPattern.MatchString(*req.CategoryID) {
		return errors.New("category_id must be a uuid")
	}
	if req.PayeeID != nil && !uuidPattern.MatchString(*req.PayeeID) {
		return errors.New("payee_id must be a uuid")
	}
	if req.ParentID != nil && !uuidPattern.MatchString(*req.ParentID) {
		return errors.New("parent_id must be a uuid")
	}
	if err := requireHLC(req.HLCPhysical, req.HLCCounter, req.HLCNodeID); err != nil {
		return err
	}

	in := db.SyncTransaction{
		ID: req.ID, AccountID: req.AccountID, Date: req.Date, Amount: *req.Amount, Cleared: *req.Cleared, Notes: req.Notes,
		HLCPhysical: *req.HLCPhysical, HLCCounter: *req.HLCCounter, HLCNodeID: req.HLCNodeID,
	}
	if req.CategoryID != nil {
		in.CategoryID = sql.NullString{String: *req.CategoryID, Valid: true}
	}
	if req.PayeeID != nil {
		in.PayeeID = sql.NullString{String: *req.PayeeID, Valid: true}
	}
	if req.ParentID != nil {
		in.ParentID = sql.NullString{String: *req.ParentID, Valid: true}
	}
	if req.TransferID != nil {
		in.TransferID = sql.NullString{String: *req.TransferID, Valid: true}
	}

	err := db.SyncUpsertTransaction(ctx, tx, in)
	switch {
	case errors.Is(err, db.ErrAccountNotFound):
		return errors.New("account_id does not reference an existing account")
	case errors.Is(err, db.ErrCategoryNotFound):
		return errors.New("category_id does not reference an existing category")
	case errors.Is(err, db.ErrPayeeNotFound):
		return errors.New("payee_id does not reference an existing payee")
	case errors.Is(err, db.ErrParentTransactionNotFound):
		return errors.New("parent_id does not reference an existing transaction")
	default:
		return err
	}
}

func applyUpsertBudgetEntry(ctx context.Context, tx *sql.Tx, row json.RawMessage) error {
	var req struct {
		ID          string `json:"id"`
		CategoryID  string `json:"category_id"`
		Month       string `json:"month"`
		Budgeted    *int64 `json:"budgeted"`
		HLCPhysical *int64 `json:"hlc_physical"`
		HLCCounter  *int64 `json:"hlc_counter"`
		HLCNodeID   string `json:"hlc_node_id"`
	}
	if err := json.Unmarshal(row, &req); err != nil {
		return errors.New("row is not a valid budget_entries object")
	}
	if !uuidPattern.MatchString(req.ID) {
		return errors.New("id must be a uuid")
	}
	if !uuidPattern.MatchString(req.CategoryID) {
		return errors.New("category_id must be a uuid")
	}
	if !monthPattern.MatchString(req.Month) {
		return errors.New("month must be in YYYY-MM format")
	}
	if req.Budgeted == nil {
		return errors.New("budgeted is required")
	}
	if err := requireHLC(req.HLCPhysical, req.HLCCounter, req.HLCNodeID); err != nil {
		return err
	}

	err := db.SyncUpsertBudgetEntry(ctx, tx, db.SyncBudgetEntry{
		ID: req.ID, CategoryID: req.CategoryID, Month: req.Month, Budgeted: *req.Budgeted,
		HLCPhysical: *req.HLCPhysical, HLCCounter: *req.HLCCounter, HLCNodeID: req.HLCNodeID,
	})
	switch {
	case errors.Is(err, db.ErrCategoryNotFound):
		return errors.New("category_id does not reference an existing category")
	case errors.Is(err, db.ErrBudgetEntryConflict):
		return errors.New("a budget entry for this category and month already exists under a different id")
	default:
		return err
	}
}

// --- changes (§2.4 pull half) ---

type syncRowAccount struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Type          string  `json:"type"`
	OnBudget      bool    `json:"on_budget"`
	Closed        bool    `json:"closed"`
	Currency      string  `json:"currency"`
	Notes         *string `json:"notes"`
	HLCPhysical   int64   `json:"hlc_physical"`
	HLCCounter    int64   `json:"hlc_counter"`
	HLCNodeID     string  `json:"hlc_node_id"`
	ServerVersion int64   `json:"server_version"`
	DeletedAt     *int64  `json:"deleted_at"`
}

type syncRowCategoryGroup struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	IsIncome      bool   `json:"is_income"`
	SortOrder     int    `json:"sort_order"`
	HLCPhysical   int64  `json:"hlc_physical"`
	HLCCounter    int64  `json:"hlc_counter"`
	HLCNodeID     string `json:"hlc_node_id"`
	ServerVersion int64  `json:"server_version"`
	DeletedAt     *int64 `json:"deleted_at"`
}

type syncRowCategory struct {
	ID            string  `json:"id"`
	GroupID       string  `json:"group_id"`
	Name          string  `json:"name"`
	Hidden        bool    `json:"hidden"`
	SortOrder     int     `json:"sort_order"`
	Notes         *string `json:"notes"`
	HLCPhysical   int64   `json:"hlc_physical"`
	HLCCounter    int64   `json:"hlc_counter"`
	HLCNodeID     string  `json:"hlc_node_id"`
	ServerVersion int64   `json:"server_version"`
	DeletedAt     *int64  `json:"deleted_at"`
}

type syncRowPayee struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	HLCPhysical   int64  `json:"hlc_physical"`
	HLCCounter    int64  `json:"hlc_counter"`
	HLCNodeID     string `json:"hlc_node_id"`
	ServerVersion int64  `json:"server_version"`
	DeletedAt     *int64 `json:"deleted_at"`
}

type syncRowTransaction struct {
	ID            string  `json:"id"`
	AccountID     string  `json:"account_id"`
	CategoryID    *string `json:"category_id"`
	PayeeID       *string `json:"payee_id"`
	ParentID      *string `json:"parent_id"`
	Date          string  `json:"date"`
	Amount        int64   `json:"amount"`
	Cleared       bool    `json:"cleared"`
	Notes         string  `json:"notes"`
	TransferID    *string `json:"transfer_id"`
	HLCPhysical   int64   `json:"hlc_physical"`
	HLCCounter    int64   `json:"hlc_counter"`
	HLCNodeID     string  `json:"hlc_node_id"`
	ServerVersion int64   `json:"server_version"`
	DeletedAt     *int64  `json:"deleted_at"`
}

type syncRowBudgetEntry struct {
	ID            string `json:"id"`
	CategoryID    string `json:"category_id"`
	Month         string `json:"month"`
	Budgeted      int64  `json:"budgeted"`
	HLCPhysical   int64  `json:"hlc_physical"`
	HLCCounter    int64  `json:"hlc_counter"`
	HLCNodeID     string `json:"hlc_node_id"`
	ServerVersion int64  `json:"server_version"`
	DeletedAt     *int64 `json:"deleted_at"`
}

func nullIntPtr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	return &n.Int64
}

func nullStringPtr(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	return &s.String
}

// changesSince assembles the "changes" half of the /sync response (§2.4):
// every row, across all six syncable tables, with server_version greater
// than since.
func (h *SyncHandler) changesSince(ctx context.Context, since int64) ([]syncChange, error) {
	changes := make([]syncChange, 0)

	accounts, err := db.ListAccountsSince(ctx, h.DB, since)
	if err != nil {
		return nil, err
	}
	for _, a := range accounts {
		changes = append(changes, syncChange{Table: "accounts", Row: syncRowAccount{
			ID: a.ID, Name: a.Name, Type: a.Type, OnBudget: a.OnBudget, Closed: a.Closed, Currency: a.Currency,
			Notes: nullStringPtr(a.Notes), HLCPhysical: a.HLCPhysical, HLCCounter: a.HLCCounter, HLCNodeID: a.HLCNodeID,
			ServerVersion: a.ServerVersion, DeletedAt: nullIntPtr(a.DeletedAt),
		}})
	}

	groups, err := db.ListCategoryGroupsSince(ctx, h.DB, since)
	if err != nil {
		return nil, err
	}
	for _, g := range groups {
		changes = append(changes, syncChange{Table: "category_groups", Row: syncRowCategoryGroup{
			ID: g.ID, Name: g.Name, IsIncome: g.IsIncome, SortOrder: g.SortOrder,
			HLCPhysical: g.HLCPhysical, HLCCounter: g.HLCCounter, HLCNodeID: g.HLCNodeID,
			ServerVersion: g.ServerVersion, DeletedAt: nullIntPtr(g.DeletedAt),
		}})
	}

	categories, err := db.ListCategoriesSince(ctx, h.DB, since)
	if err != nil {
		return nil, err
	}
	for _, c := range categories {
		changes = append(changes, syncChange{Table: "categories", Row: syncRowCategory{
			ID: c.ID, GroupID: c.GroupID, Name: c.Name, Hidden: c.Hidden, SortOrder: c.SortOrder,
			Notes: nullStringPtr(c.Notes), HLCPhysical: c.HLCPhysical, HLCCounter: c.HLCCounter, HLCNodeID: c.HLCNodeID,
			ServerVersion: c.ServerVersion, DeletedAt: nullIntPtr(c.DeletedAt),
		}})
	}

	payees, err := db.ListPayeesSince(ctx, h.DB, since)
	if err != nil {
		return nil, err
	}
	for _, p := range payees {
		changes = append(changes, syncChange{Table: "payees", Row: syncRowPayee{
			ID: p.ID, Name: p.Name, HLCPhysical: p.HLCPhysical, HLCCounter: p.HLCCounter, HLCNodeID: p.HLCNodeID,
			ServerVersion: p.ServerVersion, DeletedAt: nullIntPtr(p.DeletedAt),
		}})
	}

	transactions, err := db.ListTransactionsSince(ctx, h.DB, since)
	if err != nil {
		return nil, err
	}
	for _, t := range transactions {
		changes = append(changes, syncChange{Table: "transactions", Row: syncRowTransaction{
			ID: t.ID, AccountID: t.AccountID, CategoryID: nullStringPtr(t.CategoryID), PayeeID: nullStringPtr(t.PayeeID),
			ParentID: nullStringPtr(t.ParentID), Date: t.Date, Amount: t.Amount, Cleared: t.Cleared, Notes: t.Notes,
			TransferID: nullStringPtr(t.TransferID), HLCPhysical: t.HLCPhysical, HLCCounter: t.HLCCounter, HLCNodeID: t.HLCNodeID,
			ServerVersion: t.ServerVersion, DeletedAt: nullIntPtr(t.DeletedAt),
		}})
	}

	entries, err := db.ListBudgetEntriesSince(ctx, h.DB, since)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		changes = append(changes, syncChange{Table: "budget_entries", Row: syncRowBudgetEntry{
			ID: e.ID, CategoryID: e.CategoryID, Month: e.Month, Budgeted: e.Budgeted,
			HLCPhysical: e.HLCPhysical, HLCCounter: e.HLCCounter, HLCNodeID: e.HLCNodeID,
			ServerVersion: e.ServerVersion, DeletedAt: nullIntPtr(e.DeletedAt),
		}})
	}

	return changes, nil
}
