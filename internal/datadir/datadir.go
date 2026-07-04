// Package datadir resolves the portable data directory for the app: next to the
// exe when that location is writable, otherwise under %LOCALAPPDATA%.
package datadir

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Resolve returns the data dir to use, creating it if needed.
func Resolve() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate executable: %w", err)
	}
	dir := filepath.Dir(exe)
	return resolve(dir, fallbackBase(), preferBeside(dir), probeWritable)
}

// resolve is the injectable core: prefer <exeDir>/NetLogger-data when allowed
// and writable, else <fallbackBase>/NetLogger.
func resolve(exeDir, fallbackBase string, beside bool, writable func(string) bool) (string, error) {
	if beside {
		cand := filepath.Join(exeDir, "NetLogger-data")
		if err := os.MkdirAll(cand, 0o755); err == nil && writable(cand) {
			return cand, nil
		}
	}
	fb := filepath.Join(fallbackBase, "NetLogger")
	if err := os.MkdirAll(fb, 0o755); err != nil {
		return "", fmt.Errorf("create fallback data dir %q: %w", fb, err)
	}
	if !writable(fb) {
		return "", fmt.Errorf("no writable data dir (tried beside-exe and %q)", fb)
	}
	return fb, nil
}

// probeWritable tests writability by creating and removing a temp file.
func probeWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".wtest-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	_ = os.Remove(name)
	return true
}

// insideAppBundle reports whether dir is inside a macOS .app bundle, where
// data must never be written (breaks codesigning; lost on update).
func insideAppBundle(dir string) bool {
	return strings.Contains(filepath.ToSlash(dir), ".app/Contents/")
}
