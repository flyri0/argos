package service

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeRunner records every command invocation and lets a test fail a
// specific step by name, so install/uninstall's orchestration can be
// verified without ever shelling out to systemctl/loginctl for real.
type fakeRunner struct {
	calls   [][]string
	failOn  string // command name to fail on, e.g. "systemctl"
	failArg string // only fail if this arg is present (empty = always fail on failOn)
}

func (f *fakeRunner) run(name string, args ...string) error {
	call := append([]string{name}, args...)
	f.calls = append(f.calls, call)
	if name == f.failOn {
		if f.failArg == "" {
			return errFake
		}
		for _, a := range args {
			if a == f.failArg {
				return errFake
			}
		}
	}
	return nil
}

var errFake = &fakeError{"fake command failure"}

type fakeError struct{ msg string }

func (e *fakeError) Error() string { return e.msg }

func TestUnitContent_RunsServeHeadless(t *testing.T) {
	content := unitContent("/usr/local/bin/argos")

	if !strings.Contains(content, "ExecStart=/usr/local/bin/argos serve --headless") {
		t.Fatalf("expected ExecStart to run \"serve --headless\", got:\n%s", content)
	}
	if !strings.Contains(content, "WantedBy=default.target") {
		t.Fatalf("expected the unit to be wanted by default.target so it starts on boot, got:\n%s", content)
	}
}

func TestInstall_WritesUnitAndRunsExpectedCommandsInOrder(t *testing.T) {
	unitDir := filepath.Join(t.TempDir(), "systemd", "user")
	f := &fakeRunner{}

	if err := install(unitDir, "/usr/local/bin/argos", "alice", f.run); err != nil {
		t.Fatalf("install: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(unitDir, unitName))
	if err != nil {
		t.Fatalf("expected unit file to be written: %v", err)
	}
	if !strings.Contains(string(data), "ExecStart=/usr/local/bin/argos serve --headless") {
		t.Fatalf("unexpected unit file contents:\n%s", data)
	}

	want := [][]string{
		{"systemctl", "--user", "daemon-reload"},
		{"systemctl", "--user", "enable", "--now", unitName},
		{"loginctl", "enable-linger", "alice"},
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

func TestInstall_StopsAtFirstFailingCommand(t *testing.T) {
	unitDir := filepath.Join(t.TempDir(), "systemd", "user")
	f := &fakeRunner{failOn: "systemctl", failArg: "enable"}

	err := install(unitDir, "/usr/local/bin/argos", "alice", f.run)
	if err == nil {
		t.Fatalf("expected install to fail when systemctl enable fails")
	}
	for _, call := range f.calls {
		if call[0] == "loginctl" {
			t.Fatalf("expected loginctl to never run once an earlier step failed, got calls %+v", f.calls)
		}
	}
}

func TestUninstall_NotInstalledReturnsErrNotInstalled(t *testing.T) {
	unitDir := filepath.Join(t.TempDir(), "systemd", "user")
	f := &fakeRunner{}

	err := uninstall(unitDir, f.run)
	if err != ErrNotInstalled {
		t.Fatalf("expected ErrNotInstalled, got %v", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("expected no commands to run when nothing is installed, got %+v", f.calls)
	}
}

func TestUninstall_RemovesUnitAndRunsExpectedCommands(t *testing.T) {
	unitDir := filepath.Join(t.TempDir(), "systemd", "user")
	f := &fakeRunner{}
	if err := install(unitDir, "/usr/local/bin/argos", "alice", f.run); err != nil {
		t.Fatalf("install (setup): %v", err)
	}
	f.calls = nil

	if err := uninstall(unitDir, f.run); err != nil {
		t.Fatalf("uninstall: %v", err)
	}

	if _, err := os.Stat(filepath.Join(unitDir, unitName)); !os.IsNotExist(err) {
		t.Fatalf("expected unit file to be removed, stat err = %v", err)
	}

	want := [][]string{
		{"systemctl", "--user", "disable", "--now", unitName},
		{"systemctl", "--user", "daemon-reload"},
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

func TestUninstall_UnitFileKeptWhenDisableFails(t *testing.T) {
	unitDir := filepath.Join(t.TempDir(), "systemd", "user")
	setup := &fakeRunner{}
	if err := install(unitDir, "/usr/local/bin/argos", "alice", setup.run); err != nil {
		t.Fatalf("install (setup): %v", err)
	}

	f := &fakeRunner{failOn: "systemctl", failArg: "disable"}
	if err := uninstall(unitDir, f.run); err == nil {
		t.Fatalf("expected uninstall to fail when systemctl disable fails")
	}

	if _, err := os.Stat(filepath.Join(unitDir, unitName)); err != nil {
		t.Fatalf("expected unit file to remain when disable fails, stat err = %v", err)
	}
}

func TestResolveExecPath_ReturnsAbsolutePath(t *testing.T) {
	path, err := resolveExecPath()
	if err != nil {
		t.Fatalf("resolveExecPath: %v", err)
	}
	if !filepath.IsAbs(path) {
		t.Fatalf("expected an absolute path, got %q", path)
	}
}

func TestCurrentUsername_ReturnsNonEmpty(t *testing.T) {
	name, err := currentUsername()
	if err != nil {
		t.Fatalf("currentUsername: %v", err)
	}
	if name == "" {
		t.Fatalf("expected a non-empty username")
	}
}

func TestInstallUninstall_UnsupportedOSRejectedBeforeAnySideEffect(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("this guards the non-Linux short-circuit; on Linux, Install/Uninstall would shell out for real, so it's exercised via install/uninstall with a fake runner instead")
	}
	if err := Install(); err != ErrUnsupportedOS {
		t.Fatalf("expected ErrUnsupportedOS, got %v", err)
	}
	if err := Uninstall(); err != ErrUnsupportedOS {
		t.Fatalf("expected ErrUnsupportedOS, got %v", err)
	}
}

func TestUserUnitDir_EndsInSystemdUser(t *testing.T) {
	dir, err := userUnitDir()
	if err != nil {
		t.Fatalf("userUnitDir: %v", err)
	}
	if filepath.Base(dir) != "user" || filepath.Base(filepath.Dir(dir)) != "systemd" {
		t.Fatalf("expected .../systemd/user, got %q", dir)
	}
}
