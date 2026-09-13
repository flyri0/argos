package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"argos/internal/db"
)

// PayeesHandler implements the payees endpoints (§7.3), backed by internal/db.
type PayeesHandler struct {
	DB *sql.DB
}

// RegisterPayeeRoutes registers GET/POST /api/payees and PATCH /api/payees/{id}.
func RegisterPayeeRoutes(mux *http.ServeMux, conn *sql.DB) {
	h := &PayeesHandler{DB: conn}
	mux.HandleFunc("GET /api/payees", h.List)
	mux.HandleFunc("POST /api/payees", h.Create)
	mux.HandleFunc("PATCH /api/payees/{id}", h.Update)
}

// payee is the public JSON shape for a payees row: its own columns (§5.2)
// plus id, minus sync metadata.
type payee struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func toPayee(p db.Payee) payee {
	return payee{ID: p.ID, Name: p.Name}
}

func (h *PayeesHandler) List(w http.ResponseWriter, r *http.Request) {
	payees, err := db.ListPayees(r.Context(), h.DB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	out := make([]payee, 0, len(payees))
	for _, p := range payees {
		out = append(out, toPayee(p))
	}
	writeJSON(w, http.StatusOK, out)
}

type createPayeeRequest struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	HLCPhysical *int64 `json:"hlc_physical"`
	HLCCounter  *int64 `json:"hlc_counter"`
	HLCNodeID   string `json:"hlc_node_id"`
}

func (h *PayeesHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createPayeeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "request body is not valid JSON")
		return
	}

	if !uuidPattern.MatchString(req.ID) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "id must be a uuid")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "name is required")
		return
	}
	if req.HLCPhysical == nil || req.HLCCounter == nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "hlc_physical and hlc_counter are required")
		return
	}
	if !uuidPattern.MatchString(req.HLCNodeID) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "hlc_node_id must be a uuid")
		return
	}

	in := db.NewPayee{
		ID:          req.ID,
		Name:        req.Name,
		HLCPhysical: *req.HLCPhysical,
		HLCCounter:  *req.HLCCounter,
		HLCNodeID:   req.HLCNodeID,
	}

	created, err := db.CreatePayee(r.Context(), h.DB, in)
	switch {
	case errors.Is(err, db.ErrAlreadyExists):
		writeError(w, http.StatusConflict, "PAYEE_EXISTS", "a payee with this id already exists")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, toPayee(created))
}

func (h *PayeesHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "request body is not valid JSON")
		return
	}

	var hlcPhysical, hlcCounter *int64
	var hlcNodeID string
	if v, ok := raw["hlc_physical"]; ok {
		var n int64
		if json.Unmarshal(v, &n) != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "hlc_physical must be an integer")
			return
		}
		hlcPhysical = &n
	}
	if v, ok := raw["hlc_counter"]; ok {
		var n int64
		if json.Unmarshal(v, &n) != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "hlc_counter must be an integer")
			return
		}
		hlcCounter = &n
	}
	if v, ok := raw["hlc_node_id"]; ok {
		if json.Unmarshal(v, &hlcNodeID) != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "hlc_node_id must be a string")
			return
		}
	}
	if hlcPhysical == nil || hlcCounter == nil || !uuidPattern.MatchString(hlcNodeID) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "hlc_physical, hlc_counter, and hlc_node_id are required")
		return
	}

	isDelete := false
	if v, ok := raw["delete"]; ok {
		if json.Unmarshal(v, &isDelete) != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "delete must be a boolean")
			return
		}
	}

	if isDelete {
		h.delete(w, r, id, raw, *hlcPhysical, *hlcCounter, hlcNodeID)
		return
	}
	h.update(w, r, id, raw, *hlcPhysical, *hlcCounter, hlcNodeID)
}

// update handles the normal-update path: rename (§7.3).
func (h *PayeesHandler) update(w http.ResponseWriter, r *http.Request, id string, raw map[string]json.RawMessage, hlcPhysical, hlcCounter int64, hlcNodeID string) {
	var patch db.PayeeUpdate
	patch.HLCPhysical = hlcPhysical
	patch.HLCCounter = hlcCounter
	patch.HLCNodeID = hlcNodeID

	if v, ok := raw["name"]; ok {
		var s string
		if json.Unmarshal(v, &s) != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "name must be a string")
			return
		}
		patch.Name = &s
	}

	updated, err := db.UpdatePayee(r.Context(), h.DB, id, patch)
	switch {
	case errors.Is(err, db.ErrNotFound):
		writeError(w, http.StatusNotFound, "PAYEE_NOT_FOUND", "no payee with this id")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toPayee(updated))
}

// delete handles the soft-delete path, with reassignment when the payee is
// in use (§5.4).
func (h *PayeesHandler) delete(w http.ResponseWriter, r *http.Request, id string, raw map[string]json.RawMessage, hlcPhysical, hlcCounter int64, hlcNodeID string) {
	var reassignTo *string
	if v, ok := raw["reassign_to"]; ok && string(v) != "null" {
		var s string
		if json.Unmarshal(v, &s) != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "reassign_to must be a string")
			return
		}
		if !uuidPattern.MatchString(s) {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "reassign_to must be a uuid")
			return
		}
		if s == id {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "reassign_to must be a different payee")
			return
		}
		reassignTo = &s
	}

	deleted, err := db.DeletePayee(r.Context(), h.DB, id, reassignTo, hlcPhysical, hlcCounter, hlcNodeID)
	switch {
	case errors.Is(err, db.ErrNotFound):
		writeError(w, http.StatusNotFound, "PAYEE_NOT_FOUND", "no payee with this id")
		return
	case errors.Is(err, db.ErrPayeeInUse):
		writeError(w, http.StatusConflict, "PAYEE_IN_USE_NEEDS_REASSIGN", "payee has transactions; reassign_to is required")
		return
	case errors.Is(err, db.ErrReassignTargetNotFound):
		writeError(w, http.StatusNotFound, "REASSIGN_TARGET_NOT_FOUND", "no payee with the given reassign_to id")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toPayee(deleted))
}
