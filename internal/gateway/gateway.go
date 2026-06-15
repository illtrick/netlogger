// Package gateway discovers the machine's default gateway (router) IP, so the
// app can probe the shared-router path without any configuration.
package gateway

import "github.com/jackpal/gateway"

// Default returns the default gateway IP as a string, or "" if it can't be
// determined.
func Default() string {
	ip, err := gateway.DiscoverGateway()
	if err != nil || ip == nil {
		return ""
	}
	return ip.String()
}
