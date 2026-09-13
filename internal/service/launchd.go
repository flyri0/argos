package service

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// darwinLabel is both the plist's Label and the launchd service target
// name ("system/<label>") used with launchctl.
const darwinLabel = "com.argos.server"

// darwinPlistPath is a launchd daemon plist: a fixed, well-known system
// path (unlike systemd's per-user unit directory), so unlike userUnitDir
// this needs no per-machine resolution.
const darwinPlistPath = "/Library/LaunchDaemons/" + darwinLabel + ".plist"

// installDarwin writes a launchd daemon plist that runs the current argos
// binary as "argos serve --headless" under the invoking user's account,
// bootstraps it into the system domain, enables it, and starts it now.
// A LaunchDaemon (system-wide, /Library/LaunchDaemons) is used rather
// than a per-user LaunchAgent because only a daemon starts at boot
// without requiring a logged-in GUI session — macOS has no equivalent of
// systemd's per-user "linger" to make a LaunchAgent do that — matching
// §3.2's "starts with the system ... without requiring a logged-in
// user." This does mean install/uninstall need to run as root (sudo),
// same as Windows Service registration needs Administrator.
func installDarwin() error {
	execPath, err := resolveExecPath()
	if err != nil {
		return err
	}
	username, err := currentUsername()
	if err != nil {
		return err
	}

	return installLaunchd(darwinPlistPath, execPath, username, realRunner)
}

// uninstallDarwin stops and removes the daemon and its plist.
func uninstallDarwin() error {
	return uninstallLaunchd(darwinPlistPath, realRunner)
}

func installLaunchd(plistPath, execPath, username string, run commandRunner) error {
	if err := os.WriteFile(plistPath, []byte(launchdPlist(execPath, username)), 0o644); err != nil {
		return fmt.Errorf("service: writing plist: %w", err)
	}

	if err := run("launchctl", "bootstrap", "system", plistPath); err != nil {
		return fmt.Errorf("service: launchctl bootstrap system %s: %w", plistPath, err)
	}
	if err := run("launchctl", "enable", "system/"+darwinLabel); err != nil {
		return fmt.Errorf("service: launchctl enable system/%s: %w", darwinLabel, err)
	}
	if err := run("launchctl", "kickstart", "-k", "system/"+darwinLabel); err != nil {
		return fmt.Errorf("service: launchctl kickstart system/%s: %w", darwinLabel, err)
	}

	return nil
}

func uninstallLaunchd(plistPath string, run commandRunner) error {
	if _, err := os.Stat(plistPath); errors.Is(err, os.ErrNotExist) {
		return ErrNotInstalled
	} else if err != nil {
		return fmt.Errorf("service: checking plist: %w", err)
	}

	if err := run("launchctl", "bootout", "system/"+darwinLabel); err != nil {
		return fmt.Errorf("service: launchctl bootout system/%s: %w", darwinLabel, err)
	}
	if err := os.Remove(plistPath); err != nil {
		return fmt.Errorf("service: removing plist: %w", err)
	}

	return nil
}

// launchdPlist returns the launchd daemon plist that runs execPath as
// "<execPath> serve --headless" (this milestone's required command)
// under username, restarting it on failure, and starting it at load.
func launchdPlist(execPath, username string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>serve</string>
		<string>--headless</string>
	</array>
	<key>UserName</key>
	<string>%s</string>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
</dict>
</plist>
`, xmlEscape(darwinLabel), xmlEscape(execPath), xmlEscape(username))
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
