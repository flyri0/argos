//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"golang.org/x/sys/windows/svc"

	"argos/internal/config"
	"argos/internal/service"
)

// maybeRunAsWindowsService reports whether this process was launched by
// the Windows Service Control Manager (SCM) and, if so, runs srv under
// the SCM's control protocol instead of the normal foreground path — the
// SCM requires the process to call StartServiceCtrlDispatcher promptly or
// it kills the process as unresponsive, which svc.Run does here on our
// behalf. A plain "argos serve --headless" run from a terminal is not
// under the SCM, so it falls through unchanged to the normal path.
func maybeRunAsWindowsService(cfg config.Config, srv *http.Server, stdout io.Writer) (handled bool, err error) {
	isService, err := svc.IsWindowsService()
	if err != nil {
		return false, fmt.Errorf("checking Windows service context: %w", err)
	}
	if !isService {
		return false, nil
	}

	h := &windowsServiceHandler{srv: srv, stdout: stdout, bindMode: string(cfg.BindMode)}
	if err := svc.Run(service.WindowsServiceName, h); err != nil {
		return true, fmt.Errorf("running as Windows service: %w", err)
	}
	return true, nil
}

// windowsServiceHandler implements svc.Handler: it starts srv in the
// background, reports Running once it's up, and on a Stop/Shutdown
// control request gracefully shuts srv down before reporting Stopped.
type windowsServiceHandler struct {
	srv      *http.Server
	stdout   io.Writer
	bindMode string
}

func (h *windowsServiceHandler) Execute(args []string, r <-chan svc.ChangeRequest, s chan<- svc.Status) (svcSpecificEC bool, exitCode uint32) {
	s <- svc.Status{State: svc.StartPending}

	errCh := make(chan error, 1)
	go func() {
		fmt.Fprintf(h.stdout, "argos listening on %s (bind_mode=%s)\n", h.srv.Addr, h.bindMode)
		errCh <- h.srv.ListenAndServe()
	}()

	s <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	for {
		select {
		case err := <-errCh:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				return false, 1
			}
			return false, 0
		case req := <-r:
			switch req.Cmd {
			case svc.Interrogate:
				s <- req.CurrentStatus
			case svc.Stop, svc.Shutdown:
				s <- svc.Status{State: svc.StopPending}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				h.srv.Shutdown(ctx)
				cancel()
				s <- svc.Status{State: svc.Stopped}
				return false, 0
			}
		}
	}
}
