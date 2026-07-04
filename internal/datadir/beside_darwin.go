//go:build darwin

package datadir

import "os"

// preferBeside is true for a bare binary (dev builds, bin/ copies) but false
// when running from inside a .app bundle.
func preferBeside(exeDir string) bool { return !insideAppBundle(exeDir) }

// fallbackBase returns ~/Library/Application Support (via UserConfigDir),
// falling back to the OS temp dir.
func fallbackBase() string {
	if d, err := os.UserConfigDir(); err == nil {
		return d
	}
	return os.TempDir()
}
