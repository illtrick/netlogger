//go:build darwin

package iperf

// extraLookPaths are well-known install locations checked after PATH. A
// Finder-launched app inherits a minimal PATH that excludes Homebrew.
var extraLookPaths = []string{
	"/opt/homebrew/bin/iperf3", // Apple Silicon Homebrew
	"/usr/local/bin/iperf3",    // Intel Homebrew / manual installs
}
