//go:build darwin

package ui

// customChrome draws the hand-made caption buttons (Windows only). macOS
// keeps the native traffic lights, re-shown over the app bar by
// chrome_native_darwin.go after Gio's Decorated(false) hides them.
const customChrome = false

// nativeDecorations: false — the window is undecorated (full-size content);
// the app bar is the title bar, with native buttons floating over it.
const nativeDecorations = false

// dragRegions: the app bar marks window-drag regions (Gio forwards them to
// performWindowDragWithEvent, which also gives native double-click zoom).
const dragRegions = true

// trafficLightInset (dp) shifts the app bar's leading content clear of the
// native close/minimize/zoom buttons floating over it.
const trafficLightInset = 78
