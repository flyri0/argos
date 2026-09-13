package service

import (
	"errors"
	"strings"
	"testing"
)

// fakeSCM and fakeSCMService let install/uninstallWindowsService's
// orchestration be verified without ever opening a real handle to the
// Windows Service Control Manager, on any OS.
type fakeSCM struct {
	calls       []string
	existing    *fakeSCMService // returned by OpenService, or nil for "not installed"
	createErr   error
	disconnects int
}

func (f *fakeSCM) CreateService(name, execPath string, args []string) (scmService, error) {
	f.calls = append(f.calls, "create:"+name+":"+execPath+":"+strings.Join(args, " "))
	if f.createErr != nil {
		return nil, f.createErr
	}
	svc := &fakeSCMService{}
	f.existing = svc
	return svc, nil
}

func (f *fakeSCM) OpenService(name string) (scmService, error) {
	f.calls = append(f.calls, "open:"+name)
	if f.existing == nil {
		return nil, ErrNotInstalled
	}
	return f.existing, nil
}

func (f *fakeSCM) Disconnect() error {
	f.disconnects++
	return nil
}

type fakeSCMService struct {
	calls    []string
	startErr error
	stopErr  error
	delErr   error
}

func (s *fakeSCMService) Start() error {
	s.calls = append(s.calls, "start")
	return s.startErr
}
func (s *fakeSCMService) Stop() error {
	s.calls = append(s.calls, "stop")
	return s.stopErr
}
func (s *fakeSCMService) Delete() error {
	s.calls = append(s.calls, "delete")
	return s.delErr
}
func (s *fakeSCMService) Close() error {
	s.calls = append(s.calls, "close")
	return nil
}

func TestInstallWindowsService_CreatesAndStartsWithHeadlessArgs(t *testing.T) {
	m := &fakeSCM{}

	if err := installWindowsService(m, `C:\Program Files\Argos\argos.exe`); err != nil {
		t.Fatalf("installWindowsService: %v", err)
	}

	if len(m.calls) != 1 || m.calls[0] != `create:Argos:C:\Program Files\Argos\argos.exe:serve --headless` {
		t.Fatalf("expected a single create call for Argos with \"serve --headless\", got %+v", m.calls)
	}
	if m.existing == nil || len(m.existing.calls) != 2 || m.existing.calls[0] != "start" || m.existing.calls[1] != "close" {
		t.Fatalf("expected the created service to be started then closed, got %+v", m.existing)
	}
	if m.disconnects != 1 {
		t.Fatalf("expected the SCM connection to be disconnected exactly once, got %d", m.disconnects)
	}
}

func TestInstallWindowsService_CreateFailurePropagatesAndStillDisconnects(t *testing.T) {
	m := &fakeSCM{createErr: errors.New("access denied")}

	if err := installWindowsService(m, "argos.exe"); err == nil {
		t.Fatalf("expected create failure to propagate")
	}
	if m.disconnects != 1 {
		t.Fatalf("expected disconnect even on failure, got %d", m.disconnects)
	}
}

func TestInstallWindowsService_StartFailurePropagates(t *testing.T) {
	svc := &fakeSCMService{startErr: errors.New("start failed")}
	m := &fixedServiceSCM{svc: svc}

	if err := installWindowsService(m, "argos.exe"); err == nil {
		t.Fatalf("expected start failure to propagate")
	}
}

// fixedServiceSCM always returns a pre-configured service from
// CreateService/OpenService, for tests that need to control the
// returned service's behavior directly (e.g. a failing Start).
type fixedServiceSCM struct {
	svc         *fakeSCMService
	disconnects int
}

func (f *fixedServiceSCM) CreateService(name, execPath string, args []string) (scmService, error) {
	return f.svc, nil
}
func (f *fixedServiceSCM) OpenService(name string) (scmService, error) { return f.svc, nil }
func (f *fixedServiceSCM) Disconnect() error                           { f.disconnects++; return nil }

func TestUninstallWindowsService_NotInstalledReturnsErrNotInstalled(t *testing.T) {
	m := &fakeSCM{}

	err := uninstallWindowsService(m)
	if err != ErrNotInstalled {
		t.Fatalf("expected ErrNotInstalled, got %v", err)
	}
	if m.disconnects != 1 {
		t.Fatalf("expected disconnect even when not installed, got %d", m.disconnects)
	}
}

func TestUninstallWindowsService_StopsThenDeletes(t *testing.T) {
	svc := &fakeSCMService{}
	m := &fakeSCM{existing: svc}

	if err := uninstallWindowsService(m); err != nil {
		t.Fatalf("uninstallWindowsService: %v", err)
	}

	want := []string{"stop", "delete", "close"}
	if len(svc.calls) != len(want) {
		t.Fatalf("expected calls %v, got %v", want, svc.calls)
	}
	for i, c := range want {
		if svc.calls[i] != c {
			t.Fatalf("call %d: expected %q, got %q (%v)", i, c, svc.calls[i], svc.calls)
		}
	}
}

func TestUninstallWindowsService_StopFailurePreventsDelete(t *testing.T) {
	svc := &fakeSCMService{stopErr: errors.New("stop failed")}
	m := &fakeSCM{existing: svc}

	if err := uninstallWindowsService(m); err == nil {
		t.Fatalf("expected stop failure to propagate")
	}
	for _, c := range svc.calls {
		if c == "delete" {
			t.Fatalf("expected delete to never run when stop fails, got calls %v", svc.calls)
		}
	}
}
