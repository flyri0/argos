package api

import (
	"io/fs"
	"net/http"
)

// RegisterFrontendRoutes registers the catch-all "/" route that serves the
// embedded frontend build (§2.1) directly from frontend. It's registered
// on the same mux as every other route so Go's ServeMux picks the more
// specific "/api/...", "/sync", etc. patterns first and only falls back to
// serving a static file (or 404) for anything else.
//
// Deliberately outside auth.Middleware's protection (see
// requiresDeviceAuth, which only gates "/api/*" paths): a device with no
// token yet still needs to load the app shell to reach the pairing screen
// in the first place (§6.2).
func RegisterFrontendRoutes(mux *http.ServeMux, frontend fs.FS) {
	mux.Handle("/", http.FileServerFS(frontend))
}
