package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"argos/internal/db"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "argos.db")
	if err := db.Migrate(path); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	conn, err := db.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

var setupCodePattern = regexp.MustCompile(`Argos setup code: (\S+)`)

func newTestPairingHandler(t *testing.T) (*PairingHandler, string) {
	t.Helper()
	conn := newTestDB(t)

	var out bytes.Buffer
	h, err := newPairingHandler(conn, &out)
	if err != nil {
		t.Fatalf("newPairingHandler: %v", err)
	}

	m := setupCodePattern.FindStringSubmatch(out.String())
	if m == nil {
		t.Fatalf("expected setup code printed to stdout, got %q", out.String())
	}
	return h, m[1]
}

func postBootstrap(t *testing.T, h *PairingHandler, code string) (*httptest.ResponseRecorder, bootstrapResponse) {
	t.Helper()
	body, _ := json.Marshal(bootstrapRequest{Code: code})
	req := httptest.NewRequest(http.MethodPost, "/api/pairing/bootstrap", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.Bootstrap(w, req)

	var resp bootstrapResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	return w, resp
}

func TestPairingBootstrap_CorrectCodeCreatesFirstDevice(t *testing.T) {
	h, code := newTestPairingHandler(t)

	w, resp := postBootstrap(t, h, code)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if resp.Name != "First device" {
		t.Fatalf("expected name %q, got %q", "First device", resp.Name)
	}
	if resp.ID == "" || resp.Token == "" {
		t.Fatalf("expected non-empty id and token, got %+v", resp)
	}

	empty, err := db.DevicesEmpty(t.Context(), h.DB)
	if err != nil {
		t.Fatalf("DevicesEmpty: %v", err)
	}
	if empty {
		t.Fatalf("expected devices to be non-empty after bootstrap")
	}
}

func TestPairingBootstrap_WrongCodeRejectedAsRateLimited(t *testing.T) {
	h, code := newTestPairingHandler(t)

	w, _ := postBootstrap(t, h, code+"x")
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d: %s", w.Code, w.Body.String())
	}
	var errResp apiError
	json.Unmarshal(w.Body.Bytes(), &errResp)
	if errResp.Error.Code != "PAIRING_RATE_LIMITED" {
		t.Fatalf("expected PAIRING_RATE_LIMITED, got %+v", errResp)
	}

	empty, err := db.DevicesEmpty(t.Context(), h.DB)
	if err != nil {
		t.Fatalf("DevicesEmpty: %v", err)
	}
	if !empty {
		t.Fatalf("expected devices to remain empty after a wrong code")
	}
}

func TestPairingBootstrap_LockoutAfterFiveFailedAttemptsBlocksEvenCorrectCode(t *testing.T) {
	h, code := newTestPairingHandler(t)

	for i := 0; i < 5; i++ {
		w, _ := postBootstrap(t, h, "wrong-code")
		if w.Code != http.StatusTooManyRequests {
			t.Fatalf("attempt %d: expected 429, got %d", i+1, w.Code)
		}
	}

	// The 6th attempt, even with the correct code, must be blocked: the
	// source is now within its 1-minute lockout window (§6.3).
	w, _ := postBootstrap(t, h, code)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected correct code to still be rejected during lockout, got %d: %s", w.Code, w.Body.String())
	}

	empty, err := db.DevicesEmpty(t.Context(), h.DB)
	if err != nil {
		t.Fatalf("DevicesEmpty: %v", err)
	}
	if !empty {
		t.Fatalf("expected no device created while locked out")
	}
}

func TestPairingBootstrap_ClosedOnceDevicesNonEmpty(t *testing.T) {
	h, code := newTestPairingHandler(t)

	if w, _ := postBootstrap(t, h, code); w.Code != http.StatusCreated {
		t.Fatalf("expected first bootstrap to succeed, got %d", w.Code)
	}

	w, _ := postBootstrap(t, h, code)
	if w.Code != http.StatusGone {
		t.Fatalf("expected 410 once devices is non-empty, got %d: %s", w.Code, w.Body.String())
	}
	var errResp apiError
	json.Unmarshal(w.Body.Bytes(), &errResp)
	if errResp.Error.Code != "BOOTSTRAP_CLOSED" {
		t.Fatalf("expected BOOTSTRAP_CLOSED, got %+v", errResp)
	}
}

func TestPairingBootstrap_NoCodePrintedWhenDevicesAlreadyExist(t *testing.T) {
	conn := newTestDB(t)

	var out1 bytes.Buffer
	h1, err := newPairingHandler(conn, &out1)
	if err != nil {
		t.Fatalf("newPairingHandler: %v", err)
	}
	m := setupCodePattern.FindStringSubmatch(out1.String())
	if m == nil {
		t.Fatalf("expected a setup code on the first construction")
	}
	if w, _ := postBootstrap(t, h1, m[1]); w.Code != http.StatusCreated {
		t.Fatalf("expected bootstrap to succeed, got %d", w.Code)
	}

	// A later restart of the handler against the same (now non-empty)
	// devices table must not generate or print a new code at all.
	var out2 bytes.Buffer
	h2, err := newPairingHandler(conn, &out2)
	if err != nil {
		t.Fatalf("newPairingHandler (restart): %v", err)
	}
	if strings.Contains(out2.String(), "Argos setup code") {
		t.Fatalf("expected no setup code printed once devices is non-empty, got %q", out2.String())
	}

	w, _ := postBootstrap(t, h2, m[1])
	if w.Code != http.StatusGone {
		t.Fatalf("expected 410 from a restarted handler with devices already paired, got %d", w.Code)
	}
}

func postRequestPairing(t *testing.T, h *PairingHandler) (*httptest.ResponseRecorder, requestPairingResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/pairing/request", nil)
	w := httptest.NewRecorder()
	h.Request(w, req)

	var resp requestPairingResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	return w, resp
}

func postApprove(t *testing.T, h *PairingHandler, code string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(approveRequest{Code: code})
	req := httptest.NewRequest(http.MethodPost, "/api/pairing/approve", bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:1234" // localhost implicitly trusted, per §6.2
	w := httptest.NewRecorder()
	h.Approve(w, req)
	return w
}

func TestPairingRequest_ReturnsFourDigitCodeExpiringInTenMinutes(t *testing.T) {
	h, _ := newTestPairingHandler(t)

	w, resp := postRequestPairing(t, h)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if len(resp.Code) != 4 {
		t.Fatalf("expected a 4-digit code, got %q", resp.Code)
	}
	for _, c := range resp.Code {
		if c < '0' || c > '9' {
			t.Fatalf("expected an all-numeric code, got %q", resp.Code)
		}
	}

	wantExpiry := time.Now().Add(10 * time.Minute).Unix()
	if diff := resp.ExpiresAt - wantExpiry; diff < -2 || diff > 2 {
		t.Fatalf("expected expires_at ~%d (now+10m), got %d", wantExpiry, resp.ExpiresAt)
	}
}

func TestPairingApprove_CorrectCodeCreatesUnnamedDevice(t *testing.T) {
	h, _ := newTestPairingHandler(t)
	_, reqResp := postRequestPairing(t, h)

	w := postApprove(t, h, reqResp.Code)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp bootstrapResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Name != "Unnamed device" {
		t.Fatalf("expected name %q, got %q", "Unnamed device", resp.Name)
	}
	if resp.ID == "" || resp.Token == "" {
		t.Fatalf("expected non-empty id and token, got %+v", resp)
	}
}

func TestPairingApprove_CodeIsSingleUse(t *testing.T) {
	h, _ := newTestPairingHandler(t)
	_, reqResp := postRequestPairing(t, h)

	if w := postApprove(t, h, reqResp.Code); w.Code != http.StatusCreated {
		t.Fatalf("expected first approve to succeed, got %d: %s", w.Code, w.Body.String())
	}

	// Re-presenting the same code must fail, not silently mint a second
	// device — the code is consumed on first (correct) use.
	w := postApprove(t, h, reqResp.Code)
	if w.Code == http.StatusCreated {
		t.Fatalf("expected re-using a consumed pairing code to fail")
	}
}

func TestPairingApprove_UnknownCodeRejected(t *testing.T) {
	h, _ := newTestPairingHandler(t)

	w := postApprove(t, h, "0000")
	if w.Code == http.StatusCreated {
		t.Fatalf("expected an unknown code to be rejected, got 201")
	}
}

func TestPairingApprove_LockoutAfterFiveFailedAttempts(t *testing.T) {
	h, _ := newTestPairingHandler(t)

	for i := 0; i < 5; i++ {
		w := postApprove(t, h, "9999")
		if w.Code == http.StatusCreated {
			t.Fatalf("attempt %d: expected wrong code to be rejected", i+1)
		}
	}

	// A correct code presented immediately after must still be rejected —
	// the source is now within its 1-minute lockout (§6.3), same rule as
	// bootstrap.
	_, reqResp := postRequestPairing(t, h)
	w := postApprove(t, h, reqResp.Code)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 during lockout, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPairingApprove_ExpiredCodeRejected(t *testing.T) {
	h, _ := newTestPairingHandler(t)
	_, reqResp := postRequestPairing(t, h)

	// Force the code to look expired by rewriting its expiry directly,
	// rather than sleeping 10 real minutes in a test.
	h.pendingMu.Lock()
	h.pending[reqResp.Code] = pendingCode{expiresAt: time.Now().Add(-time.Second)}
	h.pendingMu.Unlock()

	w := postApprove(t, h, reqResp.Code)
	if w.Code == http.StatusCreated {
		t.Fatalf("expected an expired code to be rejected")
	}
}

func TestPairingApprove_RequiresAuthFromNonLocalhost(t *testing.T) {
	h, _ := newTestPairingHandler(t)
	_, reqResp := postRequestPairing(t, h)

	body, _ := json.Marshal(approveRequest{Code: reqResp.Code})
	req := httptest.NewRequest(http.MethodPost, "/api/pairing/approve", bytes.NewReader(body))
	req.RemoteAddr = "203.0.113.5:1234" // not localhost, no Authorization header
	w := httptest.NewRecorder()
	h.Approve(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
	var errResp apiError
	json.Unmarshal(w.Body.Bytes(), &errResp)
	if errResp.Error.Code != "PAIRING_REQUIRED" {
		t.Fatalf("expected PAIRING_REQUIRED, got %+v", errResp)
	}
}
