package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
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
	logger  *slog.Logger
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
	pollLimiter    *auth.RateLimiter
}

// pendingCode is one outstanding §6.2 pairing code. `claimed` and `token`
// track its life past /api/pairing/approve: the code stays in `pending`
// (rather than being deleted like a plain rejected code) so the *waiting*
// device — which never sees /approve's response, since that goes to the
// *approving* device — can retrieve its token via
// GET /api/pairing/request/{code} (§6.2, §7.3). `claimed` is set the
// instant a correct /approve request is matched, atomically with that
// check, so two concurrent approvals of the same code can't both mint a
// device; `token` is filled in once minting actually succeeds.
type pendingCode struct {
	expiresAt time.Time
	claimed   bool
	token     string
}

// pairingCodeTTL is the exact 10-minute lifetime from §6.2.
const pairingCodeTTL = 10 * time.Minute

// RegisterPairingRoutes registers the device pairing endpoints. On first
// startup (an empty devices table, §6.1) it generates the bootstrap setup
// code and logs it. It returns the handler so callers outside this package
// (desktop mode's tray, §6.1: "the tray icon also shows it directly") can
// read the live setup code via PairingHandler.SetupCode. logger must not
// be nil.
func RegisterPairingRoutes(mux *http.ServeMux, conn *sql.DB, logger *slog.Logger) (*PairingHandler, error) {
	h, err := newPairingHandler(conn, logger)
	if err != nil {
		return nil, err
	}
	mux.HandleFunc("POST /api/pairing/bootstrap", h.Bootstrap)
	mux.HandleFunc("POST /api/pairing/request", h.Request)
	mux.HandleFunc("POST /api/pairing/approve", h.Approve)
	mux.HandleFunc("GET /api/pairing/request/{code}", h.Poll)
	return h, nil
}

// SetupCode returns the live §6.1 bootstrap setup code and whether it's
// still active. It re-checks the devices table on every call rather than
// trusting a cached flag, the same way Bootstrap itself always re-checks
// before accepting a code, so a caller polling this (e.g. the tray, to know
// when to stop displaying the code) sees the moment bootstrap actually
// closes rather than a stale in-memory snapshot.
func (h *PairingHandler) SetupCode(ctx context.Context) (code string, active bool, err error) {
	empty, err := db.DevicesEmpty(ctx, h.DB)
	if err != nil {
		return "", false, err
	}
	if !empty {
		return "", false, nil
	}
	return h.code, true, nil
}

func newPairingHandler(conn *sql.DB, logger *slog.Logger) (*PairingHandler, error) {
	h := &PairingHandler{
		DB:             conn,
		logger:         logger,
		limiter:        auth.NewRateLimiter(),
		pending:        make(map[string]pendingCode),
		approveLimiter: auth.NewRateLimiter(),
		pollLimiter:    auth.NewRateLimiter(),
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
		logger.Info("bootstrap setup code", "code", code)
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

	h.logger.Info("first device paired via bootstrap", "device_id", device.ID, "remote_addr", ip)
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
//
// Unlike the code this replaced, a correctly-matched entry is *not*
// deleted here — it's marked claimed and, once minting succeeds, given the
// token — so GET /api/pairing/request/{code} can still hand that token to
// the waiting device afterwards (§6.2). Only an expired entry is dropped
// outright, same as before.
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

	// Checked before touching `pending` at all: if this source is already
	// locked out, a correct code here must still be rejected (§6.3), and
	// critically must NOT be claimed — claiming it here and then rejecting
	// it below would permanently strand an otherwise-good code.
	if h.approveLimiter.LockedOut(ip, now) {
		h.approveLimiter.Attempt(ip, now, false)
		writeError(w, http.StatusTooManyRequests, "PAIRING_RATE_LIMITED", "too many failed pairing attempts from this source")
		return
	}

	h.pendingMu.Lock()
	pending, found := h.pending[req.Code]
	expired := found && !now.Before(pending.expiresAt)
	correct := req.Code != "" && found && !expired && !pending.claimed
	switch {
	case correct:
		// Claimed atomically with the check above, under the same lock
		// acquisition, so two simultaneous correct approvals of the same
		// code can't both pass: the second sees claimed == true.
		pending.claimed = true
		h.pending[req.Code] = pending
	case found && expired:
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

	h.pendingMu.Lock()
	h.pending[req.Code] = pendingCode{expiresAt: pending.expiresAt, claimed: true, token: token}
	h.pendingMu.Unlock()

	h.logger.Info("device approved", "device_id", device.ID, "approving_remote_addr", ip)
	writeJSON(w, http.StatusCreated, bootstrapResponse{
		ID:         device.ID,
		Name:       device.Name,
		Token:      token,
		ApprovedAt: device.ApprovedAt,
	})
}

type pollPairingResponse struct {
	Status string `json:"status"`
	Token  string `json:"token,omitempty"`
}

// Poll implements GET /api/pairing/request/{code} (§6.2, §7.3): the half
// of the pairing loop that was otherwise missing — /api/pairing/approve's
// response goes to the *approving* device, never to the device that's
// actually waiting, so this is how the waiting device learns it's been
// approved and gets its own token. While the code is still outstanding it
// returns {"status":"pending"}; once /approve has minted a token for it,
// returns {"status":"approved","token":"..."} exactly once and clears the
// entry so it can never be retrieved again. An unknown, already-retrieved,
// or expired code is a 404, not just a plain rejection, since (unlike
// /approve) there's no "wrong guess vs. correct" distinction to conflate —
// this is a lookup, and a stale/missing code no longer names anything.
//
// This is unauthenticated by design: it's polled by a device that has no
// token yet. It carries the same guessable-secret risk as the codes
// themselves, though, so it gets its own §6.3 lockout rather than none.
func (h *PairingHandler) Poll(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	ip := sourceIP(r)
	now := time.Now()

	if h.pollLimiter.LockedOut(ip, now) {
		h.pollLimiter.Attempt(ip, now, false)
		writeError(w, http.StatusTooManyRequests, "PAIRING_RATE_LIMITED", "too many failed pairing attempts from this source")
		return
	}

	h.pendingMu.Lock()
	pending, found := h.pending[code]
	if found && !now.Before(pending.expiresAt) {
		delete(h.pending, code)
		found = false
	}
	var resp pollPairingResponse
	if found {
		if pending.token != "" {
			resp = pollPairingResponse{Status: "approved", Token: pending.token}
			delete(h.pending, code)
		} else {
			resp = pollPairingResponse{Status: "pending"}
		}
	}
	h.pendingMu.Unlock()

	h.pollLimiter.Attempt(ip, now, found)

	if !found {
		writeError(w, http.StatusNotFound, "PAIRING_CODE_NOT_FOUND", "no pending pairing request for this code")
		return
	}
	writeJSON(w, http.StatusOK, resp)
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
