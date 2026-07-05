//go:build darwin || linux

package singleton

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Acquire takes an exclusive flock on a lock file derived from name. The lock
// is released automatically when the process exits (even if killed), matching
// the Windows named-mutex semantics. ok=false means another instance holds it.
//
// The lock name carries the UID: single-instance is per-user, matching the
// Windows Local-scope mutex. On Linux, where os.TempDir is the shared /tmp,
// this also stops another user's 0644 lock file from turning our OpenFile
// into an EACCES fail-open (or letting anyone squat the path).
func Acquire(name string) (release func(), ok bool, err error) {
	path := filepath.Join(os.TempDir(), fmt.Sprintf("%s-%d.lock", name, os.Getuid()))
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
