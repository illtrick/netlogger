//go:build darwin

package datadir

// preferBeside is true for a bare binary (dev builds, bin/ copies) but false
// when running from inside a .app bundle. fallbackBase lives in
// fallback_unix.go (shared with the other unix platforms).
func preferBeside(exeDir string) bool { return !insideAppBundle(exeDir) }
