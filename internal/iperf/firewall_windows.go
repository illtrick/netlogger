//go:build windows

package iperf

import (
	"os/exec"
	"strconv"
)

// ensureFirewallPort best-effort opens an inbound TCP rule for the iperf3 server
// port. It only succeeds when running elevated (the NetLogger service runs as
// SYSTEM); a non-elevated interactive run can't add rules, which is a harmless
// no-op here. Delete-then-add avoids accumulating duplicate rules.
func ensureFirewallPort(port int) {
	name := "NetLogger-iperf3"
	_ = exec.Command("netsh", "advfirewall", "firewall", "delete", "rule", "name="+name).Run()
	_ = exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
		"name="+name, "dir=in", "action=allow", "protocol=TCP",
		"localport="+strconv.Itoa(port)).Run()
}
