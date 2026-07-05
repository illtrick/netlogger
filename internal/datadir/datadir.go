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
	cand := ""
	if beside {
		cand = filepath.Join(exeDir, "NetLogger-data")
		if err := os.MkdirAll(cand, 0o755); err == nil && writable(cand) {
			return cand, nil
		}
	}
	fb := filepath.Join(fallbackBase, "NetLogger")
	if err := os.MkdirAll(fb, 0o755); err != nil {
		return "", fmt.Errorf("create fallback data dir %s: %w", fb, err)
	}
	if !writable(fb) {
		// Use %s, not %q: on Windows %q backslash-escapes the path
		// ("C:\\Users\\...") — ugly in the ticket line and it breaks substring
		// checks (and the test) against the real path.
		if cand != "" {
			return "", fmt.Errorf("no writable data dir (tried %s and %s)", cand, fb)
		}
		return "", fmt.Errorf("no writable data dir (beside-exe disabled; tried %s)", fb)
	}
	return fb, nil
}

// SidecarDir returns where user-facing sidecar files (netlogger.log, export
// bundles) belong: beside the executable on portable platforms — the
// everything-in-one-folder contract — but the data dir when the exe location
// must not be written (inside a macOS .app bundle, writing would break the
// codesign seal).
func SidecarDir(dataDir string) string {
	exe, err := os.Executable()
	if err != nil {
		return dataDir
	}
	return sidecarDir(filepath.Dir(exe), dataDir)
}

// sidecarDir is the injectable core of SidecarDir.
func sidecarDir(exeDir, dataDir string) string {
	if preferBeside(exeDir) {
		return exeDir
	}
	return dataDir
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

// insideAppBundle reports whether dir is a macOS .app bundle's executable
// directory (<Name>.app/Contents/MacOS), where data must never be written
// (breaks codesigning; lost on update). It checks that exact trailing
// structure — a bundled app's exe always lives there — rather than a
// substring, so a path that merely contains ".app/Contents/" (e.g. a dev
// tree under ~/Projects/MyApp.app/Contents/tools) keeps its beside-exe dir.
func insideAppBundle(dir string) bool {
	p := strings.TrimRight(filepath.ToSlash(dir), "/")
	parts := strings.Split(p, "/")
	n := len(parts)
	return n >= 3 &&
		parts[n-1] == "MacOS" &&
		parts[n-2] == "Contents" &&
		strings.HasSuffix(parts[n-3], ".app")
}
