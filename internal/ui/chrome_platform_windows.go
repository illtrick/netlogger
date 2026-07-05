//go:build windows

package ui

// customChrome selects the undecorated window with the hand-drawn title bar
// (drag regions + caption buttons). Windows-only; macOS uses native buttons.
const customChrome = true

// nativeDecorations: false — the app bar IS the title bar.
const nativeDecorations = false

// dragRegions: the app bar's empty stretches drag the window.
const dragRegions = true

// trafficLightInset is darwin-only (native buttons over the app bar).
const trafficLightInset = 0
