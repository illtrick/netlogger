//go:build windows

package iperf

import (
	"os/exec"
	"strconv"
	"sync"
)

// portRuleName is the per-port firewall rule name. Each port gets its OWN
// rule: the old shared name ("NetLogger-iperf3") with delete-then-add meant
// opening a stress run's extra port DELETED the 5201 allow on every node —
// the always-on server went dark mid-run (inbound SYNs silently dropped).
func portRuleName(port int) string { return "NetLogger-iperf3-" + strconv.Itoa(port) }

var legacyRuleOnce sync.Once

// ensureFirewallPort best-effort opens an inbound TCP rule for one iperf3
// server port. Only effective when running elevated (the app self-elevates);
// a non-elevated run is a harmless no-op. Delete-then-add of the SAME name
// avoids duplicate rules without touching other ports' rules.
func ensureFirewallPort(port int) {
	legacyRuleOnce.Do(func() {
		// Retire the old shared-name rule; it holds whichever single port was
		// opened last and would shadow nothing but confuse audits.
		del := exec.Command("netsh", "advfirewall", "firewall", "delete", "rule", "name=NetLogger-iperf3")
		hideConsole(del)
		_ = del.Run()
	})
	name := portRuleName(port)
	del := exec.Command("netsh", "advfirewall", "firewall", "delete", "rule", "name="+name)
	hideConsole(del)
	_ = del.Run()
	add := exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
		"name="+name, "dir=in", "action=allow", "protocol=TCP",
		"localport="+strconv.Itoa(port))
	hideConsole(add)
	_ = add.Run()
}

// ensureFirewallProgram best-effort allows the exact iperf3 binary we execute
// (the bundle extracted into the data dir) on every port. Interactive "Allow"
// prompts and rules from older install layouts point at OTHER paths and do
// not cover the extracted binary, so the port rules above were load-bearing.
func ensureFirewallProgram(bin string) {
	if bin == "" {
		return
	}
	name := "NetLogger-iperf3-bin"
	del := exec.Command("netsh", "advfirewall", "firewall", "delete", "rule", "name="+name)
	hideConsole(del)
	_ = del.Run()
	add := exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
		"name="+name, "dir=in", "action=allow", "program="+bin)
	hideConsole(add)
	_ = add.Run()
}
