//go:build !windows

package iperf

import "os/exec"

// hideConsole is a no-op on non-Windows builds.
func hideConsole(cmd *exec.Cmd) {}
