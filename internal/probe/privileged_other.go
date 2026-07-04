//go:build !windows && !darwin

package probe

// privilegedICMP selects raw-socket ICMP. Non-darwin unix keeps the raw-socket
// path (unprivileged ICMP on linux depends on a sysctl; out of scope).
const privilegedICMP = true
