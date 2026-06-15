//go:build windows

package discovery

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// reuseControl sets SO_REUSEADDR before bind so multiple sockets (and multiple
// instances on one host) can share the multicast discovery port.
func reuseControl(network, address string, c syscall.RawConn) error {
	var serr error
	if err := c.Control(func(fd uintptr) {
		serr = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, windows.SO_REUSEADDR, 1)
	}); err != nil {
		return err
	}
	return serr
}
