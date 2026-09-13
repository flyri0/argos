package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"argos/internal/auth"
	"argos/internal/db"
)

func TestNewRouter_ProtectsApiRoutesFromNonLocalhost(t *testing.T) {
	conn := newTestDB(t)
	router, err := NewRouter(conn)
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
	router, err := NewRouter(conn)
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
	router, err := NewRouter(conn)
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

func TestNewRouter_PairingRoutesRemainUnauthenticated(t *testing.T) {
	conn := newTestDB(t)
	router, err := NewRouter(conn)
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
}
