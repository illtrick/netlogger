//go:build !windows

package firewall

// AllowProgram is a no-op on non-Windows builds.
func AllowProgram(ruleName string) error { return nil }

// AllowPing is a no-op on non-Windows builds.
func AllowPing(ruleName string) error { return nil }
