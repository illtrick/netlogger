// Package datadir resolves the portable data directory for the app: next to the
// exe when that location is writable, otherwise under %LOCALAPPDATA%.
package datadir

import (
	"fmt"
	"os"
	"path/filepath"
)

// Resolve returns the data dir to use, creating it if needed.
func Resolve() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate executable: %w", err)
	}
	return resolve(filepath.Dir(exe), localAppData(), probeWritable)
}

// resolve is the injectable core: prefer <exeDir>/NetLogger-data when writable,
// else <fallbackBase>/NetLogger.
func resolve(exeDir, fallbackBase string, writable func(string) bool) (string, error) {
	cand := filepath.Join(exeDir, "NetLogger-data")
	if err := os.MkdirAll(cand, 0o755); err == nil && writable(cand) {
		return cand, nil
	}
	fb := filepath.Join(fallbackBase, "NetLogger")
	if err := os.MkdirAll(fb, 0o755); err != nil {
		return "", fmt.Errorf("create fallback data dir %q: %w", fb, err)
	}
	if !writable(fb) {
		return "", fmt.Errorf("no writable data dir (tried %q and %q)", cand, fb)
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

// localAppData returns %LOCALAPPDATA%, falling back to the OS temp dir.
func localAppData() string {
	if v := os.Getenv("LOCALAPPDATA"); v != "" {
		return v
	}
	return os.TempDir()
}
