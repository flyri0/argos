// Package config loads Argos's shared runtime configuration (§3.3): the
// bind mode, port, and data directory read by both desktop and headless
// mode from one config file, with environment variable overrides.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
)

// BindMode is the listener's bind mode, per §3.3: "localhost" (the
// default) or "lan".
type BindMode string

const (
	BindModeLocalhost BindMode = "localhost"
	BindModeLAN       BindMode = "lan"

	// DefaultPort is used when no config file exists yet and no
	// ARGOS_PORT override is set.
	DefaultPort = 8080
)

// Config holds the three fields §3.3 says both run modes read from the
// same config file.
type Config struct {
	BindMode BindMode `json:"bind_mode"`
	Port     int      `json:"port"`
	DataDir  string   `json:"data_dir"`
}

// Validate reports whether c holds values that are safe to run with.
func (c Config) Validate() error {
	switch c.BindMode {
	case BindModeLocalhost, BindModeLAN:
	default:
		return fmt.Errorf("config: invalid bind_mode %q (must be %q or %q)", c.BindMode, BindModeLocalhost, BindModeLAN)
	}
	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("config: invalid port %d", c.Port)
	}
	if c.DataDir == "" {
		return fmt.Errorf("config: data_dir must not be empty")
	}
	return nil
}

// Default returns the built-in defaults for a machine with no config file
// yet: bind_mode "localhost" (§3.3 — "localhost-only is the default until
// the user opts into LAN"), port 8080, and data_dir under this OS's
// per-user data directory.
func Default() (Config, error) {
	dataDir, err := defaultDataDir()
	if err != nil {
		return Config{}, err
	}
	return Config{
		BindMode: BindModeLocalhost,
		Port:     DefaultPort,
		DataDir:  dataDir,
	}, nil
}

// DefaultPath returns the per-OS location of the config file (e.g. the
// XDG config dir on Linux), via os.UserConfigDir().
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config: resolving config dir: %w", err)
	}
	return filepath.Join(dir, "argos", "config.json"), nil
}

// Load resolves the effective configuration: Default() values, overridden
// by any fields present in the JSON config file at path (if it exists),
// further overridden by the ARGOS_BIND_MODE, ARGOS_PORT, and
// ARGOS_DATA_DIR environment variables when set (§3.2: "provided via a
// config file and/or environment variables"). The result is validated
// before being returned.
func Load(path string) (Config, error) {
	cfg, err := Default()
	if err != nil {
		return Config{}, err
	}

	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("config: parsing %s: %w", path, err)
		}
	case errors.Is(err, os.ErrNotExist):
		// No config file yet — the defaults above stand, per §3.3.
	default:
		return Config{}, fmt.Errorf("config: reading %s: %w", path, err)
	}

	if err := applyEnvOverrides(&cfg); err != nil {
		return Config{}, err
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func applyEnvOverrides(cfg *Config) error {
	if v, ok := os.LookupEnv("ARGOS_BIND_MODE"); ok {
		cfg.BindMode = BindMode(v)
	}
	if v, ok := os.LookupEnv("ARGOS_PORT"); ok {
		port, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("config: invalid ARGOS_PORT %q: %w", v, err)
		}
		cfg.Port = port
	}
	if v, ok := os.LookupEnv("ARGOS_DATA_DIR"); ok {
		cfg.DataDir = v
	}
	return nil
}

// defaultDataDir resolves this OS's conventional per-user data directory
// plus an "argos" subdirectory. Linux distinguishes data ($XDG_DATA_HOME,
// falling back to ~/.local/share) from config; Windows and macOS use the
// same per-user directory as os.UserConfigDir() for both, since neither
// draws that distinction the way XDG does.
func defaultDataDir() (string, error) {
	if runtime.GOOS == "linux" {
		if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
			return filepath.Join(dir, "argos"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("config: resolving home dir: %w", err)
		}
		return filepath.Join(home, ".local", "share", "argos"), nil
	}

	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config: resolving data dir: %w", err)
	}
	return filepath.Join(dir, "argos"), nil
}
