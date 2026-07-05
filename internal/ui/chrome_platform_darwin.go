//go:build darwin

package ui

// customChrome selects the undecorated window with the hand-drawn title bar.
// macOS keeps native chrome, integrated: the title bar is transparent and the
// app bar extends underneath the traffic lights (chrome_native_darwin.go).
const customChrome = false

// trafficLightInset (dp) shifts the app bar's leading content clear of the
// native close/minimize/zoom buttons floating over it.
const trafficLightInset = 78
