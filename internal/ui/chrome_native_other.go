//go:build !darwin

package ui

import "gioui.org/app"

// nativeViewChanged / nativeConfigChanged are darwin-only (re-showing the
// traffic lights over the app bar); no-ops elsewhere.
func nativeViewChanged(app.ViewEvent) {}

func nativeConfigChanged() {}
