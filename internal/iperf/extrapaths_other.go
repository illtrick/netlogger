//go:build !darwin

package iperf

var extraLookPaths []string

// installHint tells the user how to get iperf3 on this platform.
const installHint = "bundle it next to NetLogger or install it"
