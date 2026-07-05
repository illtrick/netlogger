//go:build darwin

package iperf

// extraLookPaths are well-known install locations checked after PATH. A
// Finder-launched app inherits a minimal PATH that excludes Homebrew.
var extraLookPaths = []string{
	"/opt/homebrew/bin/iperf3", // Apple Silicon Homebrew
	"/usr/local/bin/iperf3",    // Intel Homebrew / manual installs
}

// installHint tells the user how to get iperf3 on this platform. iperf3 is
// not bundled in the mac .app (and dropping a binary inside it would break
// the codesign), so Homebrew is the actionable path.
const installHint = "install it with `brew install iperf3`"
