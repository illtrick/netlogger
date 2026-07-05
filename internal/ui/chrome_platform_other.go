//go:build !windows && !darwin

package ui

// Non-darwin unix keeps plain OS decorations (linux is out of scope for the
// custom chrome; see the macOS port plan).
const customChrome = false

const nativeDecorations = true

const dragRegions = false

const trafficLightInset = 0
