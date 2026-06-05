//go:build windows

package iperf

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
)

// The Windows build ships iperf3 inside the binary so load tests are turnkey:
// no separate install per machine. iperf3.exe is a cygwin build, so its
// cygwin1.dll dependency must travel with it.
//
//go:embed bundled/iperf3.exe bundled/cygwin1.dll
var bundledFS embed.FS

var bundledNames = []string{"iperf3.exe", "cygwin1.dll"}

// Bootstrap extracts the embedded iperf3 (and its cygwin dependency) into dir if
// not already present at the right size, then registers it as the preferred
// binary. dir should be a writable location (the agent data dir). Safe to call
// repeatedly. Returns an error only if extraction into a writable dir fails.
func Bootstrap(dir string) error {
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir for bundled iperf3: %w", err)
	}
	for _, name := range bundledNames {
		data, err := bundledFS.ReadFile("bundled/" + name)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", name, err)
		}
		dst := filepath.Join(dir, name)
		if fi, err := os.Stat(dst); err == nil && fi.Size() == int64(len(data)) {
			continue // already extracted with matching size
		}
		if err := os.WriteFile(dst, data, 0o755); err != nil {
			return fmt.Errorf("extract %s: %w", name, err)
		}
	}
	setBundled(filepath.Join(dir, "iperf3.exe"))
	return nil
}
