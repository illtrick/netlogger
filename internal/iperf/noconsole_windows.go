//go:build windows

package iperf

import (
	"os/exec"
	"syscall"
)

// createNoWindow is the Windows CREATE_NO_WINDOW process-creation flag; it keeps
// a console child (iperf3, netsh) from flashing a terminal window when spawned
// by the GUI app.
const createNoWindow = 0x08000000

// hideConsole configures cmd so its child process runs without a console window.
func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
