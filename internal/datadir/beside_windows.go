//go:build windows

package datadir

import "os"

// preferBeside reports whether the beside-the-exe data dir should be tried
// first. Always true on Windows (the portable-app contract).
func preferBeside(exeDir string) bool { return true }

// fallbackBase returns %LOCALAPPDATA%, falling back to the OS temp dir.
func fallbackBase() string {
	if v := os.Getenv("LOCALAPPDATA"); v != "" {
		return v
	}
	return os.TempDir()
}
