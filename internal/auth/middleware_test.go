package auth

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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

func newPairedDevice(t *testing.T, conn *sql.DB) (db.Device, string) {
	t.Helper()
	token, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	dev, err := db.CreateFirstDevice(t.Context(), conn, db.NewFirstDevice{
		Name:       "First device",
		TokenHash:  HashToken(token),
		ApprovedAt: 1000,
	})
	if err != nil {
		t.Fatalf("CreateFirstDevice: %v", err)
	}
	return dev, token
}

func passThroughHandler(called *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*called = true
		w.WriteHeader(http.StatusOK)
	})
}

func TestMiddleware_LocalhostBypassesAuth(t *testing.T) {
	conn := newTestDB(t)
	var called bool
	mw := Middleware(conn, passThroughHandler(&called))

	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if !called || w.Code != http.StatusOK {
		t.Fatalf("expected localhost request to reach next handler, called=%v code=%d", called, w.Code)
	}
}

func TestMiddleware_IPv6LoopbackBypassesAuth(t *testing.T) {
	conn := newTestDB(t)
	var called bool
	mw := Middleware(conn, passThroughHandler(&called))

	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	req.RemoteAddr = "[::1]:5555"
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if !called || w.Code != http.StatusOK {
		t.Fatalf("expected ::1 request to reach next handler, called=%v code=%d", called, w.Code)
	}
}

func TestMiddleware_ValidTokenPassesThroughAndTouchesLastSeen(t *testing.T) {
	conn := newTestDB(t)
	dev, token := newPairedDevice(t, conn)
	var called bool
	mw := Middleware(conn, passThroughHandler(&called))

	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if !called || w.Code != http.StatusOK {
		t.Fatalf("expected valid token to reach next handler, called=%v code=%d", called, w.Code)
	}

	updated, found, err := db.FindActiveDeviceByToken(t.Context(), conn, HashToken(token))
	if err != nil {
		t.Fatalf("FindActiveDeviceByToken: %v", err)
	}
	if !found {
		t.Fatalf("expected device to still be active")
	}
	if !updated.LastSeenAt.Valid {
		t.Fatalf("expected last_seen_at to be set after an authenticated request, got %+v", updated)
	}
	_ = dev
}

func TestMiddleware_MissingTokenRejectedFromNonLocalhost(t *testing.T) {
	conn := newTestDB(t)
	var called bool
	mw := Middleware(conn, passThroughHandler(&called))

	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if called {
		t.Fatalf("expected request with no token to never reach next handler")
	}
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "PAIRING_REQUIRED") {
		t.Fatalf("expected PAIRING_REQUIRED, got %s", w.Body.String())
	}
}

func TestMiddleware_UnknownTokenRejected(t *testing.T) {
	conn := newTestDB(t)
	var called bool
	mw := Middleware(conn, passThroughHandler(&called))

	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	req.Header.Set("Authorization", "Bearer not-a-real-token")
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if called {
		t.Fatalf("expected request with unknown token to never reach next handler")
	}
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestMiddleware_RevokedTokenRejected(t *testing.T) {
	conn := newTestDB(t)
	dev, token := newPairedDevice(t, conn)
	if _, err := db.RevokeDevice(t.Context(), conn, dev.ID, 2000); err != nil {
		t.Fatalf("RevokeDevice: %v", err)
	}
	var called bool
	mw := Middleware(conn, passThroughHandler(&called))

	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if called {
		t.Fatalf("expected request with a revoked token to never reach next handler")
	}
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}
