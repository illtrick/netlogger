//go:build !windows && !darwin

package datadir

// preferBeside: non-darwin unix keeps the portable-app contract (beside the
// exe first). fallbackBase lives in fallback_unix.go.
func preferBeside(exeDir string) bool { return true }
