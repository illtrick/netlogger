//go:build windows

package probe

// privilegedICMP selects raw-socket ICMP. On Windows the app runs elevated
// (manifest) and raw sockets are the reliable path.
const privilegedICMP = true
