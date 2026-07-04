//go:build darwin

package probe

// privilegedICMP selects raw-socket ICMP. macOS supports unprivileged
// UDP-datagram ICMP sockets for any user, so the app never needs root.
const privilegedICMP = false
