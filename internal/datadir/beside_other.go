//go:build !windows && !darwin

package datadir

import "os"

func preferBeside(exeDir string) bool { return true }

func fallbackBase() string {
	if d, err := os.UserConfigDir(); err == nil {
		return d
	}
	return os.TempDir()
}
