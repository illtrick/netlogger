// Package sysinfo holds device-agnostic self-checks an agent reports about
// itself (iperf3 presence, data-dir writability). No vendor-specific logic.
package sysinfo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Iperf3Version returns the installed iperf3 version string, or "" if absent.
func Iperf3Version() string { return detectVersion("iperf3", "--version") }

// detectVersion runs `bin arg` and returns the first line of output trimmed,
// or "" if the binary is missing or produces no output.
func detectVersion(bin, arg string) string {
	path, err := exec.LookPath(bin)
	if err != nil {
		return ""
	}
	out, err := exec.Command(path, arg).CombinedOutput()
	if err != nil && len(out) == 0 {
		return ""
	}
	line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	return line
}

// DataDirWritable reports whether a temp file can be created in dir.
func DataDirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".netlogger-write-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	_ = os.Remove(filepath.Clean(name))
	return true
}
