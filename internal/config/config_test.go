package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDefault_MatchesSpecDefaults(t *testing.T) {
	cfg, err := Default()
	if err != nil {
		t.Fatalf("Default: %v", err)
	}
	if cfg.BindMode != BindModeLocalhost {
		t.Fatalf("expected default bind_mode %q, got %q", BindModeLocalhost, cfg.BindMode)
	}
	if cfg.Port != DefaultPort {
		t.Fatalf("expected default port %d, got %d", DefaultPort, cfg.Port)
	}
	if filepath.Base(cfg.DataDir) != "argos" {
		t.Fatalf("expected data_dir to end in an \"argos\" directory, got %q", cfg.DataDir)
	}
}

func TestLoad_NoFileReturnsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want, err := Default()
	if err != nil {
		t.Fatalf("Default: %v", err)
	}
	if cfg != want {
		t.Fatalf("expected defaults %+v, got %+v", want, cfg)
	}
}

func writeConfigFile(t *testing.T, contents any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	data, err := json.Marshal(contents)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestLoad_FileOverridesDefaults(t *testing.T) {
	path := writeConfigFile(t, map[string]any{
		"bind_mode": "lan",
		"port":      9090,
		"data_dir":  filepath.Join(t.TempDir(), "custom-data"),
	})

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.BindMode != BindModeLAN {
		t.Fatalf("expected bind_mode %q, got %q", BindModeLAN, cfg.BindMode)
	}
	if cfg.Port != 9090 {
		t.Fatalf("expected port 9090, got %d", cfg.Port)
	}
}

func TestLoad_PartialFileKeepsDefaultsForMissingFields(t *testing.T) {
	path := writeConfigFile(t, map[string]any{"port": 9999})

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 9999 {
		t.Fatalf("expected the overridden port 9999, got %d", cfg.Port)
	}
	if cfg.BindMode != BindModeLocalhost {
		t.Fatalf("expected bind_mode to keep its default %q since the file didn't set it, got %q", BindModeLocalhost, cfg.BindMode)
	}
	if filepath.Base(cfg.DataDir) != "argos" {
		t.Fatalf("expected data_dir to keep its default since the file didn't set it, got %q", cfg.DataDir)
	}
}

func TestLoad_EnvOverridesFileAndDefaults(t *testing.T) {
	path := writeConfigFile(t, map[string]any{
		"bind_mode": "localhost",
		"port":      9090,
	})

	t.Setenv("ARGOS_BIND_MODE", "lan")
	t.Setenv("ARGOS_PORT", "7000")
	t.Setenv("ARGOS_DATA_DIR", filepath.Join(t.TempDir(), "env-data"))

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.BindMode != BindModeLAN {
		t.Fatalf("expected env override bind_mode %q, got %q", BindModeLAN, cfg.BindMode)
	}
	if cfg.Port != 7000 {
		t.Fatalf("expected env override port 7000, got %d", cfg.Port)
	}
	if filepath.Base(cfg.DataDir) != "env-data" {
		t.Fatalf("expected env override data_dir, got %q", cfg.DataDir)
	}
}

func TestLoad_EnvOverridesDefaultsWithNoFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	t.Setenv("ARGOS_PORT", "6543")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 6543 {
		t.Fatalf("expected env override port 6543 even with no config file, got %d", cfg.Port)
	}
}

func TestLoad_InvalidBindModeRejected(t *testing.T) {
	path := writeConfigFile(t, map[string]any{"bind_mode": "everywhere"})

	if _, err := Load(path); err == nil {
		t.Fatalf("expected an invalid bind_mode to be rejected")
	}
}

func TestLoad_InvalidPortEnvRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	t.Setenv("ARGOS_PORT", "not-a-number")

	if _, err := Load(path); err == nil {
		t.Fatalf("expected a non-numeric ARGOS_PORT to be rejected")
	}
}

func TestLoad_InvalidPortValueRejected(t *testing.T) {
	path := writeConfigFile(t, map[string]any{"port": -1})

	if _, err := Load(path); err == nil {
		t.Fatalf("expected an out-of-range port to be rejected")
	}
}

func TestLoad_MalformedJSONRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if _, err := Load(path); err == nil {
		t.Fatalf("expected malformed JSON to be rejected")
	}
}

func TestDefaultPath_EndsInArgosConfigJSON(t *testing.T) {
	path, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	if filepath.Base(path) != "config.json" {
		t.Fatalf("expected path to end in config.json, got %q", path)
	}
	if filepath.Base(filepath.Dir(path)) != "argos" {
		t.Fatalf("expected config.json to live under an argos directory, got %q", path)
	}
}
