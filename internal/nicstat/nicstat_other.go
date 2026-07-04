//go:build !windows && !darwin

package nicstat

// Collect returns nil on non-Windows builds.
func Collect() []NIC { return nil }
