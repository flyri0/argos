package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"argos/internal/auth"
	"argos/internal/db"
)

// testFrontend stands in for the real embedded build (internal/webui)
// in these router-level tests, which only care about request routing and
// auth, not actual frontend content.
func testFrontend() fstest.MapFS {
	return fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>test</title>")},
	}
}

// testLogger discards its output — these tests assert on HTTP behavior,
// not on log lines, and a nil *slog.Logger would panic NewRouter's request
// logging middleware.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestNewRouter_ProtectsApiRoutesFromNonLocalhost(t *testing.T) {
	conn := newTestDB(t)
	router, err := NewRouter(conn, testFrontend(), testLogger())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without a device token, got %d: %s", w.Code, w.Body.String())
	}
}

func TestNewRouter_LocalhostReachesApiRoutesWithoutAToken(t *testing.T) {
	conn := newTestDB(t)
	router, err := NewRouter(conn, testFrontend(), testLogger())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 from localhost, got %d: %s", w.Code, w.Body.String())
	}
}

func TestNewRouter_ValidDeviceTokenReachesApiRoutes(t *testing.T) {
	conn := newTestDB(t)
	router, err := NewRouter(conn, testFrontend(), testLogger())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	token, err := auth.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if _, err := db.CreateFirstDevice(t.Context(), conn, db.NewFirstDevice{
		Name:       "First device",
		TokenHash:  auth.HashToken(token),
		ApprovedAt: 1000,
	}); err != nil {
		t.Fatalf("CreateFirstDevice: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with a valid device token, got %d: %s", w.Code, w.Body.String())
	}
}

func TestNewRouter_SyncRequiresDeviceAuth(t *testing.T) {
	conn := newTestDB(t)
	router, err := NewRouter(conn, testFrontend(), testLogger())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	// /sync sits outside /api/* (§7.3), but it still pushes/pulls a
	// device's full budget data — §6.2's "every request after pairing
	// carries that device's token" applies to it exactly like any other
	// API route. Only /health is the documented, deliberate exception.
	body := bytes.NewBufferString(`{"since": 0, "mutations": []}`)
	req := httptest.NewRequest(http.MethodPost, "/sync", body)
	req.RemoteAddr = "203.0.113.5:1234"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without a device token, got %d: %s", w.Code, w.Body.String())
	}
	var errResp apiError
	json.Unmarshal(w.Body.Bytes(), &errResp)
	if errResp.Error.Code != "PAIRING_REQUIRED" {
		t.Fatalf("expected PAIRING_REQUIRED, got %+v", errResp)
	}
}

func TestNewRouter_SyncReachableFromLocalhostWithoutAToken(t *testing.T) {
	conn := newTestDB(t)
	router, err := NewRouter(conn, testFrontend(), testLogger())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	body := bytes.NewBufferString(`{"since": 0, "mutations": []}`)
	req := httptest.NewRequest(http.MethodPost, "/sync", body)
	req.RemoteAddr = "127.0.0.1:5555"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 from localhost, got %d: %s", w.Code, w.Body.String())
	}
}

func TestNewRouter_SyncReachableWithValidDeviceToken(t *testing.T) {
	conn := newTestDB(t)
	router, err := NewRouter(conn, testFrontend(), testLogger())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	token, err := auth.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if _, err := db.CreateFirstDevice(t.Context(), conn, db.NewFirstDevice{
		Name:       "First device",
		TokenHash:  auth.HashToken(token),
		ApprovedAt: 1000,
	}); err != nil {
		t.Fatalf("CreateFirstDevice: %v", err)
	}

	body := bytes.NewBufferString(`{"since": 0, "mutations": []}`)
	req := httptest.NewRequest(http.MethodPost, "/sync", body)
	req.RemoteAddr = "203.0.113.5:1234"
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with a valid device token, got %d: %s", w.Code, w.Body.String())
	}
}

func TestNewRouter_PairingRoutesRemainUnauthenticated(t *testing.T) {
	conn := newTestDB(t)
	router, err := NewRouter(conn, testFrontend(), testLogger())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/pairing/request", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code == http.StatusForbidden {
		t.Fatalf("expected pairing routes to stay reachable without a device token, got 403: %s", w.Body.String())
	}

	// GET /api/pairing/request/:code (§6.2) is polled by a device that has
	// no token yet, so it must stay unauthenticated too.
	pollReq := httptest.NewRequest(http.MethodGet, "/api/pairing/request/0000", nil)
	pollReq.RemoteAddr = "203.0.113.5:1234"
	pollW := httptest.NewRecorder()
	router.ServeHTTP(pollW, pollReq)

	if pollW.Code == http.StatusForbidden {
		t.Fatalf("expected the pairing poll route to stay reachable without a device token, got 403: %s", pollW.Body.String())
	}
}

func TestNewRouter_HealthReachableWithoutADeviceToken(t *testing.T) {
	conn := newTestDB(t)
	router, err := NewRouter(conn, testFrontend(), testLogger())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 from /health without a device token, got %d: %s", w.Code, w.Body.String())
	}
}

func TestNewRouter_FrontendServedWithoutADeviceToken(t *testing.T) {
	conn := newTestDB(t)
	router, err := NewRouter(conn, testFrontend(), testLogger())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	// An unpaired device (non-localhost, no token) still needs to load the
	// app shell to reach the pairing screen in the first place (§6.2), so
	// the frontend must never sit behind auth.Middleware.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 serving the frontend without a token, got %d: %s", w.Code, w.Body.String())
	}
	if w.Body.String() != "<!doctype html><title>test</title>" {
		t.Fatalf("expected the embedded frontend's index.html, got %q", w.Body.String())
	}
}
