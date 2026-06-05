//go:build !windows

package iperf

// ensureFirewallPort is a no-op on non-Windows builds.
func ensureFirewallPort(port int) {}
