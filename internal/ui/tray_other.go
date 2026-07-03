//go:build !windows

package ui

// Tray mode is Windows-only; elsewhere these are no-ops.
func startTray(string) (stop func()) { return func() {} }
func hideMainWindow(string)          {}
func showMainWindow(string)          {}
