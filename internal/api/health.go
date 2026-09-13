package api

import "net/http"

// RegisterHealthRoutes registers GET /health (§7.3), a liveness check that
// stays outside auth.Middleware (see requiresDeviceAuth) so it can be probed
// before any device has paired.
func RegisterHealthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
}
