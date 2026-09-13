//go:build windows

package tray

import (
	"testing"

	"golang.org/x/sys/windows/registry"
)

// fakeRunKey lets isAutostartEnabledIn/enableAutostartIn/disableAutostartIn
// be verified without ever touching the real Windows registry.
type fakeRunKey struct {
	values map[string]string
	closed bool
}

func newFakeRunKey() *fakeRunKey {
	return &fakeRunKey{values: make(map[string]string)}
}

func (f *fakeRunKey) GetStringValue(name string) (string, uint32, error) {
	v, ok := f.values[name]
	if !ok {
		return "", 0, registry.ErrNotExist
	}
	return v, 1, nil
}

func (f *fakeRunKey) SetStringValue(name, value string) error {
	f.values[name] = value
	return nil
}

func (f *fakeRunKey) DeleteValue(name string) error {
	if _, ok := f.values[name]; !ok {
		return registry.ErrNotExist
	}
	delete(f.values, name)
	return nil
}

func (f *fakeRunKey) Close() error {
	f.closed = true
	return nil
}

func TestIsAutostartEnabledIn_AbsentValue(t *testing.T) {
	k := newFakeRunKey()
	enabled, err := isAutostartEnabledIn(k)
	if err != nil {
		t.Fatalf("isAutostartEnabledIn: %v", err)
	}
	if enabled {
		t.Fatalf("expected disabled when no Run value exists")
	}
}

func TestEnableAutostartIn_SetsQuotedPath(t *testing.T) {
	k := newFakeRunKey()
	if err := enableAutostartIn(k, `C:\Program Files\Argos\argos.exe`); err != nil {
		t.Fatalf("enableAutostartIn: %v", err)
	}
	if k.values[autostartValueName] != `"C:\Program Files\Argos\argos.exe"` {
		t.Fatalf("unexpected Run value: %q", k.values[autostartValueName])
	}

	enabled, err := isAutostartEnabledIn(k)
	if err != nil {
		t.Fatalf("isAutostartEnabledIn: %v", err)
	}
	if !enabled {
		t.Fatalf("expected enabled after enableAutostartIn")
	}
}

func TestDisableAutostartIn_RemovesValue(t *testing.T) {
	k := newFakeRunKey()
	if err := enableAutostartIn(k, "argos.exe"); err != nil {
		t.Fatalf("enableAutostartIn: %v", err)
	}
	if err := disableAutostartIn(k); err != nil {
		t.Fatalf("disableAutostartIn: %v", err)
	}

	enabled, err := isAutostartEnabledIn(k)
	if err != nil {
		t.Fatalf("isAutostartEnabledIn: %v", err)
	}
	if enabled {
		t.Fatalf("expected disabled after disableAutostartIn")
	}
}

func TestDisableAutostartIn_AbsentValueIsNotAnError(t *testing.T) {
	k := newFakeRunKey()
	if err := disableAutostartIn(k); err != nil {
		t.Fatalf("expected disabling an already-absent value to be a no-op, got %v", err)
	}
}
