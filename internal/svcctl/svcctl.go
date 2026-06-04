// Package svcctl runs elevated service-control actions (install/start/stop/
// uninstall) so the operator can manage the Windows Service from the GUI via a
// UAC prompt, and reports the service state. Windows-first; other OSes report
// unsupported for now.
package svcctl

import (
	"os/exec"
	"runtime"
	"strings"
)

const serviceName = "NetLogger"

// Supported reports whether elevated service control is available on this OS.
func Supported() bool { return runtime.GOOS == "windows" }

// ValidAction reports whether a is an allowed control action.
func ValidAction(a string) bool {
	switch a {
	case "install", "start", "stop", "uninstall":
		return true
	}
	return false
}

// parseState maps `sc query` output/err to a friendly state string.
func parseState(out string, runErr error) string {
	low := strings.ToLower(out)
	if strings.Contains(low, "1060") || strings.Contains(low, "does not exist") {
		return "not installed"
	}
	up := strings.ToUpper(out)
	switch {
	case strings.Contains(up, "RUNNING"):
		return "running"
	case strings.Contains(up, "STOPPED"):
		return "stopped"
	case strings.Contains(up, "START_PENDING"):
		return "starting"
	case strings.Contains(up, "STOP_PENDING"):
		return "stopping"
	}
	if runErr != nil {
		return "not installed"
	}
	return "unknown"
}

// Status returns the service state ("running"/"stopped"/"not installed"/…).
func Status() string {
	if runtime.GOOS != "windows" {
		return "unsupported"
	}
	out, err := exec.Command("sc", "query", serviceName).CombinedOutput()
	return parseState(string(out), err)
}

// RunElevated launches exe with args under a UAC prompt (Windows). The call
// returns once the prompt is raised; the elevated child performs the action.
func RunElevated(exe string, args []string) error {
	if runtime.GOOS != "windows" {
		return errUnsupported
	}
	// Build: Start-Process -FilePath 'exe' -Verb RunAs -ArgumentList 'a','b',...
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = "'" + strings.ReplaceAll(a, "'", "''") + "'"
	}
	ps := "Start-Process -FilePath '" + strings.ReplaceAll(exe, "'", "''") + "' -Verb RunAs"
	if len(quoted) > 0 {
		ps += " -ArgumentList " + strings.Join(quoted, ",")
	}
	return exec.Command("powershell", "-NoProfile", "-Command", ps).Start()
}

type sentinel string

func (s sentinel) Error() string { return string(s) }

const errUnsupported = sentinel("elevated service control is only supported on Windows")
