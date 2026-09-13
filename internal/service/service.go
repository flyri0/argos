// Package service registers/removes Argos as a system service (§3.2), so
// it starts with the system without requiring a logged-in user or a
// tray. Linux (a systemd user unit, this file), Windows (a Windows
// Service, service_windows.go), and macOS (a launchd daemon, launchd.go)
// are implemented.
package service

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
)

// unitName is both the unit file's name and its systemd unit name.
const unitName = "argos.service"

// ErrUnsupportedOS is returned by Install and Uninstall on any OS other
// than Linux.
var ErrUnsupportedOS = errors.New("service: not yet supported on this OS")

// ErrNotInstalled is returned by Uninstall when no Argos unit is
// currently installed.
var ErrNotInstalled = errors.New("service: argos is not installed as a service")

// commandRunner runs an external command, returning an error if it
// fails. Swappable in tests so install/uninstall's orchestration can be
// verified without actually invoking systemctl/loginctl.
type commandRunner func(name string, args ...string) error

func realRunner(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Install registers Argos as a system service enabled to start on boot:
// a systemd user unit on Linux, a Windows Service on Windows, or a
// launchd daemon on macOS. Any other OS is not yet supported.
func Install() error {
	switch runtime.GOOS {
	case "linux":
		return installLinux()
	case "windows":
		return installWindowsReal()
	case "darwin":
		return installDarwin()
	default:
		return ErrUnsupportedOS
	}
}

// Uninstall removes whatever Install registered.
func Uninstall() error {
	switch runtime.GOOS {
	case "linux":
		return uninstallLinux()
	case "windows":
		return uninstallWindowsReal()
	case "darwin":
		return uninstallDarwin()
	default:
		return ErrUnsupportedOS
	}
}

// installLinux writes a systemd user unit that runs the current argos
// binary as "argos serve --headless", reloads the user systemd daemon,
// enables and starts the unit, and enables linger for the current user.
// Linger is what lets a --user unit start at boot rather than only at
// next login — without it, "enabled to start on boot" wouldn't actually
// hold for a user unit, contradicting §3.2's "starts with the system ...
// without requiring a logged-in user."
func installLinux() error {
	execPath, err := resolveExecPath()
	if err != nil {
		return err
	}
	unitDir, err := userUnitDir()
	if err != nil {
		return err
	}
	username, err := currentUsername()
	if err != nil {
		return err
	}

	return install(unitDir, execPath, username, realRunner)
}

// uninstallLinux stops and disables the unit and removes its unit file.
func uninstallLinux() error {
	unitDir, err := userUnitDir()
	if err != nil {
		return err
	}

	return uninstall(unitDir, realRunner)
}

func install(unitDir, execPath, username string, run commandRunner) error {
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		return fmt.Errorf("service: creating unit directory: %w", err)
	}

	path := filepath.Join(unitDir, unitName)
	if err := os.WriteFile(path, []byte(unitContent(execPath)), 0o644); err != nil {
		return fmt.Errorf("service: writing unit file: %w", err)
	}

	if err := run("systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("service: systemctl --user daemon-reload: %w", err)
	}
	if err := run("systemctl", "--user", "enable", "--now", unitName); err != nil {
		return fmt.Errorf("service: systemctl --user enable --now %s: %w", unitName, err)
	}
	if err := run("loginctl", "enable-linger", username); err != nil {
		return fmt.Errorf("service: loginctl enable-linger %s: %w", username, err)
	}

	return nil
}

func uninstall(unitDir string, run commandRunner) error {
	path := filepath.Join(unitDir, unitName)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return ErrNotInstalled
	} else if err != nil {
		return fmt.Errorf("service: checking unit file: %w", err)
	}

	if err := run("systemctl", "--user", "disable", "--now", unitName); err != nil {
		return fmt.Errorf("service: systemctl --user disable --now %s: %w", unitName, err)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("service: removing unit file: %w", err)
	}
	if err := run("systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("service: systemctl --user daemon-reload: %w", err)
	}

	return nil
}

// userUnitDir is systemd's fixed location for per-user units:
// $XDG_CONFIG_HOME/systemd/user (falling back to ~/.config/systemd/user),
// which is exactly what os.UserConfigDir() resolves on Linux.
func userUnitDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("service: resolving config dir: %w", err)
	}
	return filepath.Join(dir, "systemd", "user"), nil
}

// resolveExecPath returns the absolute path to the currently running
// argos binary, resolving any symlink so the unit keeps working even if
// whatever symlink was used to invoke "service install" is later changed
// or removed.
func resolveExecPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("service: resolving executable path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("service: resolving executable path: %w", err)
	}
	return resolved, nil
}

func currentUsername() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("service: resolving current user: %w", err)
	}
	return u.Username, nil
}

// unitContent returns the systemd user unit file that runs execPath as
// "<execPath> serve --headless" (this milestone's required command) and
// is wanted by default.target so it starts on boot once linger is
// enabled for the owning user.
func unitContent(execPath string) string {
	return fmt.Sprintf(`[Unit]
Description=Argos personal budgeting server
After=network.target

[Service]
ExecStart=%s serve --headless
Restart=on-failure

[Install]
WantedBy=default.target
`, execPath)
}
