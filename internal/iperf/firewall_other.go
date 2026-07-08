//go:build !windows

package iperf

// ensureFirewallPort is a no-op on non-Windows builds.
func ensureFirewallPort(port int) {}

// ensureFirewallProgram is a no-op on non-Windows builds (macOS ALF prompts
// per binary on first listen; the user's one-time Allow covers all ports).
func ensureFirewallProgram(bin string) {}
