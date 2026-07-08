//go:build windows

// Package firewall adds a program-scoped inbound Windows Firewall allow rule for
// the running executable, so its dynamic ports (discovery, sync API, iperf) are
// reachable. Best-effort: only effective when elevated.
package firewall

import (
	"os"
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000

func hidden(c *exec.Cmd) *exec.Cmd {
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	return c
}

// ruleExists reports whether a firewall rule with this exact name exists.
// netsh `show rule` exits non-zero when nothing matches, so the exit code is
// the locale-independent signal.
func ruleExists(name string) bool {
	return hidden(exec.Command("netsh", "advfirewall", "firewall", "show", "rule", "name="+name)).Run() == nil
}

// AllowProgram ensures an inbound allow rule for this executable exists,
// updating the program path in place if the exe moved. Never deletes a rule —
// `netsh delete rule` is a defense-impairment pattern that sandboxes (and
// their Sigma rules) rightly flag; check-then-add/set is just as idempotent.
// Returns nil regardless of netsh success so callers can ignore it.
func AllowProgram(ruleName string) error {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	if ruleExists(ruleName) {
		_ = hidden(exec.Command("netsh", "advfirewall", "firewall", "set", "rule",
			"name="+ruleName, "new", "program="+exe, "enable=yes")).Run()
		return nil
	}
	_ = hidden(exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
		"name="+ruleName, "dir=in", "action=allow", "program="+exe, "enable=yes", "profile=any")).Run()
	return nil
}

// AllowPing ensures inbound ICMP echo-request allow rules exist so this
// machine answers pings from peers. The program-scoped rule does NOT cover
// ICMP because echo is handled by the kernel, not our process. Returns nil
// regardless of netsh success.
func AllowPing(ruleName string) error {
	for _, r := range []struct{ name, proto string }{
		{ruleName + " v4", "icmpv4:8,any"},
		{ruleName + " v6", "icmpv6:128,any"},
	} {
		if ruleExists(r.name) {
			continue
		}
		_ = hidden(exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
			"name="+r.name, "protocol="+r.proto, "dir=in", "action=allow", "profile=any")).Run()
	}
	return nil
}
