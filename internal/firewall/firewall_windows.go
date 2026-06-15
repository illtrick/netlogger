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

// AllowProgram adds (delete-then-add, idempotent) an inbound allow rule for this
// executable. Returns nil regardless of netsh success so callers can ignore it.
func AllowProgram(ruleName string) error {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	_ = hidden(exec.Command("netsh", "advfirewall", "firewall", "delete", "rule", "name="+ruleName)).Run()
	_ = hidden(exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
		"name="+ruleName, "dir=in", "action=allow", "program="+exe, "enable=yes", "profile=any")).Run()
	return nil
}

// AllowPing adds (delete-then-add, idempotent) inbound ICMP echo-request allow
// rules so this machine answers pings from peers. The program-scoped rule does
// NOT cover ICMP because echo is handled by the kernel, not our process. Returns
// nil regardless of netsh success.
func AllowPing(ruleName string) error {
	v4 := ruleName + " v4"
	v6 := ruleName + " v6"
	_ = hidden(exec.Command("netsh", "advfirewall", "firewall", "delete", "rule", "name="+v4)).Run()
	_ = hidden(exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
		"name="+v4, "protocol=icmpv4:8,any", "dir=in", "action=allow", "profile=any")).Run()
	_ = hidden(exec.Command("netsh", "advfirewall", "firewall", "delete", "rule", "name="+v6)).Run()
	_ = hidden(exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
		"name="+v6, "protocol=icmpv6:128,any", "dir=in", "action=allow", "profile=any")).Run()
	return nil
}
