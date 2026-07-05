//go:build darwin

package keepawake

import (
	"log"
	"os"
	"os/exec"
	"strconv"
)

// Keeper holds a running `caffeinate -i -w <our pid>` child that prevents
// idle system sleep while NetLogger monitors. Killed on Stop (settings
// toggle); -w makes caffeinate exit on its own when our process dies by ANY
// path — including Cmd+Q/AppleEvent termination, which Cocoa performs
// without unwinding Go defers, and outright crashes — so the child can
// never outlive the app and hold the Mac awake forever.
type Keeper struct {
	cmd *exec.Cmd
}

// Start launches caffeinate. A failure to start is logged, not fatal —
// monitoring works fine, the machine just may sleep.
func Start() *Keeper {
	cmd := exec.Command("/usr/bin/caffeinate", "-i", "-w", strconv.Itoa(os.Getpid()))
	if err := cmd.Start(); err != nil {
		log.Printf("keepawake: caffeinate failed to start: %v", err)
		return &Keeper{}
	}
	return &Keeper{cmd: cmd}
}

// Stop kills the caffeinate child, releasing the sleep assertion.
func (k *Keeper) Stop() {
	if k.cmd != nil && k.cmd.Process != nil {
		_ = k.cmd.Process.Kill()
		_, _ = k.cmd.Process.Wait()
	}
}
