//go:build !windows

package main

import (
	"io"
	"net/http"

	"argos/internal/config"
)

// maybeRunAsWindowsService is a no-op on non-Windows platforms — nothing
// here runs under the Windows Service Control Manager, so the normal
// foreground path in runServe always applies.
func maybeRunAsWindowsService(cfg config.Config, srv *http.Server, stdout io.Writer) (handled bool, err error) {
	return false, nil
}
