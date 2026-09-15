package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"argos/internal/db"
)

// CategoriesHandler implements the categories endpoints (§7), backed by internal/db.
type CategoriesHandler struct {
	DB *sql.DB
}

// RegisterCategoryRoutes registers GET/POST /api/categories and
// PATCH /api/categories/{id}.
func RegisterCategoryRoutes(mux *http.ServeMux, conn *sql.DB) {
	h := &CategoriesHandler{DB: conn}
	mux.HandleFunc("GET /api/categories", h.List)
	mux.HandleFunc("POST /api/categories", h.Create)
	mux.HandleFunc("PATCH /api/categories/{id}", h.Update)
}

// category is the public JSON shape for a categories row: its own columns
// (§5.2) plus id, minus sync metadata.
type category struct {
	ID        string  `json:"id"`
	GroupID   string  `json:"group_id"`
	Name      string  `json:"name"`
	Hidden    bool    `json:"hidden"`
	SortOrder int     `json:"sort_order"`
	Notes     *string `json:"notes"`
}

func toCategory(c db.Category) category {
	out := category{
		ID:        c.ID,
		GroupID:   c.GroupID,
		Name:      c.Name,
		Hidden:    c.Hidden,
		SortOrder: c.SortOrder,
		Notes:     nil,
	}
	if c.Notes.Valid {
		out.Notes = &c.Notes.String
	}
	return out
}

// categoryGroup is the public JSON shape for GET /api/categories: a
// category_groups row plus its non-deleted categories nested (§7).
type categoryGroup struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	IsIncome   bool       `json:"is_income"`
	SortOrder  int        `json:"sort_order"`
	Categories []category `json:"categories"`
}

func (h *CategoriesHandler) List(w http.ResponseWriter, r *http.Request) {
	groups, err := db.ListCategoryGroups(r.Context(), h.DB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	categories, err := db.ListCategories(r.Context(), h.DB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	byGroup := make(map[string][]category)
	for _, c := range categories {
		byGroup[c.GroupID] = append(byGroup[c.GroupID], toCategory(c))
	}

	out := make([]categoryGroup, 0, len(groups))
	for _, g := range groups {
		out = append(out, categoryGroup{
			ID:         g.ID,
			Name:       g.Name,
			IsIncome:   g.IsIncome,
			SortOrder:  g.SortOrder,
			Categories: byGroup[g.ID],
		})
	}
	writeJSON(w, http.StatusOK, out)
}

type createCategoryRequest struct {
	ID          string  `json:"id"`
	GroupID     string  `json:"group_id"`
	Name        string  `json:"name"`
	Hidden      bool    `json:"hidden"`
	SortOrder   *int    `json:"sort_order"`
	Notes       *string `json:"notes"`
	HLCPhysical *int64  `json:"hlc_physical"`
	HLCCounter  *int64  `json:"hlc_counter"`
	HLCNodeID   string  `json:"hlc_node_id"`
}

func (h *CategoriesHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createCategoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "request body is not valid JSON")
		return
	}

	if !uuidPattern.MatchString(req.ID) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "id must be a uuid")
		return
	}
	if !uuidPattern.MatchString(req.GroupID) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "group_id must be a uuid")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "name is required")
		return
	}
	if req.SortOrder == nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "sort_order is required")
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

	in := db.NewCategory{
		ID:          req.ID,
		GroupID:     req.GroupID,
		Name:        req.Name,
		Hidden:      req.Hidden,
		SortOrder:   *req.SortOrder,
		HLCPhysical: *req.HLCPhysical,
		HLCCounter:  *req.HLCCounter,
		HLCNodeID:   req.HLCNodeID,
	}
	if req.Notes != nil {
		in.Notes = sql.NullString{String: *req.Notes, Valid: true}
	}

	created, err := db.CreateCategory(r.Context(), h.DB, in)
	switch {
	case errors.Is(err, db.ErrCategoryGroupNotFound):
		writeError(w, http.StatusNotFound, "CATEGORY_GROUP_NOT_FOUND", "no category group with this id")
		return
	case errors.Is(err, db.ErrAlreadyExists):
		writeError(w, http.StatusConflict, "CATEGORY_EXISTS", "a category with this id already exists")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, toCategory(created))
}

func (h *CategoriesHandler) Update(w http.ResponseWriter, r *http.Request) {
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

// update handles the normal-update path: rename, hide/unhide, reorder (§7).
func (h *CategoriesHandler) update(w http.ResponseWriter, r *http.Request, id string, raw map[string]json.RawMessage, hlcPhysical, hlcCounter int64, hlcNodeID string) {
	var patch db.CategoryUpdate
	patch.HLCPhysical = hlcPhysical
	patch.HLCCounter = hlcCounter
	patch.HLCNodeID = hlcNodeID

	for key, value := range raw {
		var errMsg string
		switch key {
		case "name":
			var s string
			if json.Unmarshal(value, &s) != nil {
				errMsg = "name must be a string"
			}
			patch.Name = &s
		case "hidden":
			var b bool
			if json.Unmarshal(value, &b) != nil {
				errMsg = "hidden must be a boolean"
			}
			patch.Hidden = &b
		case "sort_order":
			var n int
			if json.Unmarshal(value, &n) != nil {
				errMsg = "sort_order must be an integer"
			}
			patch.SortOrder = &n
		case "notes":
			if string(value) == "null" {
				patch.Notes = &sql.NullString{}
			} else {
				var s string
				if json.Unmarshal(value, &s) != nil {
					errMsg = "notes must be a string or null"
				}
				patch.Notes = &sql.NullString{String: s, Valid: true}
			}
		}
		if errMsg != "" {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", errMsg)
			return
		}
	}

	updated, err := db.UpdateCategory(r.Context(), h.DB, id, patch)
	switch {
	case errors.Is(err, db.ErrStaleWrite):
		writeError(w, http.StatusConflict, "STALE_WRITE", "a newer write to this category already exists")
		return
	case errors.Is(err, db.ErrNotFound):
		writeError(w, http.StatusNotFound, "CATEGORY_NOT_FOUND", "no category with this id")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toCategory(updated))
}

// delete handles the soft-delete path, with reassignment when the category
// is in use (§5.4).
func (h *CategoriesHandler) delete(w http.ResponseWriter, r *http.Request, id string, raw map[string]json.RawMessage, hlcPhysical, hlcCounter int64, hlcNodeID string) {
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
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "reassign_to must be a different category")
			return
		}
		reassignTo = &s
	}

	deleted, err := db.DeleteCategory(r.Context(), h.DB, id, reassignTo, hlcPhysical, hlcCounter, hlcNodeID)
	switch {
	case errors.Is(err, db.ErrStaleWrite):
		writeError(w, http.StatusConflict, "STALE_WRITE", "a newer write to this category or a row it would move already exists")
		return
	case errors.Is(err, db.ErrNotFound):
		writeError(w, http.StatusNotFound, "CATEGORY_NOT_FOUND", "no category with this id")
		return
	case errors.Is(err, db.ErrCategoryInUse):
		writeError(w, http.StatusConflict, "CATEGORY_IN_USE_NEEDS_REASSIGN", "category has transactions or budgeted amounts; reassign_to is required")
		return
	case errors.Is(err, db.ErrReassignTargetNotFound):
		writeError(w, http.StatusNotFound, "REASSIGN_TARGET_NOT_FOUND", "no category with the given reassign_to id")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toCategory(deleted))
}
