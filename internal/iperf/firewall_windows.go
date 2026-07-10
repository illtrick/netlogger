//go:build windows

package iperf

import (
	"strconv"

	"netlogger/internal/firewall"
)

// Both helpers verify rule HEALTH (enabled + inbound + allow + program path),
// not mere name existence, and heal drift by adding a fresh rule — never
// deleting (see internal/firewall). Best-effort; effective only when elevated.

// ensureFirewallPort keeps an inbound TCP allow open for one iperf3 server port.
func ensureFirewallPort(port int) {
	p := strconv.Itoa(port)
	firewall.EnsureInboundRule("NetLogger-iperf3-"+p, "",
		"dir=in", "action=allow", "protocol=TCP", "localport="+p, "enable=yes", "profile=any")
}

// ensureFirewallProgram keeps the exact iperf3 binary we execute (the bundle
// extracted into the data dir) allowed on every port. Interactive "Allow"
// prompts and rules from older install layouts point at OTHER paths and do
// not cover the extracted binary.
func ensureFirewallProgram(bin string) {
	if bin == "" {
		return
	}
	firewall.EnsureInboundRule("NetLogger-iperf3-bin", bin,
		"dir=in", "action=allow", "program="+bin, "enable=yes", "profile=any")
}
