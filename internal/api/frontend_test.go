package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestRegisterFrontendRoutes_ServesIndexAndKnownAssets(t *testing.T) {
	frontend := fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte("<!doctype html><title>Argos</title>")},
		"assets/app.js": &fstest.MapFile{Data: []byte("console.log('hi')")},
		"manifest.json": &fstest.MapFile{Data: []byte(`{"name":"Argos"}`)},
	}
	mux := http.NewServeMux()
	RegisterFrontendRoutes(mux, frontend)

	for _, path := range []string{"/", "/assets/app.js", "/manifest.json"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("GET %s: expected 200, got %d", path, w.Code)
		}
	}
}

func TestRegisterFrontendRoutes_UnknownPathIs404(t *testing.T) {
	frontend := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html>")},
	}
	mux := http.NewServeMux()
	RegisterFrontendRoutes(mux, frontend)

	req := httptest.NewRequest(http.MethodGet, "/no-such-file.js", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for an unknown path, got %d", w.Code)
	}
}
