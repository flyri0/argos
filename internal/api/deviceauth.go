package api

import (
	"database/sql"
	"net/http"
	"strings"
	"time"

	"argos/internal/auth"
	"argos/internal/db"
)

// requireDevice enforces §6.2's rule for the privileged pairing/device
// endpoints (/api/pairing/approve and /api/devices/*): a request from
// localhost is implicitly trusted; anything else must carry a valid,
// non-revoked device token as "Authorization: Bearer <token>" (the wire
// format for carrying the token isn't specified in §6.2, so this follows
// the standard HTTP convention). On success it also updates the device's
// last_seen_at (§6.2's device list shows this), since these are the only
// endpoints so far that authenticate a device. On failure it writes the
// 403 PAIRING_REQUIRED response itself and returns false.
func requireDevice(w http.ResponseWriter, r *http.Request, conn *sql.DB) bool {
	if isLocalhost(r) {
		return true
	}

	token := bearerToken(r)
	if token == "" {
		writeError(w, http.StatusForbidden, "PAIRING_REQUIRED", "request has no valid, non-revoked device token")
		return false
	}

	device, found, err := db.FindActiveDeviceByToken(r.Context(), conn, auth.HashToken(token))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return false
	}
	if !found {
		writeError(w, http.StatusForbidden, "PAIRING_REQUIRED", "request has no valid, non-revoked device token")
		return false
	}

	if err := db.TouchDeviceLastSeen(r.Context(), conn, device.ID, time.Now().Unix()); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return false
	}
	return true
}

// isLocalhost reports whether r originates from the same machine running
// the server (§6.2: "a request originating from 127.0.0.1 ... does not
// need pairing").
func isLocalhost(r *http.Request) bool {
	switch sourceIP(r) {
	case "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

func bearerToken(r *http.Request) string {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return ""
	}
	return strings.TrimPrefix(h, prefix)
}
