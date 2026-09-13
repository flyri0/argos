package auth

import (
	"database/sql"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"

	"argos/internal/db"
)

// Middleware enforces §6.2's device-pairing rule for every request that
// reaches next: a request originating from 127.0.0.1 or ::1 on the same
// machine running the server is implicitly trusted and passes straight
// through; anything else must carry a valid, non-revoked device token as
// "Authorization: Bearer <token>" (checked against devices.token_hash via
// db.FindActiveDeviceByToken), or the request is rejected with
// 403 PAIRING_REQUIRED (§7.2). A successful token check also updates the
// device's last_seen_at. Callers decide which routes this wraps — it applies
// no path-based scoping itself.
func Middleware(conn *sql.DB, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isLocalhost(r) {
			next.ServeHTTP(w, r)
			return
		}

		token := bearerToken(r)
		if token == "" {
			writePairingRequired(w)
			return
		}

		device, found, err := db.FindActiveDeviceByToken(r.Context(), conn, HashToken(token))
		if err != nil {
			writeInternalError(w, err)
			return
		}
		if !found {
			writePairingRequired(w)
			return
		}

		if err := db.TouchDeviceLastSeen(r.Context(), conn, device.ID, time.Now().Unix()); err != nil {
			writeInternalError(w, err)
			return
		}

		next.ServeHTTP(w, r)
	})
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

func sourceIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func bearerToken(r *http.Request) string {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return ""
	}
	return strings.TrimPrefix(h, prefix)
}

// apiError mirrors the §7.1 error envelope. Middleware lives in package auth
// (per the task) rather than package api, so it writes this shape directly
// instead of importing api's writeError.
type apiError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	body := apiError{}
	body.Error.Code = code
	body.Error.Message = message

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func writePairingRequired(w http.ResponseWriter) {
	writeJSONError(w, http.StatusForbidden, "PAIRING_REQUIRED", "request has no valid, non-revoked device token")
}

func writeInternalError(w http.ResponseWriter, err error) {
	writeJSONError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
}
