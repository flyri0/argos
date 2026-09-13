// Package tray implements Argos's desktop mode (§3.1): a system tray /
// menu bar icon that makes it clear whether Argos is running, what mode
// it's bound to, and how to stop it — used only when the server is
// started without --headless.
package tray

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	"time"

	"fyne.io/systray"

	"argos/internal/config"
)

// SetupCodeFunc reports the live §6.1 bootstrap setup code and whether
// it's still active — i.e. no device has paired yet, so the tray should
// keep displaying it. Expressed as a func type rather than importing
// internal/api's PairingHandler directly, so this package (which api never
// imports back) stays decoupled from the pairing implementation.
type SetupCodeFunc func(ctx context.Context) (code string, active bool, err error)

// App owns the HTTP server's lifecycle for as long as the tray runs, so
// it can restart the listener in-process when the user toggles bind mode
// (§3.1) and stop it cleanly on Quit.
type App struct {
	configPath string
	handler    http.Handler
	stdout     io.Writer
	setupCode  SetupCodeFunc

	mu         sync.Mutex
	cfg        config.Config
	srv        *http.Server
	actualAddr string

	statusItem    *systray.MenuItem
	modeItem      *systray.MenuItem
	lanItem       *systray.MenuItem
	loginItem     *systray.MenuItem
	setupCodeItem *systray.MenuItem

	startErr error
}

// Run starts the HTTP server, shows the tray icon, and blocks until the
// user chooses Quit (or the process receives an interrupt/termination
// signal), at which point it shuts the server down cleanly and returns.
// setupCode is used to show the §6.1 bootstrap setup code in the tray while
// it's still active; pass nil if it's not available (e.g. in tests).
func Run(cfg config.Config, configPath string, handler http.Handler, stdout io.Writer, setupCode SetupCodeFunc) error {
	app := &App{configPath: configPath, handler: handler, stdout: stdout, cfg: cfg, setupCode: setupCode}

	if err := app.startServer(); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		app.stopServer()
		systray.Quit()
	}()

	systray.Run(app.onReady, app.onExit)
	return app.startErr
}

func (app *App) onReady() {
	systray.SetIcon(icon())
	systray.SetTitle("Argos")
	systray.SetTooltip("Argos — personal budgeting server")

	app.mu.Lock()
	lanChecked := app.cfg.BindMode == config.BindModeLAN
	app.mu.Unlock()

	app.statusItem = systray.AddMenuItem("Status: Running", "")
	app.statusItem.Disable()
	app.modeItem = systray.AddMenuItem(app.modeLabel(), "")
	app.modeItem.Disable()

	if app.setupCode != nil {
		if code, active, err := app.setupCode(context.Background()); err != nil {
			fmt.Fprintf(app.stdout, "argos: checking setup code: %v\n", err)
		} else if active {
			app.setupCodeItem = systray.AddMenuItem(fmt.Sprintf("Setup code: %s", code),
				"Use this code to pair your first device (§6.1) — hidden again once a device has paired")
			app.setupCodeItem.Disable()
			go app.watchSetupCode()
		}
	}

	systray.AddSeparator()

	openItem := systray.AddMenuItem("Open in browser", "Open Argos in your browser")
	app.lanItem = systray.AddMenuItemCheckbox("Allow LAN access", "Allow other devices on your network to connect", lanChecked)

	loginEnabled, err := isAutostartEnabled()
	if err != nil {
		fmt.Fprintf(app.stdout, "argos: checking start-on-login state: %v\n", err)
	}
	app.loginItem = systray.AddMenuItemCheckbox("Start on login", "Launch Argos automatically when you log in", loginEnabled)

	systray.AddSeparator()
	quitItem := systray.AddMenuItem("Quit", "Stop Argos and quit")

	go app.handleOpenInBrowser(openItem)
	go app.handleToggleLAN()
	go app.handleToggleStartOnLogin()
	go app.handleQuit(quitItem)
}

func (app *App) onExit() {}

func (app *App) modeLabel() string {
	app.mu.Lock()
	defer app.mu.Unlock()
	mode := "localhost only"
	if app.cfg.BindMode == config.BindModeLAN {
		mode = "LAN"
	}
	return fmt.Sprintf("Mode: %s, port %d", mode, app.cfg.Port)
}

func (app *App) setStatus(text string) {
	if app.statusItem != nil {
		app.statusItem.SetTitle("Status: " + text)
	}
}

func (app *App) handleOpenInBrowser(item *systray.MenuItem) {
	for range item.ClickedCh {
		app.mu.Lock()
		port := app.cfg.Port
		app.mu.Unlock()
		url := fmt.Sprintf("http://127.0.0.1:%d", port)
		if err := openBrowser(url); err != nil {
			fmt.Fprintf(app.stdout, "argos: opening browser: %v\n", err)
		}
	}
}

func (app *App) handleToggleLAN() {
	for range app.lanItem.ClickedCh {
		app.mu.Lock()
		newMode := config.BindModeLAN
		if app.cfg.BindMode == config.BindModeLAN {
			newMode = config.BindModeLocalhost
		}
		app.cfg.BindMode = newMode
		cfg := app.cfg
		app.mu.Unlock()

		app.setStatus("Restarting…")

		if err := config.Save(app.configPath, cfg); err != nil {
			fmt.Fprintf(app.stdout, "argos: saving config: %v\n", err)
		}

		if err := app.restartServer(); err != nil {
			fmt.Fprintf(app.stdout, "argos: restarting server: %v\n", err)
			app.setStatus("Error — see logs")
			continue
		}

		if newMode == config.BindModeLAN {
			app.lanItem.Check()
		} else {
			app.lanItem.Uncheck()
		}
		app.modeItem.SetTitle(app.modeLabel())
		app.setStatus("Running")
	}
}

func (app *App) handleToggleStartOnLogin() {
	for range app.loginItem.ClickedCh {
		if app.loginItem.Checked() {
			if err := disableAutostart(); err != nil {
				fmt.Fprintf(app.stdout, "argos: disabling start on login: %v\n", err)
				continue
			}
			app.loginItem.Uncheck()
			continue
		}

		execPath, err := os.Executable()
		if err != nil {
			fmt.Fprintf(app.stdout, "argos: resolving executable path: %v\n", err)
			continue
		}
		if err := enableAutostart(execPath); err != nil {
			fmt.Fprintf(app.stdout, "argos: enabling start on login: %v\n", err)
			continue
		}
		app.loginItem.Check()
	}
}

// watchSetupCode hides setupCodeItem once bootstrap closes (§6.1: the code
// stops mattering the instant the first device pairs). Pairing happens over
// HTTP, outside this goroutine's control flow, so it polls rather than
// being notified — and stops polling as soon as it observes the closed
// state, since bootstrap never reopens for an installation.
func (app *App) watchSetupCode() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		_, active, err := app.setupCode(context.Background())
		if err != nil {
			fmt.Fprintf(app.stdout, "argos: checking setup code: %v\n", err)
			continue
		}
		if !active {
			app.setupCodeItem.Hide()
			return
		}
	}
}

func (app *App) handleQuit(item *systray.MenuItem) {
	<-item.ClickedCh
	app.stopServer()
	systray.Quit()
}

// startServer opens the listener for the current config synchronously
// (so a bind failure — e.g. the port is already in use — is reported
// immediately, not raced against a background goroutine) and then serves
// on it in the background.
func (app *App) startServer() error {
	app.mu.Lock()
	cfg := app.cfg
	app.mu.Unlock()

	addr := cfg.BindAddr()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		err = fmt.Errorf("listening on %s: %w", addr, err)
		app.startErr = err
		return err
	}

	srv := &http.Server{Handler: app.handler}
	app.mu.Lock()
	app.srv = srv
	app.actualAddr = ln.Addr().String()
	app.mu.Unlock()

	go func() {
		fmt.Fprintf(app.stdout, "argos listening on %s (bind_mode=%s)\n", addr, cfg.BindMode)
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(app.stdout, "argos: serve error: %v\n", err)
		}
	}()
	return nil
}

func (app *App) stopServer() {
	app.mu.Lock()
	srv := app.srv
	app.mu.Unlock()
	if srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
}

func (app *App) restartServer() error {
	app.stopServer()
	return app.startServer()
}

// openBrowserCommand returns the command and arguments used to open url
// in the default browser on goos. Factored out from openBrowser so the
// choice of command is testable without actually launching a browser.
func openBrowserCommand(goos, url string) (name string, args []string) {
	switch goos {
	case "windows":
		return "cmd", []string{"/c", "start", "", url}
	case "darwin":
		return "open", []string{url}
	default:
		return "xdg-open", []string{url}
	}
}

var openBrowser = func(url string) error {
	name, args := openBrowserCommand(runtime.GOOS, url)
	return exec.Command(name, args...).Start()
}
