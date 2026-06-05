//go:build !windows

package iperf

// Bootstrap is a no-op on non-Windows builds: iperf3 is resolved from a
// co-located binary or PATH (no embedded binary ships for these platforms).
func Bootstrap(dir string) error { return nil }
