//go:build !windows && !darwin && !linux

package tray

import "errors"

// errAutostartUnsupported is returned on any OS with no autostart
// implementation yet.
var errAutostartUnsupported = errors.New("tray: start-on-login is not yet supported on this OS")

func isAutostartEnabled() (bool, error) {
	return false, nil
}

func enableAutostart(execPath string) error {
	return errAutostartUnsupported
}

func disableAutostart() error {
	return errAutostartUnsupported
}
