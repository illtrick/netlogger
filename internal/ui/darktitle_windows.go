//go:build windows

package ui

import (
	"os"
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
	procDwmSetWindowAttribute = dwmapi.NewProc("DwmSetWindowAttribute")
	procExtractIconExW        = shell32.NewProc("ExtractIconExW")
)

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
		const dwmwaUseImmersiveDarkMode = 20 // Windows 10 2004+ / Windows 11
		for i := 0; i < 50; i++ {
			hwnd, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(t)))
			if hwnd != 0 {
				dark := int32(1)
				_, _, _ = procDwmSetWindowAttribute.Call(hwnd, dwmwaUseImmersiveDarkMode,
					uintptr(unsafe.Pointer(&dark)), unsafe.Sizeof(dark))
				setWindowIcon(hwnd)
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()
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
