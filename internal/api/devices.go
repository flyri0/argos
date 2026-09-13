package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"argos/internal/db"
)

// DevicesHandler implements the device management endpoints (§6.2, §7.3),
// backed by internal/db. devices is server-side auth metadata, not synced
// to clients (§5.2), so unlike PayeesHandler/TransactionsHandler these
// mutations carry no HLC fields.
type DevicesHandler struct {
	DB *sql.DB
}

// RegisterDeviceRoutes registers GET /api/devices, PATCH /api/devices/{id},
// and DELETE /api/devices/{id}.
func RegisterDeviceRoutes(mux *http.ServeMux, conn *sql.DB) {
	h := &DevicesHandler{DB: conn}
	mux.HandleFunc("GET /api/devices", h.List)
	mux.HandleFunc("PATCH /api/devices/{id}", h.Update)
	mux.HandleFunc("DELETE /api/devices/{id}", h.Delete)
}

// device is the public JSON shape for a devices row: its own columns
// (§5.2), minus token_hash, which must never leave the server.
type device struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ApprovedAt int64  `json:"approved_at"`
	LastSeenAt *int64 `json:"last_seen_at"`
	RevokedAt  *int64 `json:"revoked_at"`
}

func toDevice(d db.Device) device {
	out := device{ID: d.ID, Name: d.Name, ApprovedAt: d.ApprovedAt}
	if d.LastSeenAt.Valid {
		out.LastSeenAt = &d.LastSeenAt.Int64
	}
	if d.RevokedAt.Valid {
		out.RevokedAt = &d.RevokedAt.Int64
	}
	return out
}

func (h *DevicesHandler) List(w http.ResponseWriter, r *http.Request) {
	if !requireDevice(w, r, h.DB) {
		return
	}

	devices, err := db.ListDevices(r.Context(), h.DB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	out := make([]device, 0, len(devices))
	for _, d := range devices {
		out = append(out, toDevice(d))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *DevicesHandler) Update(w http.ResponseWriter, r *http.Request) {
	if !requireDevice(w, r, h.DB) {
		return
	}
	id := r.PathValue("id")

	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "request body is not valid JSON")
		return
	}

	v, ok := raw["name"]
	if !ok {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "name is required")
		return
	}
	var name string
	if json.Unmarshal(v, &name) != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "name must be a string")
		return
	}
	if strings.TrimSpace(name) == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "name is required")
		return
	}

	updated, err := db.UpdateDeviceName(r.Context(), h.DB, id, name)
	switch {
	case errors.Is(err, db.ErrNotFound):
		writeError(w, http.StatusNotFound, "DEVICE_NOT_FOUND", "no device with this id")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toDevice(updated))
}

// Delete implements DELETE /api/devices/:id (§6.2, §7.3): revokes a
// device's access by setting revoked_at, which immediately invalidates its
// token (checked in requireDevice via FindActiveDeviceByToken). Never a
// hard delete, per CLAUDE.md.
func (h *DevicesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if !requireDevice(w, r, h.DB) {
		return
	}
	id := r.PathValue("id")

	_, err := db.RevokeDevice(r.Context(), h.DB, id, time.Now().Unix())
	switch {
	case errors.Is(err, db.ErrNotFound):
		writeError(w, http.StatusNotFound, "DEVICE_NOT_FOUND", "no device with this id")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
