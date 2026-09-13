//go:build linux

package tray

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAutostartDesktopEntry_RunsExecPathWithNoArgs(t *testing.T) {
	entry := autostartDesktopEntry("/usr/local/bin/argos")
	if !strings.Contains(entry, "Exec=/usr/local/bin/argos\n") {
		t.Fatalf("expected Exec=/usr/local/bin/argos, got:\n%s", entry)
	}
	if !strings.Contains(entry, "X-GNOME-Autostart-enabled=true") {
		t.Fatalf("expected autostart to be enabled, got:\n%s", entry)
	}
}

func TestAutostartFileExists_AbsentFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "argos.desktop")
	exists, err := autostartFileExists(path)
	if err != nil {
		t.Fatalf("autostartFileExists: %v", err)
	}
	if exists {
		t.Fatalf("expected no autostart entry for a fresh temp dir")
	}
}

func TestWriteAutostartFile_ThenExists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "autostart", "argos.desktop")
	if err := writeAutostartFile(path, "/usr/local/bin/argos"); err != nil {
		t.Fatalf("writeAutostartFile: %v", err)
	}

	exists, err := autostartFileExists(path)
	if err != nil {
		t.Fatalf("autostartFileExists: %v", err)
	}
	if !exists {
		t.Fatalf("expected the autostart entry to exist after writing it")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "/usr/local/bin/argos") {
		t.Fatalf("unexpected contents:\n%s", data)
	}
}

func TestRemoveAutostartFile_RemovesExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "argos.desktop")
	if err := writeAutostartFile(path, "/usr/local/bin/argos"); err != nil {
		t.Fatalf("writeAutostartFile: %v", err)
	}
	if err := removeAutostartFile(path); err != nil {
		t.Fatalf("removeAutostartFile: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected the file to be removed, stat err = %v", err)
	}
}

func TestRemoveAutostartFile_AbsentFileIsNotAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "argos.desktop")
	if err := removeAutostartFile(path); err != nil {
		t.Fatalf("expected removing an already-absent file to be a no-op, got %v", err)
	}
}
