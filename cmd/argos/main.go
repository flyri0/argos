// Command argos runs the Argos server (§2.1): an HTTP API plus embedded
// PWA, in either desktop mode (with a system tray, §3.1) or headless mode
// (§3.2, for a home server/NAS/Raspberry Pi with no desktop environment).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"argos/internal/api"
	"argos/internal/config"
	"argos/internal/db"
	"argos/internal/service"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "argos:", err)
		os.Exit(1)
	}
}

// topLevelCommand splits args into an explicit top-level command ("serve"
// or "service") and its remaining arguments. Bare flags with no leading
// subcommand (e.g. "argos --headless", predating the "serve" subcommand)
// are treated as an implicit "serve" so existing invocations keep working.
func topLevelCommand(args []string) (cmd string, rest []string) {
	if len(args) > 0 && (args[0] == "serve" || args[0] == "service") {
		return args[0], args[1:]
	}
	return "serve", args
}

func run(args []string, stdout io.Writer) error {
	cmd, rest := topLevelCommand(args)
	if cmd == "service" {
		return runService(rest, stdout)
	}
	return runServe(rest, stdout)
}

// runService implements "argos service install" / "argos service
// uninstall" (§3.2): registering/removing the system service (a systemd
// user unit on Linux, a Windows Service on Windows, or a launchd daemon
// on macOS) that runs "argos serve --headless" and is enabled to start
// on boot.
func runService(args []string, stdout io.Writer) error {
	if len(args) != 1 || (args[0] != "install" && args[0] != "uninstall") {
		return fmt.Errorf("usage: argos service <install|uninstall>")
	}

	switch args[0] {
	case "install":
		if err := service.Install(); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "argos service installed and enabled to start on boot")
	case "uninstall":
		if err := service.Uninstall(); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "argos service uninstalled")
	}
	return nil
}

// flagOverrides holds the CLI-flag values for the three §3.3 config
// fields. A zero value (empty string / 0) means "not passed on the
// command line" — flags take precedence over both the config file and
// its environment variable overrides (config.Load already applied those),
// so only a flag that was actually set should override cfg here.
type flagOverrides struct {
	headless bool
	bindMode string
	port     int
	dataDir  string
}

func parseFlags(args []string) (flagOverrides, error) {
	fs := flag.NewFlagSet("argos", flag.ContinueOnError)
	var f flagOverrides
	fs.BoolVar(&f.headless, "headless", false, "run without the desktop tray icon (§3.2)")
	fs.StringVar(&f.bindMode, "bind-mode", "", `override bind_mode ("localhost" or "lan")`)
	fs.IntVar(&f.port, "port", 0, "override the listen port")
	fs.StringVar(&f.dataDir, "data-dir", "", "override the data directory")
	if err := fs.Parse(args); err != nil {
		return flagOverrides{}, err
	}
	return f, nil
}

// applyFlagOverrides returns cfg with any explicitly-passed flags applied
// on top, at the highest precedence (flags > env vars > config file >
// defaults).
func applyFlagOverrides(cfg config.Config, f flagOverrides) config.Config {
	if f.bindMode != "" {
		cfg.BindMode = config.BindMode(f.bindMode)
	}
	if f.port != 0 {
		cfg.Port = f.port
	}
	if f.dataDir != "" {
		cfg.DataDir = f.dataDir
	}
	return cfg
}

// bindAddr resolves the actual listen address for cfg. bind_mode
// "localhost" binds 127.0.0.1 only; "lan" binds 0.0.0.0 — §3.3 is explicit
// that this must never happen silently, which is why it's driven entirely
// by cfg.BindMode rather than defaulting to all-interfaces.
func bindAddr(cfg config.Config) string {
	host := "127.0.0.1"
	if cfg.BindMode == config.BindModeLAN {
		host = "0.0.0.0"
	}
	return net.JoinHostPort(host, strconv.Itoa(cfg.Port))
}

// runServe implements "argos serve" (also reachable as bare flags with no
// subcommand): it resolves the effective config and actually runs the
// HTTP server, per §3.2/§3.3.
func runServe(args []string, stdout io.Writer) error {
	f, err := parseFlags(args)
	if err != nil {
		return err
	}

	path, err := config.DefaultPath()
	if err != nil {
		return err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	cfg = applyFlagOverrides(cfg, f)
	if err := cfg.Validate(); err != nil {
		return err
	}

	if !f.headless {
		// Desktop mode (§3.1) will initialize the system tray here once
		// internal/tray exists. It doesn't yet, so desktop mode currently
		// runs identically to headless mode — nothing calls tray code
		// either way.
	}

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return fmt.Errorf("creating data dir %s: %w", cfg.DataDir, err)
	}
	dbPath := filepath.Join(cfg.DataDir, "argos.db")
	if err := db.Migrate(dbPath); err != nil {
		return fmt.Errorf("migrating database: %w", err)
	}
	conn, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer conn.Close()

	router, err := api.NewRouter(conn)
	if err != nil {
		return fmt.Errorf("building router: %w", err)
	}

	addr := bindAddr(cfg)
	srv := &http.Server{Addr: addr, Handler: router}

	handledByWindowsSCM, err := maybeRunAsWindowsService(cfg, srv, stdout)
	if err != nil {
		return err
	}
	if handledByWindowsSCM {
		return nil
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		fmt.Fprintf(stdout, "argos listening on %s (bind_mode=%s)\n", addr, cfg.BindMode)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serving: %w", err)
		}
		return nil
	case <-ctx.Done():
		stop()
		return srv.Shutdown(context.Background())
	}
}
