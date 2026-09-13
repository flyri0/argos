package main

import (
	"strings"
	"testing"

	"argos/internal/config"
)

func TestTopLevelCommand_ServeSubcommand(t *testing.T) {
	cmd, rest := topLevelCommand([]string{"serve", "--headless"})
	if cmd != "serve" {
		t.Fatalf("expected cmd %q, got %q", "serve", cmd)
	}
	if len(rest) != 1 || rest[0] != "--headless" {
		t.Fatalf("expected rest to be [--headless], got %v", rest)
	}
}

func TestTopLevelCommand_ServiceSubcommand(t *testing.T) {
	cmd, rest := topLevelCommand([]string{"service", "install"})
	if cmd != "service" {
		t.Fatalf("expected cmd %q, got %q", "service", cmd)
	}
	if len(rest) != 1 || rest[0] != "install" {
		t.Fatalf("expected rest to be [install], got %v", rest)
	}
}

func TestTopLevelCommand_BareFlagsImplyServe(t *testing.T) {
	cmd, rest := topLevelCommand([]string{"--headless", "--port=9090"})
	if cmd != "serve" {
		t.Fatalf("expected bare flags to imply serve, got %q", cmd)
	}
	if len(rest) != 2 {
		t.Fatalf("expected rest to be untouched, got %v", rest)
	}
}

func TestTopLevelCommand_NoArgsImplyServe(t *testing.T) {
	cmd, rest := topLevelCommand(nil)
	if cmd != "serve" {
		t.Fatalf("expected no args to imply serve, got %q", cmd)
	}
	if len(rest) != 0 {
		t.Fatalf("expected empty rest, got %v", rest)
	}
}

func TestRunService_UnknownSubcommandRejected(t *testing.T) {
	var out strings.Builder
	if err := runService([]string{"frobnicate"}, &out); err == nil {
		t.Fatalf("expected an unknown service subcommand to be rejected")
	}
}

func TestRunService_NoSubcommandRejected(t *testing.T) {
	var out strings.Builder
	if err := runService(nil, &out); err == nil {
		t.Fatalf("expected no service subcommand to be rejected")
	}
}

func TestParseFlags_Defaults(t *testing.T) {
	f, err := parseFlags(nil)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if f.headless {
		t.Fatalf("expected headless to default to false")
	}
	if f.bindMode != "" || f.port != 0 || f.dataDir != "" {
		t.Fatalf("expected unset flags to be zero values, got %+v", f)
	}
}

func TestParseFlags_HeadlessAndOverrides(t *testing.T) {
	f, err := parseFlags([]string{"--headless", "--bind-mode=lan", "--port=9090", "--data-dir=/tmp/argos-data"})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if !f.headless {
		t.Fatalf("expected headless to be true")
	}
	if f.bindMode != "lan" {
		t.Fatalf("expected bind-mode lan, got %q", f.bindMode)
	}
	if f.port != 9090 {
		t.Fatalf("expected port 9090, got %d", f.port)
	}
	if f.dataDir != "/tmp/argos-data" {
		t.Fatalf("expected data-dir override, got %q", f.dataDir)
	}
}

func TestParseFlags_UnknownFlagRejected(t *testing.T) {
	if _, err := parseFlags([]string{"--not-a-real-flag"}); err == nil {
		t.Fatalf("expected an unknown flag to be rejected")
	}
}

func TestApplyFlagOverrides_OnlySetFlagsOverrideConfig(t *testing.T) {
	cfg := config.Config{BindMode: config.BindModeLocalhost, Port: 8080, DataDir: "/data"}

	got := applyFlagOverrides(cfg, flagOverrides{port: 9999})
	want := config.Config{BindMode: config.BindModeLocalhost, Port: 9999, DataDir: "/data"}
	if got != want {
		t.Fatalf("expected only port to be overridden, got %+v, want %+v", got, want)
	}
}

func TestApplyFlagOverrides_AllFieldsOverridden(t *testing.T) {
	cfg := config.Config{BindMode: config.BindModeLocalhost, Port: 8080, DataDir: "/data"}

	got := applyFlagOverrides(cfg, flagOverrides{bindMode: "lan", port: 9999, dataDir: "/other"})
	want := config.Config{BindMode: config.BindModeLAN, Port: 9999, DataDir: "/other"}
	if got != want {
		t.Fatalf("expected every field to be overridden, got %+v, want %+v", got, want)
	}
}

func TestBindAddr_LocalhostBindsLoopbackOnly(t *testing.T) {
	addr := bindAddr(config.Config{BindMode: config.BindModeLocalhost, Port: 8080})
	if addr != "127.0.0.1:8080" {
		t.Fatalf("expected 127.0.0.1:8080, got %q", addr)
	}
}

func TestBindAddr_LANBindsAllInterfaces(t *testing.T) {
	addr := bindAddr(config.Config{BindMode: config.BindModeLAN, Port: 8080})
	if addr != "0.0.0.0:8080" {
		t.Fatalf("expected 0.0.0.0:8080, got %q", addr)
	}
}
