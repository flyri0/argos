//go:build windows

package tray

import (
	"fmt"

	"golang.org/x/sys/windows/registry"
)

// autostartValueName is the value name under the per-user Run key that
// Windows checks at login.
const autostartValueName = "Argos"

// runKey abstracts the subset of golang.org/x/sys/windows/registry this
// package needs, so autostart's logic can be tested without touching the
// real Windows registry.
type runKey interface {
	GetStringValue(name string) (string, uint32, error)
	SetStringValue(name, value string) error
	DeleteValue(name string) error
	Close() error
}

var openRunKey = func() (runKey, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return nil, fmt.Errorf("tray: opening the Run registry key: %w", err)
	}
	return k, nil
}

func isAutostartEnabled() (bool, error) {
	k, err := openRunKey()
	if err != nil {
		return false, err
	}
	defer k.Close()
	return isAutostartEnabledIn(k)
}

func enableAutostart(execPath string) error {
	k, err := openRunKey()
	if err != nil {
		return err
	}
	defer k.Close()
	return enableAutostartIn(k, execPath)
}

func disableAutostart() error {
	k, err := openRunKey()
	if err != nil {
		return err
	}
	defer k.Close()
	return disableAutostartIn(k)
}

func isAutostartEnabledIn(k runKey) (bool, error) {
	_, _, err := k.GetStringValue(autostartValueName)
	if err != nil {
		if err == registry.ErrNotExist {
			return false, nil
		}
		return false, fmt.Errorf("tray: reading Run value: %w", err)
	}
	return true, nil
}

func enableAutostartIn(k runKey, execPath string) error {
	if err := k.SetStringValue(autostartValueName, `"`+execPath+`"`); err != nil {
		return fmt.Errorf("tray: writing Run value: %w", err)
	}
	return nil
}

func disableAutostartIn(k runKey) error {
	if err := k.DeleteValue(autostartValueName); err != nil {
		if err == registry.ErrNotExist {
			return nil
		}
		return fmt.Errorf("tray: deleting Run value: %w", err)
	}
	return nil
}
