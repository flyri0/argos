package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"argos/internal/auth"
	"argos/internal/db"
)

// PairingHandler implements the device pairing endpoints (§6, §7.3),
// starting with the bootstrap flow (§6.1). mu serializes bootstrap
// attempts: the endpoint only ever succeeds once per installation, so a
// single mutex around "check code, then create the device" is simpler than
// finer-grained locking and costs nothing in practice.
type PairingHandler struct {
	DB      *sql.DB
	mu      sync.Mutex
	code    string // the live bootstrap setup code; "" once devices is non-empty
	limiter *auth.RateLimiter

	// pendingMu guards pending, the in-memory table of live §6.2 pairing
	// codes. These are short-lived, single-installation, request-scoped
	// secrets — not synced data — so unlike devices there's no case for
	// persisting them to SQLite; losing them on a server restart just means
	// an in-flight pairing has to be requested again.
	pendingMu sync.Mutex
	pending   map[string]pendingCode

	approveLimiter *auth.RateLimiter
}

// pendingCode is one outstanding §6.2 pairing code, waiting to be presented
// to /api/pairing/approve before it expires.
type pendingCode struct {
	expiresAt time.Time
}

// pairingCodeTTL is the exact 10-minute lifetime from §6.2.
const pairingCodeTTL = 10 * time.Minute

// RegisterPairingRoutes registers the device pairing endpoints. On first
// startup (an empty devices table, §6.1) it generates the bootstrap setup
// code and prints it to stdout.
func RegisterPairingRoutes(mux *http.ServeMux, conn *sql.DB) error {
	h, err := newPairingHandler(conn, os.Stdout)
	if err != nil {
		return err
	}
	mux.HandleFunc("POST /api/pairing/bootstrap", h.Bootstrap)
	mux.HandleFunc("POST /api/pairing/request", h.Request)
	mux.HandleFunc("POST /api/pairing/approve", h.Approve)
	return nil
}

func newPairingHandler(conn *sql.DB, out io.Writer) (*PairingHandler, error) {
	h := &PairingHandler{
		DB:             conn,
		limiter:        auth.NewRateLimiter(),
		pending:        make(map[string]pendingCode),
		approveLimiter: auth.NewRateLimiter(),
	}

	empty, err := db.DevicesEmpty(context.Background(), conn)
	if err != nil {
		return nil, err
	}
	if empty {
		code, err := auth.GenerateSetupCode()
		if err != nil {
			return nil, err
		}
		h.code = code
		fmt.Fprintf(out, "Argos setup code: %s\n", code)
	}

	return h, nil
}

type bootstrapRequest struct {
	Code string `json:"code"`
}

type bootstrapResponse struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Token      string `json:"token"`
	ApprovedAt int64  `json:"approved_at"`
}

// Bootstrap implements POST /api/pairing/bootstrap (§6.1, §7.3). While
// devices is empty, it accepts the printed setup code and, on a match,
// creates the first device and returns its token. Once devices is
// non-empty, it always responds 410 Gone.
func (h *PairingHandler) Bootstrap(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()

	empty, err := db.DevicesEmpty(r.Context(), h.DB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	if !empty {
		writeError(w, http.StatusGone, "BOOTSTRAP_CLOSED", "bootstrap is only available before the first device has paired")
		return
	}

	var req bootstrapRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "request body is not valid JSON")
		return
	}

	ip := sourceIP(r)
	now := time.Now()
	correct := req.Code != "" && auth.ConstantTimeEqual(req.Code, h.code)

	if !h.limiter.Attempt(ip, now, correct) {
		writeError(w, http.StatusTooManyRequests, "PAIRING_RATE_LIMITED", "too many failed pairing attempts from this source")
		return
	}

	token, err := auth.GenerateToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	device, err := db.CreateFirstDevice(r.Context(), h.DB, db.NewFirstDevice{
		Name:       "First device",
		TokenHash:  auth.HashToken(token),
		ApprovedAt: now.Unix(),
	})
	switch {
	case errors.Is(err, db.ErrBootstrapClosed):
		writeError(w, http.StatusGone, "BOOTSTRAP_CLOSED", "bootstrap is only available before the first device has paired")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, bootstrapResponse{
		ID:         device.ID,
		Name:       device.Name,
		Token:      token,
		ApprovedAt: device.ApprovedAt,
	})
}

type requestPairingResponse struct {
	Code      string `json:"code"`
	ExpiresAt int64  `json:"expires_at"`
}

// Request implements POST /api/pairing/request (§6.2, §7.3): an unpaired
// device asks to join and receives a short pairing code, valid for exactly
// 10 minutes, to be read aloud/typed by the user into an already-trusted
// device. Unlike /api/pairing/bootstrap and /api/pairing/approve, this
// endpoint has no secret to guess — it always succeeds — so §6.3's lockout,
// which exists specifically to bound guessing attempts, does not apply
// here (see §6.3's own wording, which names only those two endpoints).
func (h *PairingHandler) Request(w http.ResponseWriter, r *http.Request) {
	code, err := auth.GeneratePairingCode()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	now := time.Now()
	expiresAt := now.Add(pairingCodeTTL)

	h.pendingMu.Lock()
	h.pending[code] = pendingCode{expiresAt: expiresAt}
	h.pendingMu.Unlock()

	writeJSON(w, http.StatusCreated, requestPairingResponse{Code: code, ExpiresAt: expiresAt.Unix()})
}

type approveRequest struct {
	Code string `json:"code"`
}

// Approve implements POST /api/pairing/approve (§6.2, §7.3): an
// already-trusted device (or localhost, §6.2) confirms a pending pairing
// code, minting a token for the requesting device and recording it in
// devices with the default name "Unnamed device". The pairing code is a
// network-guessable secret, so §6.3's lockout applies here exactly as it
// does to /api/pairing/bootstrap.
func (h *PairingHandler) Approve(w http.ResponseWriter, r *http.Request) {
	if !requireDevice(w, r, h.DB) {
		return
	}

	var req approveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "request body is not valid JSON")
		return
	}

	ip := sourceIP(r)
	now := time.Now()

	h.pendingMu.Lock()
	pending, found := h.pending[req.Code]
	correct := req.Code != "" && found && now.Before(pending.expiresAt)
	if found {
		// Consume the code on a correct match, and drop it once expired
		// either way — an expired code is useless and would otherwise sit
		// in the map forever.
		delete(h.pending, req.Code)
	}
	h.pendingMu.Unlock()

	if !h.approveLimiter.Attempt(ip, now, correct) {
		writeError(w, http.StatusTooManyRequests, "PAIRING_RATE_LIMITED", "too many failed pairing attempts from this source")
		return
	}

	token, err := auth.GenerateToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	device, err := db.CreateDevice(r.Context(), h.DB, db.NewDevice{
		Name:       "Unnamed device",
		TokenHash:  auth.HashToken(token),
		ApprovedAt: now.Unix(),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, bootstrapResponse{
		ID:         device.ID,
		Name:       device.Name,
		Token:      token,
		ApprovedAt: device.ApprovedAt,
	})
}

// sourceIP extracts the request's source IP, stripping the port that
// r.RemoteAddr normally carries (e.g. "192.168.1.5:54321"); it falls back
// to the raw value if that fails, since any non-empty string still works
// fine as a rate-limiter key.
func sourceIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
