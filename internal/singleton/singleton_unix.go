//go:build darwin || linux

package singleton

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Acquire takes an exclusive flock on a lock file derived from name. The lock
// is released automatically when the process exits (even if killed), matching
// the Windows named-mutex semantics. ok=false means another instance holds it.
func Acquire(name string) (release func(), ok bool, err error) {
	path := filepath.Join(os.TempDir(), name+".lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return func() {}, true, err // fail open, like the Windows path logs-and-continues
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if err == unix.EWOULDBLOCK {
			return func() {}, false, nil
		}
		return func() {}, true, err
	}
	return func() {
		unix.Flock(int(f.Fd()), unix.LOCK_UN)
		f.Close()
	}, true, nil
}
