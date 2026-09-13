package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"argos/internal/auth"
	"argos/internal/db"
)

// newTestDevicesHandler seeds an already-paired device directly via the db
// layer (bypassing pairing) and returns a handler plus that device's raw
// token, for exercising the auth-gated endpoints.
func newTestDevicesHandler(t *testing.T) (*DevicesHandler, db.Device, string) {
	t.Helper()
	conn := newTestDB(t)

	token, err := auth.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	dev, err := db.CreateFirstDevice(t.Context(), conn, db.NewFirstDevice{
		Name:       "First device",
		TokenHash:  auth.HashToken(token),
		ApprovedAt: 1000,
	})
	if err != nil {
		t.Fatalf("CreateFirstDevice: %v", err)
	}
	return &DevicesHandler{DB: conn}, dev, token
}

func authedRequest(method, target string, body []byte, token string) *http.Request {
	var r *http.Request
	if body == nil {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, bytes.NewReader(body))
	}
	r.RemoteAddr = "203.0.113.5:1234" // not localhost, so the token is what authenticates
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return r
}

func TestDevicesList_RequiresAuth(t *testing.T) {
	h, _, _ := newTestDevicesHandler(t)

	w := httptest.NewRecorder()
	h.List(w, authedRequest(http.MethodGet, "/api/devices", nil, ""))

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
	var errResp apiError
	json.Unmarshal(w.Body.Bytes(), &errResp)
	if errResp.Error.Code != "PAIRING_REQUIRED" {
		t.Fatalf("expected PAIRING_REQUIRED, got %+v", errResp)
	}
}

func TestDevicesList_LocalhostBypassesAuth(t *testing.T) {
	h, dev, _ := newTestDevicesHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	w := httptest.NewRecorder()
	h.List(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var out []device
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 1 || out[0].ID != dev.ID {
		t.Fatalf("expected the one paired device, got %+v", out)
	}
}

func TestDevicesList_ExcludesTokenHash(t *testing.T) {
	h, _, token := newTestDevicesHandler(t)

	w := httptest.NewRecorder()
	h.List(w, authedRequest(http.MethodGet, "/api/devices", nil, token))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "token_hash") || strings.Contains(w.Body.String(), token) {
		t.Fatalf("expected response to never include token_hash or the raw token, got %s", w.Body.String())
	}
}

func TestDevicesList_IncludesRevokedDevices(t *testing.T) {
	h, dev, _ := newTestDevicesHandler(t)

	if _, err := db.RevokeDevice(t.Context(), h.DB, dev.ID, 2000); err != nil {
		t.Fatalf("RevokeDevice: %v", err)
	}

	// The token used to authenticate is now revoked, so use localhost to
	// reach the endpoint and still verify the revoked device is listed.
	req := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	w := httptest.NewRecorder()
	h.List(w, req)

	var out []device
	json.Unmarshal(w.Body.Bytes(), &out)
	if len(out) != 1 || out[0].RevokedAt == nil {
		t.Fatalf("expected the revoked dev to still appear with revoked_at set, got %+v", out)
	}
}

func TestDevicesUpdate_RenamesDevice(t *testing.T) {
	h, dev, token := newTestDevicesHandler(t)

	body, _ := json.Marshal(map[string]string{"name": "Kitchen tablet"})
	req := authedRequest(http.MethodPatch, "/api/devices/"+dev.ID, body, token)
	req.SetPathValue("id", dev.ID)
	w := httptest.NewRecorder()
	h.Update(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var out device
	json.Unmarshal(w.Body.Bytes(), &out)
	if out.Name != "Kitchen tablet" {
		t.Fatalf("expected renamed device, got %+v", out)
	}
}

func TestDevicesUpdate_UnknownIDReturns404(t *testing.T) {
	h, _, token := newTestDevicesHandler(t)

	body, _ := json.Marshal(map[string]string{"name": "Ghost"})
	req := authedRequest(http.MethodPatch, "/api/devices/does-not-exist", body, token)
	req.SetPathValue("id", "does-not-exist")
	w := httptest.NewRecorder()
	h.Update(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
	var errResp apiError
	json.Unmarshal(w.Body.Bytes(), &errResp)
	if errResp.Error.Code != "DEVICE_NOT_FOUND" {
		t.Fatalf("expected DEVICE_NOT_FOUND, got %+v", errResp)
	}
}

func TestDevicesUpdate_RequiresAuth(t *testing.T) {
	h, dev, _ := newTestDevicesHandler(t)

	body, _ := json.Marshal(map[string]string{"name": "Nope"})
	req := authedRequest(http.MethodPatch, "/api/devices/"+dev.ID, body, "")
	req.SetPathValue("id", dev.ID)
	w := httptest.NewRecorder()
	h.Update(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDevicesDelete_SetsRevokedAt(t *testing.T) {
	h, dev, token := newTestDevicesHandler(t)

	req := authedRequest(http.MethodDelete, "/api/devices/"+dev.ID, nil, token)
	req.SetPathValue("id", dev.ID)
	w := httptest.NewRecorder()
	h.Delete(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}

	// The device's own token must no longer authenticate — a hard-delete
	// vs. revoked_at distinction that matters per CLAUDE.md's no-hard-delete
	// rule: the row still exists, but access is gone.
	_, found, err := db.FindActiveDeviceByToken(t.Context(), h.DB, auth.HashToken(token))
	if err != nil {
		t.Fatalf("FindActiveDeviceByToken: %v", err)
	}
	if found {
		t.Fatalf("expected revoked device's token to no longer be active")
	}

	all, err := db.ListDevices(t.Context(), h.DB)
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected the row to still exist (no hard delete), got %d devices", len(all))
	}
}

func TestDevicesDelete_UnknownIDReturns404(t *testing.T) {
	h, _, token := newTestDevicesHandler(t)

	req := authedRequest(http.MethodDelete, "/api/devices/does-not-exist", nil, token)
	req.SetPathValue("id", "does-not-exist")
	w := httptest.NewRecorder()
	h.Delete(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDevicesDelete_RequiresAuth(t *testing.T) {
	h, dev, _ := newTestDevicesHandler(t)

	req := authedRequest(http.MethodDelete, "/api/devices/"+dev.ID, nil, "")
	req.SetPathValue("id", dev.ID)
	w := httptest.NewRecorder()
	h.Delete(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}
