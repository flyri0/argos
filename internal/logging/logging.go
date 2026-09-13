// Package logging sets up Argos's on-disk log files (see project_spec.md
// "Logging"): rotating files under <data_dir>/logs, capped at a small,
// fixed number so a server that runs for months on something like a
// Raspberry Pi never accumulates logs without bound.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"path/filepath"

	"gopkg.in/natefinch/lumberjack.v2"
)

// maxLogFileSizeMB is the size threshold at which the active log file
// rotates into a timestamped backup and a fresh one begins.
const maxLogFileSizeMB = 10

// maxRetainedBackups is how many rotated backup files are kept alongside
// the current one — 4 backups + 1 active file = 5 total, the cap
// project_spec.md's "Logging" section calls for.
const maxRetainedBackups = 4

// Setup creates <dataDir>/logs (if it doesn't exist yet) and opens
// argos.log there as a rotating log file. It rotates once immediately —
// so every run starts with a clean file rather than appending into
// whatever the previous run left mid-file — and lumberjack rotates it
// again on its own whenever it exceeds maxLogFileSizeMB, always deleting
// old backups beyond maxRetainedBackups.
//
// It returns: out, a writer that duplicates everything onto both base
// (typically os.Stdout, so Argos keeps behaving like a normal foreground
// process when run attached to a terminal) and the rotating file; logger,
// a structured logger writing through out in a human-readable form (not
// JSON — these files are meant to be opened and read directly by
// whoever's debugging, not machine-parsed); and closeFn, which the caller
// must invoke on shutdown to close the file.
//
// Setup never fails outright: logging is a diagnostic aid, not something
// that should ever be able to stop Argos from actually serving a user's
// budget data. Most commonly on Windows, a previous instance that's still
// shutting down (or a second instance started by mistake) can hold
// argos.log open, which makes the eager rotation below fail with a
// sharing violation — that's noted on base as a warning rather than
// returned as an error, and Argos carries on: lumberjack still attempts
// to open or append to the file on the next real write, and if that keeps
// failing too, log lines are just silently dropped (see
// (*slog.Logger).Info's own contract) rather than crashing anything.
func Setup(dataDir string, base io.Writer) (out io.Writer, logger *slog.Logger, closeFn func() error) {
	file := &lumberjack.Logger{
		Filename:   filepath.Join(dataDir, "logs", "argos.log"),
		MaxSize:    maxLogFileSizeMB,
		MaxBackups: maxRetainedBackups,
		Compress:   false,
	}
	if err := file.Rotate(); err != nil {
		fmt.Fprintf(base, "argos: warning: could not start a fresh log file (%v) — continuing without one\n", err)
	}

	combined := io.MultiWriter(base, file)
	handler := slog.NewTextHandler(combined, &slog.HandlerOptions{Level: slog.LevelInfo})
	return combined, slog.New(handler), file.Close
}
