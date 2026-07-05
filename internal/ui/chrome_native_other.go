//go:build !darwin

package ui

import "gioui.org/app"

// nativeViewChanged is darwin-only (integrated title bar); no-op elsewhere.
func nativeViewChanged(app.ViewEvent) {}
