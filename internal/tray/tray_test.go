package tray

import (
	"bytes"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"argos/internal/config"
)

func TestModeLabel(t *testing.T) {
	orig := lanAddr
	lanAddr = func() (string, error) { return "192.168.1.50", nil }
	defer func() { lanAddr = orig }()

	app := &App{cfg: config.Config{BindMode: config.BindModeLocalhost, Port: 8080}}
	if got := app.modeLabel(); got != "Mode: localhost only, port 8080" {
		t.Fatalf("unexpected label: %q", got)
	}

	app.cfg.BindMode = config.BindModeLAN
	if got := app.modeLabel(); got != "Mode: LAN — http://192.168.1.50:8080" {
		t.Fatalf("unexpected label: %q", got)
	}
}

func TestModeLabel_FallsBackWhenLANAddressUnavailable(t *testing.T) {
	orig := lanAddr
	lanAddr = func() (string, error) { return "", errors.New("no route") }
	defer func() { lanAddr = orig }()

	app := &App{cfg: config.Config{BindMode: config.BindModeLAN, Port: 9090}}
	if got := app.modeLabel(); got != "Mode: LAN, port 9090" {
		t.Fatalf("unexpected label: %q", got)
	}
}

func TestLanAddr_ReturnsAValidIPWhenARouteExists(t *testing.T) {
	ip, err := lanAddr()
	if err != nil {
		t.Skipf("no outbound route available in this environment: %v", err)
	}
	if net.ParseIP(ip) == nil {
		t.Fatalf("expected a valid IP address, got %q", ip)
	}
}

func TestOpenBrowserCommand(t *testing.T) {
	cases := []struct {
		goos     string
		wantName string
	}{
		{"windows", "cmd"},
		{"darwin", "open"},
		{"linux", "xdg-open"},
		{"freebsd", "xdg-open"},
	}
	for _, c := range cases {
		name, args := openBrowserCommand(c.goos, "http://127.0.0.1:8080")
		if name != c.wantName {
			t.Fatalf("%s: expected command %q, got %q", c.goos, c.wantName, name)
		}
		if len(args) == 0 || args[len(args)-1] != "http://127.0.0.1:8080" {
			t.Fatalf("%s: expected the URL to be the last argument, got %v", c.goos, args)
		}
	}
}

func TestApp_StartStopServer(t *testing.T) {
	var out bytes.Buffer
	app := &App{
		cfg:     config.Config{BindMode: config.BindModeLocalhost, Port: 0},
		handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }),
		stdout:  &out,
	}

	if err := app.startServer(); err != nil {
		t.Fatalf("startServer: %v", err)
	}
	addr := app.actualAddr
	if addr == "" {
		t.Fatalf("expected actualAddr to be set after startServer")
	}

	resp, err := http.Get("http://" + addr)
	if err != nil {
		t.Fatalf("GET %s: %v", addr, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	app.stopServer()

	if _, err := http.Get("http://" + addr); err == nil {
		t.Fatalf("expected requests to fail once the server is stopped")
	}
}

func TestApp_RestartServerMovesToANewPort(t *testing.T) {
	var out bytes.Buffer
	app := &App{
		cfg:     config.Config{BindMode: config.BindModeLocalhost, Port: 0},
		handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }),
		stdout:  &out,
	}

	if err := app.startServer(); err != nil {
		t.Fatalf("startServer: %v", err)
	}
	firstAddr := app.actualAddr

	if err := app.restartServer(); err != nil {
		t.Fatalf("restartServer: %v", err)
	}
	secondAddr := app.actualAddr
	defer app.stopServer()

	if secondAddr == "" {
		t.Fatalf("expected actualAddr to be set after restartServer")
	}

	// The old listener must actually be gone.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := http.Get("http://" + firstAddr); err != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := http.Get("http://" + firstAddr); err == nil {
		t.Fatalf("expected the original listener at %s to be closed after restart", firstAddr)
	}

	resp, err := http.Get("http://" + secondAddr)
	if err != nil {
		t.Fatalf("GET %s: %v", secondAddr, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from the restarted server, got %d", resp.StatusCode)
	}
}
