//go:build darwin

package tray

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
)

// autostartLabel is both the plist's Label and the launchd service target
// used with launchctl. This is a per-user LaunchAgent (login item),
// distinct from internal/service's system-wide LaunchDaemon used for
// headless mode — it only needs to run while the user is logged in, so
// it belongs in the user's own GUI session domain and needs no root.
const autostartLabel = "com.argos.tray"

func autostartPlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("tray: resolving home dir: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", autostartLabel+".plist"), nil
}

func guiDomain() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("tray: resolving current user: %w", err)
	}
	return "gui/" + u.Uid, nil
}

func isAutostartEnabled() (bool, error) {
	path, err := autostartPlistPath()
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("tray: checking login item: %w", err)
	}
	return true, nil
}

func enableAutostart(execPath string) error {
	path, err := autostartPlistPath()
	if err != nil {
		return err
	}
	domain, err := guiDomain()
	if err != nil {
		return err
	}
	return enableAutostartAt(path, domain, execPath, realRunner)
}

func disableAutostart() error {
	path, err := autostartPlistPath()
	if err != nil {
		return err
	}
	domain, err := guiDomain()
	if err != nil {
		return err
	}
	return disableAutostartAt(path, domain, realRunner)
}

func enableAutostartAt(plistPath, domain, execPath string, run commandRunner) error {
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return fmt.Errorf("tray: creating LaunchAgents dir: %w", err)
	}
	if err := os.WriteFile(plistPath, []byte(loginAgentPlist(execPath)), 0o644); err != nil {
		return fmt.Errorf("tray: writing login item plist: %w", err)
	}
	if err := run("launchctl", "bootstrap", domain, plistPath); err != nil {
		return fmt.Errorf("tray: launchctl bootstrap %s: %w", domain, err)
	}
	return nil
}

func disableAutostartAt(plistPath, domain string, run commandRunner) error {
	if _, err := os.Stat(plistPath); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("tray: checking login item: %w", err)
	}
	if err := run("launchctl", "bootout", domain+"/"+autostartLabel); err != nil {
		return fmt.Errorf("tray: launchctl bootout %s/%s: %w", domain, autostartLabel, err)
	}
	if err := os.Remove(plistPath); err != nil {
		return fmt.Errorf("tray: removing login item plist: %w", err)
	}
	return nil
}

// loginAgentPlist returns the LaunchAgent plist that runs execPath (the
// desktop tray binary itself, with no arguments — a login item should
// come back up in the same desktop/tray mode, not headless) at login.
func loginAgentPlist(execPath string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
</dict>
</plist>
`, xmlEscape(autostartLabel), xmlEscape(execPath))
}

var xmlEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
	"'", "&apos;",
)

func xmlEscape(s string) string {
	return xmlEscaper.Replace(s)
}

// commandRunner runs an external command. Swappable in tests so
// enable/disableAutostartAt's orchestration can be verified without
// actually invoking launchctl.
type commandRunner func(name string, args ...string) error

func realRunner(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
