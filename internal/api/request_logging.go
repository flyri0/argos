package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// statusRecorder captures the status code of every response, and — only
// once that status turns out to be an error — the response body too, so
// withRequestLogging can report the exact error code (§7.2) alongside the
// HTTP status rather than just the bare status number.
type statusRecorder struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	if r.status >= http.StatusBadRequest {
		r.body.Write(b)
	}
	return r.ResponseWriter.Write(b)
}

// withRequestLogging logs every request that reaches next: method, path,
// status, how long it took, and the caller's address. Wrapping the whole
// router this way — rather than adding a log call inside every individual
// handler — is what actually gives "the whole backend" HTTP-level
// visibility from one place. A 4xx/5xx response additionally logs the
// error code and message from the response body (§7.1's envelope), since
// the bare status alone doesn't say which of several possible failures
// happened at that path.
func withRequestLogging(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}

		attrs := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote_addr", sourceIP(r),
		}
		if status >= http.StatusBadRequest {
			var body apiError
			if json.Unmarshal(rec.body.Bytes(), &body) == nil && body.Error.Code != "" {
				attrs = append(attrs, "error_code", body.Error.Code, "error_message", body.Error.Message)
			}
		}

		switch {
		case status >= http.StatusInternalServerError:
			logger.Error("request", attrs...)
		case status >= http.StatusBadRequest:
			logger.Warn("request", attrs...)
		default:
			logger.Info("request", attrs...)
		}
	})
}
