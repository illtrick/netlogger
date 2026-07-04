//go:build !windows && !darwin && !linux

package singleton

// Acquire is a no-op on non-Windows builds (always succeeds).
func Acquire(name string) (release func(), ok bool, err error) {
	return func() {}, true, nil
}
