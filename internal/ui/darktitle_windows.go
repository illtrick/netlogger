//go:build windows

package ui

import (
	"syscall"
	"time"
	"unsafe"
)

var (
	user32                    = syscall.NewLazyDLL("user32.dll")
	dwmapi                    = syscall.NewLazyDLL("dwmapi.dll")
	procFindWindowW           = user32.NewProc("FindWindowW")
	procDwmSetWindowAttribute = dwmapi.NewProc("DwmSetWindowAttribute")
)

// applyDarkTitleBar asks DWM to render the native title bar dark so it matches
// the app surface. Gio doesn't expose the HWND, so the window is located by its
// title once it exists (retried briefly — creation is asynchronous).
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
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()
}
