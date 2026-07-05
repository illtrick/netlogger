//go:build windows

package ui

// customChrome selects the undecorated window with the hand-drawn title bar
// (drag regions + caption buttons). Windows-only; macOS uses native chrome.
const customChrome = true

// trafficLightInset is darwin-only (native buttons over the app bar).
const trafficLightInset = 0
