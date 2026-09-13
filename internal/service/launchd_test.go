package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLaunchdPlist_RunsServeHeadless(t *testing.T) {
	plist := launchdPlist("/usr/local/bin/argos", "alice")

	if !strings.Contains(plist, "<string>/usr/local/bin/argos</string>") ||
		!strings.Contains(plist, "<string>serve</string>") ||
		!strings.Contains(plist, "<string>--headless</string>") {
		t.Fatalf("expected ProgramArguments to run \"serve --headless\", got:\n%s", plist)
	}
	if !strings.Contains(plist, "<key>RunAtLoad</key>\n\t<true/>") {
		t.Fatalf("expected RunAtLoad true so the daemon starts on boot, got:\n%s", plist)
	}
	if !strings.Contains(plist, "<string>alice</string>") {
		t.Fatalf("expected UserName alice, got:\n%s", plist)
	}
}

func TestLaunchdPlist_EscapesSpecialCharacters(t *testing.T) {
	plist := launchdPlist(`/Users/a&b/argos`, "o'brien")

	if !strings.Contains(plist, "/Users/a&amp;b/argos") {
		t.Fatalf("expected & to be escaped, got:\n%s", plist)
	}
	if !strings.Contains(plist, "o&apos;brien") {
		t.Fatalf("expected ' to be escaped, got:\n%s", plist)
	}
}

func TestInstallLaunchd_WritesPlistAndRunsExpectedCommandsInOrder(t *testing.T) {
	plistPath := filepath.Join(t.TempDir(), "com.argos.server.plist")
	f := &fakeRunner{}

	if err := installLaunchd(plistPath, "/usr/local/bin/argos", "alice", f.run); err != nil {
		t.Fatalf("installLaunchd: %v", err)
	}

	data, err := os.ReadFile(plistPath)
	if err != nil {
		t.Fatalf("expected plist to be written: %v", err)
	}
	if !strings.Contains(string(data), "/usr/local/bin/argos") {
		t.Fatalf("unexpected plist contents:\n%s", data)
	}

	want := [][]string{
		{"launchctl", "bootstrap", "system", plistPath},
		{"launchctl", "enable", "system/" + darwinLabel},
		{"launchctl", "kickstart", "-k", "system/" + darwinLabel},
	}
	if len(f.calls) != len(want) {
		t.Fatalf("expected %d commands, got %d: %+v", len(want), len(f.calls), f.calls)
	}
	for i, call := range want {
		if strings.Join(call, " ") != strings.Join(f.calls[i], " ") {
			t.Fatalf("call %d: expected %v, got %v", i, call, f.calls[i])
		}
	}
}

func TestInstallLaunchd_StopsAtFirstFailingCommand(t *testing.T) {
	plistPath := filepath.Join(t.TempDir(), "com.argos.server.plist")
	f := &fakeRunner{failOn: "launchctl", failArg: "enable"}

	err := installLaunchd(plistPath, "/usr/local/bin/argos", "alice", f.run)
	if err == nil {
		t.Fatalf("expected install to fail when launchctl enable fails")
	}
	for _, call := range f.calls {
		if len(call) > 1 && call[1] == "kickstart" {
			t.Fatalf("expected kickstart to never run once an earlier step failed, got calls %+v", f.calls)
		}
	}
}

func TestUninstallLaunchd_NotInstalledReturnsErrNotInstalled(t *testing.T) {
	plistPath := filepath.Join(t.TempDir(), "com.argos.server.plist")
	f := &fakeRunner{}

	err := uninstallLaunchd(plistPath, f.run)
	if err != ErrNotInstalled {
		t.Fatalf("expected ErrNotInstalled, got %v", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("expected no commands to run when nothing is installed, got %+v", f.calls)
	}
}

func TestUninstallLaunchd_RemovesPlistAndRunsExpectedCommands(t *testing.T) {
	plistPath := filepath.Join(t.TempDir(), "com.argos.server.plist")
	f := &fakeRunner{}
	if err := installLaunchd(plistPath, "/usr/local/bin/argos", "alice", f.run); err != nil {
		t.Fatalf("installLaunchd (setup): %v", err)
	}
	f.calls = nil

	if err := uninstallLaunchd(plistPath, f.run); err != nil {
		t.Fatalf("uninstallLaunchd: %v", err)
	}

	if _, err := os.Stat(plistPath); !os.IsNotExist(err) {
		t.Fatalf("expected plist to be removed, stat err = %v", err)
	}

	want := [][]string{
		{"launchctl", "bootout", "system/" + darwinLabel},
	}
	if len(f.calls) != len(want) {
		t.Fatalf("expected %d commands, got %d: %+v", len(want), len(f.calls), f.calls)
	}
	for i, call := range want {
		if strings.Join(call, " ") != strings.Join(f.calls[i], " ") {
			t.Fatalf("call %d: expected %v, got %v", i, call, f.calls[i])
		}
	}
}

func TestUninstallLaunchd_PlistKeptWhenBootoutFails(t *testing.T) {
	plistPath := filepath.Join(t.TempDir(), "com.argos.server.plist")
	setup := &fakeRunner{}
	if err := installLaunchd(plistPath, "/usr/local/bin/argos", "alice", setup.run); err != nil {
		t.Fatalf("installLaunchd (setup): %v", err)
	}

	f := &fakeRunner{failOn: "launchctl", failArg: "bootout"}
	if err := uninstallLaunchd(plistPath, f.run); err == nil {
		t.Fatalf("expected uninstall to fail when launchctl bootout fails")
	}

	if _, err := os.Stat(plistPath); err != nil {
		t.Fatalf("expected plist to remain when bootout fails, stat err = %v", err)
	}
}
