package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegisterHealthRoutes_ReturnsOK(t *testing.T) {
	mux := http.NewServeMux()
	RegisterHealthRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != `{"status":"ok"}`+"\n" {
		t.Fatalf("unexpected body: %q", got)
	}
}
