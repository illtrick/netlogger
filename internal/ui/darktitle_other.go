//go:build !windows

package ui

// applyDarkTitleBar is a no-op off Windows (only DWM needs to be asked).
func applyDarkTitleBar(string) {}
