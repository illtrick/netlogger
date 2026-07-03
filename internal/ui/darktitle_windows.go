//go:build windows

package ui

import (
	"os"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32                    = syscall.NewLazyDLL("user32.dll")
	dwmapi                    = syscall.NewLazyDLL("dwmapi.dll")
	shell32                   = syscall.NewLazyDLL("shell32.dll")
	procFindWindowW           = user32.NewProc("FindWindowW")
	procSendMessageW          = user32.NewProc("SendMessageW")
	procSetWindowLongPtrW     = user32.NewProc("SetWindowLongPtrW")
	procCallWindowProcW       = user32.NewProc("CallWindowProcW")
	procDwmSetWindowAttribute = dwmapi.NewProc("DwmSetWindowAttribute")
	procExtractIconExW        = shell32.NewProc("ExtractIconExW")
)

// Close-to-tray: the main window's WndProc is subclassed so the title-bar X
// hides the window (monitoring continues; the tray icon reopens it) instead of
// quitting. Quit (tray menu) sets allowClose first so the close proceeds.
var (
	origWndProc uintptr
	allowClose  int32
)

// allowAppClose lets the next WM_CLOSE actually destroy the window (Quit path).
func allowAppClose() { atomic.StoreInt32(&allowClose, 1) }

// applyDarkTitleBar makes the native chrome match the app: it asks DWM to render
// the title bar dark and hands the window the icon embedded in the exe (Gio
// registers its window class with the stock icon, so title bar and taskbar show
// a generic one otherwise). Gio doesn't expose the HWND, so the window is
// located by its title once it exists (retried briefly — creation is async).
func applyDarkTitleBar(title string) {
	go func() {
		t, err := syscall.UTF16PtrFromString(title)
		if err != nil {
			return
		}
		const (
			dwmwaUseImmersiveDarkMode = 20 // Windows 10 2004+ / Windows 11
			dwmwaWindowCornerPref     = 33 // Windows 11
			dwmwcpRound               = 2
		)
		for i := 0; i < 50; i++ {
			hwnd, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(t)))
			if hwnd != 0 {
				dark := int32(1)
				_, _, _ = procDwmSetWindowAttribute.Call(hwnd, dwmwaUseImmersiveDarkMode,
					uintptr(unsafe.Pointer(&dark)), unsafe.Sizeof(dark))
				// The window is undecorated (the app draws its own title bar);
				// ask DWM for Win11 rounded corners + shadow so it still looks native.
				corner := int32(dwmwcpRound)
				_, _, _ = procDwmSetWindowAttribute.Call(hwnd, dwmwaWindowCornerPref,
					uintptr(unsafe.Pointer(&corner)), unsafe.Sizeof(corner))
				setWindowIcon(hwnd)
				installCloseToTray(hwnd)
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()
}

// installCloseToTray subclasses the window so the title-bar X hides it to the
// tray instead of quitting. The subclass proc runs on the window's own message
// thread; everything except the intercepted WM_CLOSE forwards to Gio's WndProc.
func installCloseToTray(hwnd uintptr) {
	const (
		gwlpWndProc = ^uintptr(3) // GWLP_WNDPROC (-4)
		wmCloseMsg  = 0x0010
		swHideCmd   = 0
	)
	sub := syscall.NewCallback(func(h, msg, wp, lp uintptr) uintptr {
		if msg == wmCloseMsg && atomic.LoadInt32(&allowClose) == 0 {
			procShowWindow.Call(h, swHideCmd) // own thread — safe, no deadlock
			return 0
		}
		r, _, _ := procCallWindowProcW.Call(origWndProc, h, msg, wp, lp)
		return r
	})
	origWndProc, _, _ = procSetWindowLongPtrW.Call(hwnd, gwlpWndProc, sub)
}

// setWindowIcon extracts the first icon from this exe's resources and assigns it
// to the window (big + small), which the title bar and taskbar both follow.
func setWindowIcon(hwnd uintptr) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	p, err := syscall.UTF16PtrFromString(exe)
	if err != nil {
		return
	}
	var big, small uintptr
	n, _, _ := procExtractIconExW.Call(uintptr(unsafe.Pointer(p)), 0,
		uintptr(unsafe.Pointer(&big)), uintptr(unsafe.Pointer(&small)), 1)
	if n == 0 {
		return // no icon resource in the exe
	}
	const (
		wmSetIcon = 0x0080
		iconSmall = 0
		iconBig   = 1
	)
	if small != 0 {
		procSendMessageW.Call(hwnd, wmSetIcon, iconSmall, small)
	}
	if big != 0 {
		procSendMessageW.Call(hwnd, wmSetIcon, iconBig, big)
	}
}
