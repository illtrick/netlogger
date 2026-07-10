//go:build windows

// Package firewall keeps the inbound Windows Firewall rules this app depends
// on HEALTHY — not merely present. Rule-name existence is not health: a rule
// that exists but is disabled, edited to Block, or pointing at a stale binary
// path silently blackholes the mesh (peers see 100% loss inbound while this
// machine looks fine to itself). Pre-1.3.4 builds self-healed by
// delete-and-recreate on every launch; deleting rules trips sandbox
// defense-impairment heuristics (MITRE T1562), so instead we VERIFY the rule's
// effective properties and, on any drift, ADD a fresh correctly-specified rule
// — duplicate display names are legal, and an extra Allow can only widen
// access. Best-effort throughout: only effective when elevated.
package firewall

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

const createNoWindow = 0x08000000

func hidden(c *exec.Cmd) *exec.Cmd {
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	return c
}

// psQuote single-quotes s for a PowerShell single-quoted string literal.
func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// healthProbe builds a PowerShell snippet exiting 0 only when an ENABLED
// inbound ALLOW rule with this display name exists — and, when program is
// non-empty, at least one such rule covers that exact binary path. Cmdlet
// properties are locale-independent; netsh's printed output is not.
func healthProbe(name, program string) string {
	q := fmt.Sprintf(
		"$r=Get-NetFirewallRule -DisplayName %s -ErrorAction SilentlyContinue | Where-Object {$_.Enabled -eq 'True' -and $_.Action -eq 'Allow' -and $_.Direction -eq 'Inbound'}; if(-not $r){exit 1}",
		psQuote(name))
	if program != "" {
		q += fmt.Sprintf(
			"; $p=$r | Get-NetFirewallApplicationFilter -ErrorAction SilentlyContinue | Where-Object {$_.Program -ieq %s}; if(-not $p){exit 1}",
			psQuote(program))
	}
	return q + "; exit 0"
}

func ruleHealthy(name, program string) bool {
	return hidden(exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		healthProbe(name, program))).Run() == nil
}

// EnsureInboundRule verifies a healthy inbound allow rule named `name`
// (covering `program` when non-empty) and adds a fresh one via netsh addArgs
// when the check fails. Exported for the iperf package's server rules.
func EnsureInboundRule(name, program string, addArgs ...string) {
	if ruleHealthy(name, program) {
		return
	}
	args := append([]string{"advfirewall", "firewall", "add", "rule", "name=" + name}, addArgs...)
	_ = hidden(exec.Command("netsh", args...)).Run()
}

// AllowProgram ensures a healthy inbound allow rule for this executable.
// Returns nil regardless of netsh success so callers can ignore it.
func AllowProgram(ruleName string) error {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	EnsureInboundRule(ruleName, exe,
		"dir=in", "action=allow", "program="+exe, "enable=yes", "profile=any")
	return nil
}

// AllowPing ensures inbound ICMP echo-request allow rules so this machine
// answers pings from peers. The program-scoped rule does NOT cover ICMP
// because echo is handled by the kernel, not our process. Returns nil
// regardless of netsh success.
func AllowPing(ruleName string) error {
	EnsureInboundRule(ruleName+" v4", "",
		"protocol=icmpv4:8,any", "dir=in", "action=allow", "enable=yes", "profile=any")
	EnsureInboundRule(ruleName+" v6", "",
		"protocol=icmpv6:128,any", "dir=in", "action=allow", "enable=yes", "profile=any")
	return nil
}
