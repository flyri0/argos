package api

import (
	"database/sql"
	"io/fs"
	"net/http"
	"strings"

	"argos/internal/auth"
)

// NewRouter assembles the full HTTP API by registering every endpoint from
// every milestone on one mux, then wrapping it so §6.2's device-pairing
// check (auth.Middleware) actually protects every /api/* route except the
// pairing endpoints themselves, which exist precisely to let an unpaired
// device obtain a token in the first place. /sync and /health are top-level
// paths outside /api/* (§7.3) and are therefore untouched by this wrapping.
// frontend is the embedded PWA build (§2.1, internal/webui.Dist()), served
// for every route this mux doesn't otherwise claim.
func NewRouter(conn *sql.DB, frontend fs.FS) (http.Handler, error) {
	mux := http.NewServeMux()

	RegisterAccountRoutes(mux, conn)
	RegisterCategoryRoutes(mux, conn)
	RegisterPayeeRoutes(mux, conn)
	RegisterTransactionRoutes(mux, conn)
	RegisterBudgetRoutes(mux, conn)
	RegisterDeviceRoutes(mux, conn)
	RegisterSyncRoutes(mux, conn)
	if err := RegisterPairingRoutes(mux, conn); err != nil {
		return nil, err
	}
	RegisterFrontendRoutes(mux, frontend)

	return wrapDeviceAuth(mux, conn), nil
}

// wrapDeviceAuth routes each request either straight to mux, or through
// auth.Middleware first (which itself calls back into mux once the request
// is authenticated), based on requiresDeviceAuth.
func wrapDeviceAuth(mux *http.ServeMux, conn *sql.DB) http.Handler {
	protected := auth.Middleware(conn, mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requiresDeviceAuth(r.URL.Path) {
			protected.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// requiresDeviceAuth reports whether path is subject to §6.2's device-token
// check: every /api/* route except /api/pairing/*.
func requiresDeviceAuth(path string) bool {
	if !strings.HasPrefix(path, "/api/") {
		return false
	}
	return !strings.HasPrefix(path, "/api/pairing/")
}
