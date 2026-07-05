//go:build darwin

package ui

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit

#import <AppKit/AppKit.h>
#include <dispatch/dispatch.h>

// showStandardButtons re-shows the native close/minimize/zoom buttons that
// Gio's Decorated(false) path hides, giving the integrated look: the app's
// dark bar runs to the window's top edge with the traffic lights floating
// over it. Gio itself already applied titlebarAppearsTransparent, hidden
// title, and NSWindowStyleMaskFullSizeContentView — we deliberately do NOT
// touch the style mask (mutating it behind Gio's back makes it think the
// window switched decoration modes and it draws fallback decorations).
// The window background is set to the bar color so resize rubber-banding
// never flashes white. Idempotent; safe to re-apply.
static void showStandardButtons(uintptr_t viewPtr) {
	dispatch_async(dispatch_get_main_queue(), ^{
		NSView *view = (__bridge NSView *)(void *)viewPtr;
		NSWindow *w = view.window;
		if (w == nil) {
			return;
		}
		[w standardWindowButton:NSWindowCloseButton].hidden = NO;
		[w standardWindowButton:NSWindowMiniaturizeButton].hidden = NO;
		[w standardWindowButton:NSWindowZoomButton].hidden = NO;
		w.backgroundColor = [NSColor colorWithSRGBRed:0x11 / 255.0
		                                        green:0x1A / 255.0
		                                         blue:0x26 / 255.0
		                                        alpha:1.0];
	});
}
*/
import "C"

import "gioui.org/app"

// nativeView is the last AppKit view Gio handed us; needed to re-assert the
// standard buttons after any Configure round-trip re-hides them.
var nativeView uintptr

// nativeViewChanged re-shows the traffic lights whenever Gio (re)creates the
// native view. The window title stays set for Cmd-Tab/Mission Control; Gio
// hides it in the bar itself (the app bar draws the brand).
func nativeViewChanged(e app.ViewEvent) {
	if v, ok := e.(app.AppKitViewEvent); ok && v.Valid() {
		nativeView = v.View
		C.showStandardButtons(C.uintptr_t(nativeView))
	}
}

// nativeConfigChanged re-asserts the buttons after config changes (Gio's
// Configure re-hides standard buttons on every pass in undecorated mode).
func nativeConfigChanged() {
	if nativeView != 0 {
		C.showStandardButtons(C.uintptr_t(nativeView))
	}
}
