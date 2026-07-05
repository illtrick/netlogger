//go:build !windows

package datadir

import "os"

// fallbackBase returns the per-user config base (~/Library/Application Support
// on darwin, XDG config dir elsewhere via UserConfigDir), falling back to the
// OS temp dir. One copy for every unix platform; Windows has its own
// %LOCALAPPDATA% version in beside_windows.go.
func fallbackBase() string {
	if d, err := os.UserConfigDir(); err == nil {
		return d
	}
	return os.TempDir()
}
