package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
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

// errRejectedStale signals that an existing row's HLC was strictly greater
// than the incoming mutation's (§2.3) — expected conflict-resolution
// behavior, not an error, so it is reported as "rejected_stale" rather than
// via the "rejected_invalid" + error-object shape (§2.4).
var errRejectedStale = errors.New("existing row has a greater HLC")

// errAlreadyApplied signals an incoming HLC exactly equal to the stored
// row's: the same write arriving again (e.g. a retry after a lost response).
// applyUnit skips such a member instead of aborting the unit, so a retried
// operation merged with a newer one in the same unit can't discard the newer
// edit (§2.4).
var errAlreadyApplied = errors.New("existing row already has this HLC")

// clockSkewError signals an incoming HLC whose physical component is more
// than maxClockSkew ahead of the server's own clock — reported as
// "rejected_invalid" with error code CLOCK_SKEW_TOO_LARGE (§7.2) rather
// than the generic SYNC_MUTATION_INVALID.
type clockSkewError struct{ msg string }

func (e *clockSkewError) Error() string { return e.msg }

// SyncHandler implements POST /sync (§2.4), backed by internal/db. Logger
// is optional — tests constructing this directly often leave it nil, so
// every use goes through the logger() accessor below rather than the
// field itself.
type SyncHandler struct {
	DB     *sql.DB
	Logger *slog.Logger
}

func (h *SyncHandler) logger() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}

// RegisterSyncRoutes registers POST /sync. logger must not be nil.
func RegisterSyncRoutes(mux *http.ServeMux, conn *sql.DB, logger *slog.Logger) {
	h := &SyncHandler{DB: conn, Logger: logger}
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

// syncResponse is the exact response shape from §2.4.
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
	var staleRows []syncRowRef
	for _, u := range units {
		result, rows := h.applyUnit(r.Context(), u, now)
		results = append(results, result)
		if result.Status == "rejected_stale" {
			staleRows = append(staleRows, rows...)
		}
	}

	serverVersion, err := db.CurrentServerVersion(r.Context(), h.DB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	meta, err := db.GetOrInitServerMeta(r.Context(), h.DB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	changes, err := h.changesSince(r.Context(), req.Since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	changes, err = h.appendStaleRows(r.Context(), changes, staleRows)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	h.logPush(req.Since, results, changes, serverVersion)

	writeJSON(w, http.StatusOK, syncResponse{
		ServerVersion: serverVersion,
		SyncID:        meta.SyncID,
		SchemaVersion: meta.SchemaVersion,
		Results:       results,
		Changes:       changes,
	})
}

// logPush summarizes one /sync round-trip: sync is the most complex, most
// error-prone part of the backend (HLC conflict resolution, atomic groups,
// per-mutation independence, §2.3/§2.4), so this is deliberately more
// detailed than the generic per-request line withRequestLogging already
// produces — a bare "200 OK" doesn't say whether every mutation actually
// applied or whether some silently lost a conflict or got rejected as
// invalid. Each rejected_invalid mutation also gets its own line at Warn,
// since that status means either a client bug or a real data problem
// worth a human noticing, not routine conflict resolution.
func (h *SyncHandler) logPush(since int64, results []syncResult, changes []syncChange, serverVersion int64) {
	var applied, stale, invalid int
	for _, res := range results {
		switch res.Status {
		case "applied":
			applied++
		case "rejected_stale":
			stale++
		case "rejected_invalid":
			invalid++
		}
	}

	h.logger().Info("sync push",
		"since", since,
		"mutations", len(results),
		"applied", applied,
		"rejected_stale", stale,
		"rejected_invalid", invalid,
		"changes_returned", len(changes),
		"server_version", serverVersion,
	)

	for _, res := range results {
		if res.Status != "rejected_invalid" || res.Error == nil {
			continue
		}
		h.logger().Warn("sync mutation rejected",
			"table", res.Table, "id", res.ID,
			"error_code", res.Error.Code, "error_message", res.Error.Message,
		)
	}
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

// syncRowRef identifies one row a mutation referenced.
type syncRowRef struct {
	table string
	id    string
}

// applyUnit applies u and also returns the row every one of its mutations
// referenced, so a rejected_stale unit's rows can be sent back in full (§2.4).
func (h *SyncHandler) applyUnit(ctx context.Context, u syncUnit, now time.Time) (syncResult, []syncRowRef) {
	rows := make([]syncRowRef, 0, len(u.mutations))
	for _, m := range u.mutations {
		rows = append(rows, syncRowRef{table: m.Table, id: rowID(m.Row)})
	}
	return h.applyUnitResult(ctx, u, now), rows
}

// applyUnitResult applies every mutation in u within one transaction: all
// succeed together, or the whole unit is reported as a single rejected result
// and none of its mutations take effect (§2.3). Members already applied
// (equal HLC) are skipped; a unit where every member was skipped is
// rejected_stale (§2.4).
func (h *SyncHandler) applyUnitResult(ctx context.Context, u syncUnit, now time.Time) syncResult {
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return syncResult{Table: u.table, ID: u.id, Status: "rejected_invalid", Error: &apiErrorBody{Code: "INTERNAL_ERROR", Message: err.Error()}}
	}
	defer tx.Rollback()

	skipped := 0
	touchedTransfers := make(map[string]bool)
	for _, m := range u.mutations {
		if m.Table == "transactions" {
			if err := collectTransferIDs(ctx, tx, m, touchedTransfers); err != nil {
				return syncResult{Table: u.table, ID: u.id, Status: "rejected_invalid", Error: &apiErrorBody{Code: "INTERNAL_ERROR", Message: err.Error()}}
			}
		}
		err := applyMutation(ctx, tx, m, now)
		switch {
		case err == nil:
			continue
		case errors.Is(err, errAlreadyApplied):
			skipped++
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

	if skipped == len(u.mutations) {
		return syncResult{Table: u.table, ID: u.id, Status: "rejected_stale"}
	}

	// §2.4: checked against the unit's combined result, since a valid pair
	// write is only balanced once both legs are applied.
	for transferID := range touchedTransfers {
		err := db.CheckTransferPairTx(ctx, tx, transferID)
		switch {
		case errors.Is(err, db.ErrTransferPairInvalid):
			return syncResult{Table: u.table, ID: u.id, Status: "rejected_invalid", Error: &apiErrorBody{
				Code:    "TRANSFER_PAIR_INVALID",
				Message: "transfer " + transferID + " must have two legs in different accounts, both deleted or both live and balanced",
			}}
		case err != nil:
			return syncResult{Table: u.table, ID: u.id, Status: "rejected_invalid", Error: &apiErrorBody{Code: "INTERNAL_ERROR", Message: err.Error()}}
		}
	}

	if err := tx.Commit(); err != nil {
		return syncResult{Table: u.table, ID: u.id, Status: "rejected_invalid", Error: &apiErrorBody{Code: "SYNC_MUTATION_INVALID", Message: err.Error()}}
	}

	return syncResult{Table: u.table, ID: u.id, Status: "applied"}
}

// collectTransferIDs adds every transfer_id a transactions mutation touches
// to into: the row's stored transfer_id (covering deletes and upserts that
// change or clear it) and, for an upsert, the incoming one. A row that fails
// to parse is left for applyMutation to report.
func collectTransferIDs(ctx context.Context, tx *sql.Tx, m syncMutation, into map[string]bool) error {
	var probe struct {
		ID         string  `json:"id"`
		TransferID *string `json:"transfer_id"`
	}
	if err := json.Unmarshal(m.Row, &probe); err != nil {
		return nil
	}
	if uuidPattern.MatchString(probe.ID) {
		stored, err := db.TransferIDOfTx(ctx, tx, probe.ID)
		if err != nil {
			return err
		}
		if stored.Valid {
			into[stored.String] = true
		}
	}
	if m.Op == "upsert" && probe.TransferID != nil && *probe.TransferID != "" {
		into[*probe.TransferID] = true
	}
	return nil
}

// applyMutation validates and applies one mutation's row within tx. A
// returned error is one of: errAlreadyApplied (§2.4, the member is skipped),
// errRejectedStale (§2.3, becomes "rejected_stale"), *clockSkewError (becomes "rejected_invalid" +
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
// clock, and reject (as stale, not invalid) a mutation whose HLC orders
// strictly before the existing row's — or report errAlreadyApplied when it's
// exactly equal. A row that fails to parse its id
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
	switch c := sync.Compare(existingHLC, incoming); {
	case c == 0:
		return errAlreadyApplied
	case c > 0:
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

	upsert := func(id string) error {
		return db.SyncUpsertBudgetEntry(ctx, tx, db.SyncBudgetEntry{
			ID: id, CategoryID: req.CategoryID, Month: req.Month, Budgeted: *req.Budgeted,
			HLCPhysical: *req.HLCPhysical, HLCCounter: *req.HLCCounter, HLCNodeID: req.HLCNodeID,
		})
	}

	err := upsert(req.ID)
	var keyTaken *db.BudgetEntryKeyTakenError
	if errors.As(err, &keyTaken) {
		// §5.2: (category_id, month) is the entry's identity, so another id
		// for the same pair is the same logical row — last-write-wins onto
		// the stored id instead of a permanent rejection.
		existing, found, hlcErr := db.GetRowHLCTx(ctx, tx, "budget_entries", keyTaken.ExistingID)
		if hlcErr != nil {
			return hlcErr
		}
		incoming := sync.HLC{Physical: *req.HLCPhysical, Counter: *req.HLCCounter, NodeID: req.HLCNodeID}
		if found {
			switch c := sync.Compare(sync.HLC{Physical: existing.Physical, Counter: existing.Counter, NodeID: existing.NodeID}, incoming); {
			case c == 0:
				// This write was already merged onto the stored id (§2.4).
				return errAlreadyApplied
			case c > 0:
				return errRejectedStale
			}
		}
		err = upsert(keyTaken.ExistingID)
	}
	switch {
	case errors.Is(err, db.ErrCategoryNotFound):
		return errors.New("category_id does not reference an existing category")
	case errors.Is(err, db.ErrIncomeCategoryNotBudgetable):
		return errors.New("category_id belongs to the income group, which cannot be budgeted")
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

// toSyncChange maps a db row struct (as returned by the List*Since functions
// or db.GetSyncRow) to its §2.4 wire shape.
func toSyncChange(table string, row any) syncChange {
	switch r := row.(type) {
	case db.Account:
		return syncChange{Table: table, Row: syncRowAccount{
			ID: r.ID, Name: r.Name, Type: r.Type, OnBudget: r.OnBudget, Closed: r.Closed, Currency: r.Currency,
			Notes: nullStringPtr(r.Notes), HLCPhysical: r.HLCPhysical, HLCCounter: r.HLCCounter, HLCNodeID: r.HLCNodeID,
			ServerVersion: r.ServerVersion, DeletedAt: nullIntPtr(r.DeletedAt),
		}}
	case db.CategoryGroup:
		return syncChange{Table: table, Row: syncRowCategoryGroup{
			ID: r.ID, Name: r.Name, IsIncome: r.IsIncome, SortOrder: r.SortOrder,
			HLCPhysical: r.HLCPhysical, HLCCounter: r.HLCCounter, HLCNodeID: r.HLCNodeID,
			ServerVersion: r.ServerVersion, DeletedAt: nullIntPtr(r.DeletedAt),
		}}
	case db.Category:
		return syncChange{Table: table, Row: syncRowCategory{
			ID: r.ID, GroupID: r.GroupID, Name: r.Name, Hidden: r.Hidden, SortOrder: r.SortOrder,
			Notes: nullStringPtr(r.Notes), HLCPhysical: r.HLCPhysical, HLCCounter: r.HLCCounter, HLCNodeID: r.HLCNodeID,
			ServerVersion: r.ServerVersion, DeletedAt: nullIntPtr(r.DeletedAt),
		}}
	case db.Payee:
		return syncChange{Table: table, Row: syncRowPayee{
			ID: r.ID, Name: r.Name, HLCPhysical: r.HLCPhysical, HLCCounter: r.HLCCounter, HLCNodeID: r.HLCNodeID,
			ServerVersion: r.ServerVersion, DeletedAt: nullIntPtr(r.DeletedAt),
		}}
	case db.Transaction:
		return syncChange{Table: table, Row: syncRowTransaction{
			ID: r.ID, AccountID: r.AccountID, CategoryID: nullStringPtr(r.CategoryID), PayeeID: nullStringPtr(r.PayeeID),
			ParentID: nullStringPtr(r.ParentID), Date: r.Date, Amount: r.Amount, Cleared: r.Cleared, Notes: r.Notes,
			TransferID: nullStringPtr(r.TransferID), HLCPhysical: r.HLCPhysical, HLCCounter: r.HLCCounter, HLCNodeID: r.HLCNodeID,
			ServerVersion: r.ServerVersion, DeletedAt: nullIntPtr(r.DeletedAt),
		}}
	case db.BudgetEntry:
		return syncChange{Table: table, Row: syncRowBudgetEntry{
			ID: r.ID, CategoryID: r.CategoryID, Month: r.Month, Budgeted: r.Budgeted,
			HLCPhysical: r.HLCPhysical, HLCCounter: r.HLCCounter, HLCNodeID: r.HLCNodeID,
			ServerVersion: r.ServerVersion, DeletedAt: nullIntPtr(r.DeletedAt),
		}}
	default:
		panic("toSyncChange: unsupported row type for table " + table)
	}
}

// syncChangeID returns the id of a change produced by toSyncChange.
func syncChangeID(c syncChange) string {
	switch r := c.Row.(type) {
	case syncRowAccount:
		return r.ID
	case syncRowCategoryGroup:
		return r.ID
	case syncRowCategory:
		return r.ID
	case syncRowPayee:
		return r.ID
	case syncRowTransaction:
		return r.ID
	case syncRowBudgetEntry:
		return r.ID
	default:
		return ""
	}
}

func appendChanges[T any](changes []syncChange, table string, rows []T) []syncChange {
	for _, row := range rows {
		changes = append(changes, toSyncChange(table, row))
	}
	return changes
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
	changes = appendChanges(changes, "accounts", accounts)

	groups, err := db.ListCategoryGroupsSince(ctx, h.DB, since)
	if err != nil {
		return nil, err
	}
	changes = appendChanges(changes, "category_groups", groups)

	categories, err := db.ListCategoriesSince(ctx, h.DB, since)
	if err != nil {
		return nil, err
	}
	changes = appendChanges(changes, "categories", categories)

	payees, err := db.ListPayeesSince(ctx, h.DB, since)
	if err != nil {
		return nil, err
	}
	changes = appendChanges(changes, "payees", payees)

	transactions, err := db.ListTransactionsSince(ctx, h.DB, since)
	if err != nil {
		return nil, err
	}
	changes = appendChanges(changes, "transactions", transactions)

	entries, err := db.ListBudgetEntriesSince(ctx, h.DB, since)
	if err != nil {
		return nil, err
	}
	changes = appendChanges(changes, "budget_entries", entries)

	return changes, nil
}

// appendStaleRows adds the current server row for every row a rejected_stale
// unit referenced (§2.4), even if its server_version is at or below the
// request's since — otherwise the losing device, whose cursor may already be
// past the winning row, would keep its losing local state forever. Rows
// already in changes, and rows that don't exist on the server, are skipped.
func (h *SyncHandler) appendStaleRows(ctx context.Context, changes []syncChange, refs []syncRowRef) ([]syncChange, error) {
	if len(refs) == 0 {
		return changes, nil
	}
	seen := make(map[syncRowRef]bool, len(changes)+len(refs))
	for _, c := range changes {
		seen[syncRowRef{table: c.Table, id: syncChangeID(c)}] = true
	}
	for _, ref := range refs {
		if seen[ref] || ref.id == "" || !db.IsSyncTable(ref.table) {
			continue
		}
		seen[ref] = true
		row, found, err := db.GetSyncRow(ctx, h.DB, ref.table, ref.id)
		if err != nil {
			return nil, err
		}
		if found {
			changes = append(changes, toSyncChange(ref.table, row))
		}
	}
	return changes, nil
}
