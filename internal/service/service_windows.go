//go:build windows

package service

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

func installWindowsReal() error {
	execPath, err := resolveExecPath()
	if err != nil {
		return err
	}
	m, err := connectSCM()
	if err != nil {
		return err
	}
	return installWindowsService(m, execPath)
}

func uninstallWindowsReal() error {
	m, err := connectSCM()
	if err != nil {
		return err
	}
	return uninstallWindowsService(m)
}

func connectSCM() (scm, error) {
	m, err := mgr.Connect()
	if err != nil {
		return nil, fmt.Errorf("service: connecting to the Windows Service Control Manager (try running as Administrator): %w", err)
	}
	return &realSCM{m}, nil
}

// realSCM is the real scm implementation, backed by
// golang.org/x/sys/windows/svc/mgr.
type realSCM struct{ m *mgr.Mgr }

func (r *realSCM) CreateService(name, execPath string, args []string) (scmService, error) {
	s, err := r.m.CreateService(name, execPath, mgr.Config{
		DisplayName: WindowsServiceName,
		Description: "Argos personal budgeting server",
		StartType:   mgr.StartAutomatic,
	}, args...)
	if err != nil {
		return nil, err
	}
	return &realService{s}, nil
}

func (r *realSCM) OpenService(name string) (scmService, error) {
	s, err := r.m.OpenService(name)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return nil, ErrNotInstalled
		}
		return nil, fmt.Errorf("service: opening Windows service %s: %w", name, err)
	}
	return &realService{s}, nil
}

func (r *realSCM) Disconnect() error { return r.m.Disconnect() }

type realService struct{ s *mgr.Service }

func (r *realService) Start() error { return r.s.Start() }

// Stop asks the service to stop and treats "it's already stopped" as
// success — Uninstall's goal is a stopped, absent service either way.
func (r *realService) Stop() error {
	_, err := r.s.Control(svc.Stop)
	if err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
		return err
	}
	return nil
}

func (r *realService) Delete() error { return r.s.Delete() }
func (r *realService) Close() error  { return r.s.Close() }
