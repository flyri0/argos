//go:build linux

package tray

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// autostartDesktopFile is the XDG Autostart spec's per-user location:
// desktop environments (GNOME, KDE, XFCE, ...) scan
// $XDG_CONFIG_HOME/autostart (falling back to ~/.config/autostart) for
// .desktop entries at login — no daemon or "enable" command involved, so
// unlike the darwin/windows implementations this is pure file I/O.
func autostartDesktopFile() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("tray: resolving config dir: %w", err)
	}
	return filepath.Join(dir, "autostart", "argos.desktop"), nil
}

func isAutostartEnabled() (bool, error) {
	path, err := autostartDesktopFile()
	if err != nil {
		return false, err
	}
	return autostartFileExists(path)
}

func enableAutostart(execPath string) error {
	path, err := autostartDesktopFile()
	if err != nil {
		return err
	}
	return writeAutostartFile(path, execPath)
}

func disableAutostart() error {
	path, err := autostartDesktopFile()
	if err != nil {
		return err
	}
	return removeAutostartFile(path)
}

func autostartFileExists(path string) (bool, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("tray: checking autostart entry: %w", err)
	}
	return true, nil
}

func writeAutostartFile(path, execPath string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("tray: creating autostart dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(autostartDesktopEntry(execPath)), 0o644); err != nil {
		return fmt.Errorf("tray: writing autostart entry: %w", err)
	}
	return nil
}

func removeAutostartFile(path string) error {
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("tray: removing autostart entry: %w", err)
	}
	return nil
}

// autostartDesktopEntry returns the XDG desktop entry that launches
// execPath (the desktop tray binary, with no arguments) at login.
func autostartDesktopEntry(execPath string) string {
	return fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=Argos
Exec=%s
X-GNOME-Autostart-enabled=true
`, execPath)
}
