//go:build darwin

package ui

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit

#import <AppKit/AppKit.h>
#include <dispatch/dispatch.h>

// integrateTitleBar merges the native title bar with the app's own dark bar:
// transparent title bar, hidden title text, and full-size content so the Gio
// surface extends underneath — leaving the traffic lights floating over the
// app bar (the standard integrated look). The window background is set to the
// bar color so resize rubber-banding never flashes white.
static void integrateTitleBar(uintptr_t viewPtr) {
	dispatch_async(dispatch_get_main_queue(), ^{
		NSView *view = (__bridge NSView *)(void *)viewPtr;
		NSWindow *w = view.window;
		if (w == nil) {
			return;
		}
		w.titlebarAppearsTransparent = YES;
		w.titleVisibility = NSWindowTitleHidden;
		w.styleMask |= NSWindowStyleMaskFullSizeContentView;
		w.backgroundColor = [NSColor colorWithSRGBRed:0x11 / 255.0
		                                        green:0x1A / 255.0
		                                         blue:0x26 / 255.0
		                                        alpha:1.0];
	});
}
*/
import "C"

import "gioui.org/app"

// nativeViewChanged applies the integrated-title-bar treatment whenever Gio
// (re)creates the native view. The title stays set for Cmd-Tab/Mission
// Control; it is only hidden in the bar itself (the app bar draws the brand).
func nativeViewChanged(e app.ViewEvent) {
	if v, ok := e.(app.AppKitViewEvent); ok && v.Valid() {
		C.integrateTitleBar(C.uintptr_t(v.View))
	}
}
