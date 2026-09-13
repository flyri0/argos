package logging_test

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"argos/internal/logging"
)

func TestSetup_WritesToBothBaseAndFile(t *testing.T) {
	dir := t.TempDir()
	var base strings.Builder

	_, logger, closeFn := logging.Setup(dir, &base)
	defer closeFn()

	logger.Info("hello", "key", "value")

	if !strings.Contains(base.String(), "hello") {
		t.Fatalf("expected the base writer to also receive log lines, got %q", base.String())
	}

	data, err := os.ReadFile(filepath.Join(dir, "logs", "argos.log"))
	if err != nil {
		t.Fatalf("reading log file: %v", err)
	}
	if !strings.Contains(string(data), "hello") {
		t.Fatalf("expected the log file to contain the logged line, got %q", data)
	}
}

func TestSetup_CreatesLogsDirectoryUnderDataDir(t *testing.T) {
	dir := t.TempDir()

	_, _, closeFn := logging.Setup(dir, io.Discard)
	defer closeFn()

	info, err := os.Stat(filepath.Join(dir, "logs"))
	if err != nil || !info.IsDir() {
		t.Fatalf("expected <dataDir>/logs to exist as a directory, err=%v", err)
	}
}

func TestSetup_StartsANewFileEachCall(t *testing.T) {
	dir := t.TempDir()

	_, logger1, close1 := logging.Setup(dir, io.Discard)
	logger1.Info("from first run")
	if err := close1(); err != nil {
		t.Fatalf("close1: %v", err)
	}

	_, logger2, close2 := logging.Setup(dir, io.Discard)
	defer close2()
	logger2.Info("from second run")

	entries, err := os.ReadDir(filepath.Join(dir, "logs"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) < 2 {
		t.Fatalf("expected at least 2 files after 2 separate setups (a rotated backup plus the current file), got %d: %v", len(entries), entries)
	}
}

func TestSetup_KeepsAtMostFiveLogFiles(t *testing.T) {
	dir := t.TempDir()

	for i := range 8 {
		_, logger, closeFn := logging.Setup(dir, io.Discard)
		logger.Info("run", "n", i)
		if err := closeFn(); err != nil {
			t.Fatalf("closeFn (%d): %v", i, err)
		}
	}

	// Old backups beyond MaxBackups are pruned by lumberjack in a
	// background goroutine after each rotation, so poll briefly instead
	// of asserting immediately.
	var entries []os.DirEntry
	deadline := time.Now().Add(2 * time.Second)
	for {
		var err error
		entries, err = os.ReadDir(filepath.Join(dir, "logs"))
		if err != nil {
			t.Fatalf("ReadDir: %v", err)
		}
		if len(entries) <= 5 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if len(entries) > 5 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("expected at most 5 log files retained (1 current + 4 backups), got %d: %v", len(entries), names)
	}
}

// TestSetup_DegradesGracefullyWhenTheLogFileCannotBeOpened guards the bug
// found by manually restarting a real build on Windows: a previous Argos
// process still holding argos.log open (a hung shutdown, or a second
// instance started by mistake) made the eager startup rotation fail, and
// that failure used to propagate out of Setup as an error, which
// cmd/argos treated as fatal — a logging hiccup took the whole server
// down with it. Simulated here in a way that fails on every platform, not
// just Windows' file-locking semantics: "logs" already exists as a
// regular file, so the directory Setup needs can never be created.
func TestSetup_DegradesGracefullyWhenTheLogFileCannotBeOpened(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "logs"), []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("seeding a colliding path: %v", err)
	}

	var base strings.Builder
	out, logger, closeFn := logging.Setup(dir, &base)
	defer closeFn()

	if out == nil || logger == nil {
		t.Fatalf("expected a usable writer and logger even when the log file can't be opened")
	}

	// Must not panic or block just because the underlying file write
	// keeps failing.
	logger.Info("still running despite the broken log file")

	if !strings.Contains(base.String(), "warning") {
		t.Fatalf("expected a warning about the failed log file on the base writer, got %q", base.String())
	}
	if !strings.Contains(base.String(), "still running despite the broken log file") {
		t.Fatalf("expected the actual log line to still reach the base writer, got %q", base.String())
	}
}
