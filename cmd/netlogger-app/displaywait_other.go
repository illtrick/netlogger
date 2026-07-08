//go:build !darwin

package main

// waitForDisplay is darwin-only (Gio's CVDisplayLink needs an awake display
// there); other platforms create windows fine without one.
func waitForDisplay() {}

// uiPanicRecover re-raises: the relaunch dance is darwin-only.
func uiPanicRecover() {
	if r := recover(); r != nil {
		panic(r)
	}
}
