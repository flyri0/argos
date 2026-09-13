//go:build !windows

package service

// installWindowsReal/uninstallWindowsReal are unreachable in practice —
// Install/Uninstall's runtime.GOOS switch only calls them when
// GOOS=="windows" — but must exist so the package still builds on other
// OSes, where the real implementation (service_windows.go) is excluded.
func installWindowsReal() error {
	return ErrUnsupportedOS
}

func uninstallWindowsReal() error {
	return ErrUnsupportedOS
}
