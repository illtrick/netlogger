//go:build darwin

package keepawake

import (
	"log"
	"os/exec"
)

// Keeper holds a running `caffeinate -i` child that prevents idle system
// sleep while NetLogger monitors. Killed on Stop; dies with us if we crash
// (caffeinate exits when its parent's stdin closes is NOT relied on — the
// assertion simply lapses when the process is gone).
type Keeper struct {
	cmd *exec.Cmd
}

// Start launches caffeinate. A failure to start is logged, not fatal —
// monitoring works fine, the machine just may sleep.
func Start() *Keeper {
	cmd := exec.Command("/usr/bin/caffeinate", "-i")
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
