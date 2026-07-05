//go:build !windows

package discovery

import (
	"syscall"

	"golang.org/x/sys/unix"
)

func reuseControl(network, address string, c syscall.RawConn) error {
	var serr error
	if err := c.Control(func(fd uintptr) {
		serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
		if serr == nil {
			// BSD (and Linux) require REUSEPORT — not just REUSEADDR — for a
			// second wildcard bind, i.e. two instances sharing the port.
			serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
		}
		if serr == nil {
			// Announces also go to subnet broadcast (multicast-hostile APs).
			serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_BROADCAST, 1)
		}
	}); err != nil {
		return err
	}
	return serr
}
