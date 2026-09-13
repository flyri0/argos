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
