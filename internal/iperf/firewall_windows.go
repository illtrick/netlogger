//go:build windows

package iperf

import (
	"os/exec"
	"strconv"
)

// These helpers never run `netsh delete rule`: deleting firewall rules is a
// defense-impairment pattern (MITRE T1562) that sandbox Sigma rules flag on
// otherwise-clean binaries — and the old shared-name delete-then-add once
// took the 5201 allow down mid-run. Check-then-add is just as idempotent.
// The pre-1.3.4 legacy rule ("NetLogger-iperf3") is left in place if present;
// it allows one TCP port that is closed whenever no server is listening.

// ruleExists reports whether a firewall rule with this exact name exists.
// netsh `show rule` exits non-zero when nothing matches (locale-independent).
func ruleExists(name string) bool {
	c := exec.Command("netsh", "advfirewall", "firewall", "show", "rule", "name="+name)
	hideConsole(c)
	return c.Run() == nil
}

// ensureFirewallPort best-effort opens an inbound TCP rule for one iperf3
// server port. Only effective when running elevated (the app self-elevates);
// a non-elevated run is a harmless no-op.
func ensureFirewallPort(port int) {
	name := "NetLogger-iperf3-" + strconv.Itoa(port)
	if ruleExists(name) {
		return
	}
	add := exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
		"name="+name, "dir=in", "action=allow", "protocol=TCP",
		"localport="+strconv.Itoa(port))
	hideConsole(add)
	_ = add.Run()
}

// ensureFirewallProgram best-effort allows the exact iperf3 binary we execute
// (the bundle extracted into the data dir) on every port. Interactive "Allow"
// prompts and rules from older install layouts point at OTHER paths and do
// not cover the extracted binary. If the rule exists, the program path is
// updated in place (the data dir can move between runs).
func ensureFirewallProgram(bin string) {
	if bin == "" {
		return
	}
	name := "NetLogger-iperf3-bin"
	if ruleExists(name) {
		set := exec.Command("netsh", "advfirewall", "firewall", "set", "rule",
			"name="+name, "new", "program="+bin, "enable=yes")
		hideConsole(set)
		_ = set.Run()
		return
	}
	add := exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
		"name="+name, "dir=in", "action=allow", "program="+bin)
	hideConsole(add)
	_ = add.Run()
}
