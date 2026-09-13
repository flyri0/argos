package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWithRequestLogging_LogsMethodPathStatusAndDuration(t *testing.T) {
	var out bytes.Buffer
	logger := testLoggerTo(&out)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusCreated, map[string]string{"ok": "yes"})
	})

	req := httptest.NewRequest(http.MethodPost, "/api/accounts", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	w := httptest.NewRecorder()
	withRequestLogging(logger, next).ServeHTTP(w, req)

	line := out.String()
	for _, want := range []string{
		`method=POST`, `path=/api/accounts`, `status=201`, `remote_addr=203.0.113.5`, `duration_ms=`,
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("expected log line to contain %q, got %q", want, line)
		}
	}
}

func TestWithRequestLogging_IncludesErrorCodeOnFailure(t *testing.T) {
	var out bytes.Buffer
	logger := testLoggerTo(&out)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "no account with this id")
	})

	req := httptest.NewRequest(http.MethodGet, "/api/accounts/does-not-exist", nil)
	w := httptest.NewRecorder()
	withRequestLogging(logger, next).ServeHTTP(w, req)

	line := out.String()
	if !strings.Contains(line, `level=WARN`) {
		t.Fatalf("expected a 404 to log at WARN, got %q", line)
	}
	if !strings.Contains(line, `error_code=ACCOUNT_NOT_FOUND`) {
		t.Fatalf("expected the error code in the log line, got %q", line)
	}
}

func TestWithRequestLogging_LogsServerErrorsAtErrorLevel(t *testing.T) {
	var out bytes.Buffer
	logger := testLoggerTo(&out)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "boom")
	})

	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	w := httptest.NewRecorder()
	withRequestLogging(logger, next).ServeHTTP(w, req)

	if !strings.Contains(out.String(), `level=ERROR`) {
		t.Fatalf("expected a 500 to log at ERROR, got %q", out.String())
	}
}

func TestWithRequestLogging_DefaultsToStatus200WhenHandlerNeverWritesAHeader(t *testing.T) {
	var out bytes.Buffer
	logger := testLoggerTo(&out)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	withRequestLogging(logger, next).ServeHTTP(w, req)

	if !strings.Contains(out.String(), `status=200`) {
		t.Fatalf("expected an implicit 200 when WriteHeader is never called, got %q", out.String())
	}
}
