package service

import "fmt"

// WindowsServiceName is both the Windows service's name and its display
// name — shared with cmd/argos, which must register the exact same name
// with svc.Run when it detects it's running under the Service Control
// Manager (SCM).
const WindowsServiceName = "Argos"

// scm abstracts the subset of golang.org/x/sys/windows/svc/mgr this
// package needs, so install/uninstall's orchestration can be verified
// without opening a real handle to the Windows Service Control Manager.
// The real implementation lives in service_windows.go (Windows-only,
// since the underlying package only builds for GOOS=windows); the
// interface itself carries no such restriction, so it can be exercised
// from ordinary tests on any platform.
type scm interface {
	// CreateService registers a new service named name whose command
	// line is execPath followed by args, then closes over the SCM
	// connection until Disconnect is called.
	CreateService(name, execPath string, args []string) (scmService, error)
	// OpenService looks up an existing service by name. It returns
	// ErrNotInstalled if no such service is registered.
	OpenService(name string) (scmService, error)
	Disconnect() error
}

// scmService is a handle to one registered Windows service.
type scmService interface {
	// Start starts the service now (Install calls this so the server is
	// actually running immediately after installation, not just enabled
	// for the next boot).
	Start() error
	// Stop stops the service if it's running; it must return nil if the
	// service ends up stopped, including if it was already stopped.
	Stop() error
	Delete() error
	Close() error
}

// installWindowsService registers execPath as the Windows service
// WindowsServiceName, running "<execPath> serve --headless" (this
// milestone's required command), set to start automatically at boot,
// then starts it immediately.
func installWindowsService(m scm, execPath string) error {
	defer m.Disconnect()

	s, err := m.CreateService(WindowsServiceName, execPath, []string{"serve", "--headless"})
	if err != nil {
		return fmt.Errorf("service: creating Windows service %s: %w", WindowsServiceName, err)
	}
	defer s.Close()

	if err := s.Start(); err != nil {
		return fmt.Errorf("service: starting Windows service %s: %w", WindowsServiceName, err)
	}
	return nil
}

// uninstallWindowsService stops and deletes the Windows service, or
// returns ErrNotInstalled if it isn't registered.
func uninstallWindowsService(m scm) error {
	defer m.Disconnect()

	s, err := m.OpenService(WindowsServiceName)
	if err != nil {
		return err
	}
	defer s.Close()

	if err := s.Stop(); err != nil {
		return fmt.Errorf("service: stopping Windows service %s: %w", WindowsServiceName, err)
	}
	if err := s.Delete(); err != nil {
		return fmt.Errorf("service: deleting Windows service %s: %w", WindowsServiceName, err)
	}
	return nil
}
