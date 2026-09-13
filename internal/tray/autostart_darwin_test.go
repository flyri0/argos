//go:build darwin

package tray

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeCommandRunner records every command invocation and lets a test
// fail a specific step, so enable/disableAutostartAt's orchestration can
// be verified without ever invoking launchctl for real.
type fakeCommandRunner struct {
	calls  []string
	failOn string
}

func (f *fakeCommandRunner) run(name string, args ...string) error {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	if name == f.failOn {
		return errFake
	}
	return nil
}

type fakeErr struct{ msg string }

func (e *fakeErr) Error() string { return e.msg }

var errFake = &fakeErr{"fake command failure"}

func TestLoginAgentPlist_RunsExecPathWithNoArgs(t *testing.T) {
	plist := loginAgentPlist("/Applications/Argos.app/Contents/MacOS/argos")
	if !strings.Contains(plist, "<string>/Applications/Argos.app/Contents/MacOS/argos</string>") {
		t.Fatalf("expected ProgramArguments to run the exec path, got:\n%s", plist)
	}
	if !strings.Contains(plist, "<key>RunAtLoad</key>\n\t<true/>") {
		t.Fatalf("expected RunAtLoad true, got:\n%s", plist)
	}
}

func TestEnableAutostartAt_WritesPlistAndBootstraps(t *testing.T) {
	plistPath := filepath.Join(t.TempDir(), "com.argos.tray.plist")
	f := &fakeCommandRunner{}

	if err := enableAutostartAt(plistPath, "gui/501", "/usr/local/bin/argos", f.run); err != nil {
		t.Fatalf("enableAutostartAt: %v", err)
	}

	data, err := os.ReadFile(plistPath)
	if err != nil {
		t.Fatalf("expected plist to be written: %v", err)
	}
	if !strings.Contains(string(data), "/usr/local/bin/argos") {
		t.Fatalf("unexpected plist contents:\n%s", data)
	}

	if len(f.calls) != 1 || f.calls[0] != "launchctl bootstrap gui/501 "+plistPath {
		t.Fatalf("expected a single bootstrap call, got %+v", f.calls)
	}
}

func TestDisableAutostartAt_NotInstalledIsANoOp(t *testing.T) {
	plistPath := filepath.Join(t.TempDir(), "com.argos.tray.plist")
	f := &fakeCommandRunner{}

	if err := disableAutostartAt(plistPath, "gui/501", f.run); err != nil {
		t.Fatalf("expected disabling a not-installed login item to be a no-op, got %v", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("expected no commands to run, got %+v", f.calls)
	}
}

func TestDisableAutostartAt_BootsOutAndRemovesPlist(t *testing.T) {
	plistPath := filepath.Join(t.TempDir(), "com.argos.tray.plist")
	setup := &fakeCommandRunner{}
	if err := enableAutostartAt(plistPath, "gui/501", "/usr/local/bin/argos", setup.run); err != nil {
		t.Fatalf("enableAutostartAt (setup): %v", err)
	}

	f := &fakeCommandRunner{}
	if err := disableAutostartAt(plistPath, "gui/501", f.run); err != nil {
		t.Fatalf("disableAutostartAt: %v", err)
	}

	if _, err := os.Stat(plistPath); !os.IsNotExist(err) {
		t.Fatalf("expected plist to be removed, stat err = %v", err)
	}
	if len(f.calls) != 1 || f.calls[0] != "launchctl bootout gui/501/"+autostartLabel {
		t.Fatalf("expected a single bootout call, got %+v", f.calls)
	}
}

func TestDisableAutostartAt_BootoutFailurePreventsRemoval(t *testing.T) {
	plistPath := filepath.Join(t.TempDir(), "com.argos.tray.plist")
	setup := &fakeCommandRunner{}
	if err := enableAutostartAt(plistPath, "gui/501", "/usr/local/bin/argos", setup.run); err != nil {
		t.Fatalf("enableAutostartAt (setup): %v", err)
	}

	f := &fakeCommandRunner{failOn: "launchctl"}
	if err := disableAutostartAt(plistPath, "gui/501", f.run); err == nil {
		t.Fatalf("expected bootout failure to propagate")
	}
	if _, err := os.Stat(plistPath); err != nil {
		t.Fatalf("expected plist to remain when bootout fails, stat err = %v", err)
	}
}
