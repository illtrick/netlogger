//go:build windows

package ui

import (
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

// System tray support (pure syscall, no cgo): a hidden message window owns a
// notification-area icon. Left-click reopens the app; right-click offers
// Open / Quit. The main window is hidden (not closed) by the Tray button, so
// monitoring continues in the background.

var (
	kernel32              = syscall.NewLazyDLL("kernel32.dll")
	procGetModuleHandleW  = kernel32.NewProc("GetModuleHandleW")
	procRegisterClassExW  = user32.NewProc("RegisterClassExW")
	procCreateWindowExW   = user32.NewProc("CreateWindowExW")
	procDefWindowProcW    = user32.NewProc("DefWindowProcW")
	procGetMessageW       = user32.NewProc("GetMessageW")
	procTranslateMessage  = user32.NewProc("TranslateMessage")
	procDispatchMessageW  = user32.NewProc("DispatchMessageW")
	procShowWindow        = user32.NewProc("ShowWindow")
	procSetForegroundWnd  = user32.NewProc("SetForegroundWindow")
	procPostMessageW      = user32.NewProc("PostMessageW")
	procCreatePopupMenu   = user32.NewProc("CreatePopupMenu")
	procAppendMenuW       = user32.NewProc("AppendMenuW")
	procTrackPopupMenu    = user32.NewProc("TrackPopupMenu")
	procDestroyMenu       = user32.NewProc("DestroyMenu")
	procGetCursorPos      = user32.NewProc("GetCursorPos")
	shell32Tray           = syscall.NewLazyDLL("shell32.dll")
	procShellNotifyIconW  = shell32Tray.NewProc("Shell_NotifyIconW")
	procExtractIconExTray = shell32Tray.NewProc("ExtractIconExW")
)

const (
	wmClose        = 0x0010
	wmTrayCallback = 0x8001 // WM_APP + 1
	wmLButtonUp    = 0x0202
	wmRButtonUp    = 0x0205

	nimAdd    = 0
	nimDelete = 2
	nifMsg    = 0x1
	nifIcon   = 0x2
	nifTip    = 0x4

	swHide    = 0
	swShow    = 5
	swRestore = 9

	mfString       = 0x0
	tpmRightButton = 0x0002
	tpmReturnCmd   = 0x0100
	menuIDOpen     = 1
	menuIDQuit     = 2
)

type notifyIconData struct {
	CbSize           uint32
	HWnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         [16]byte
	HBalloonIcon     uintptr
}

type wndClassEx struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type msgStruct struct {
	HWnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      [2]int32
}

type pointStruct struct{ X, Y int32 }

// mainWindowHWND locates the Gio window by its title.
func mainWindowHWND(title string) uintptr {
	t, _ := syscall.UTF16PtrFromString(title)
	hwnd, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(t)))
	return hwnd
}

// hideMainWindow sends the app to the tray (monitoring continues).
func hideMainWindow(title string) {
	if hwnd := mainWindowHWND(title); hwnd != 0 {
		procShowWindow.Call(hwnd, swHide)
	}
}

func showMainWindow(title string) {
	if hwnd := mainWindowHWND(title); hwnd != 0 {
		procShowWindow.Call(hwnd, swShow)
		procShowWindow.Call(hwnd, swRestore)
		procSetForegroundWnd.Call(hwnd)
	}
}

// startTray creates the notification-area icon on its own OS thread and returns
// a stop func that removes it. mainTitle names the Gio window to open/close.
func startTray(mainTitle string) (stop func()) {
	done := make(chan uintptr, 1) // message-window hwnd, for teardown

	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		hInst, _, _ := procGetModuleHandleW.Call(0)
		className, _ := syscall.UTF16PtrFromString("NetLoggerTray")

		var nid notifyIconData
		wndProc := syscall.NewCallback(func(hwnd, msg, wParam, lParam uintptr) uintptr {
			if msg == wmTrayCallback {
				switch lParam {
				case wmLButtonUp:
					showMainWindow(mainTitle)
				case wmRButtonUp:
					menu, _, _ := procCreatePopupMenu.Call()
					open, _ := syscall.UTF16PtrFromString("Open NetLogger")
					quit, _ := syscall.UTF16PtrFromString("Quit")
					procAppendMenuW.Call(menu, mfString, menuIDOpen, uintptr(unsafe.Pointer(open)))
					procAppendMenuW.Call(menu, mfString, menuIDQuit, uintptr(unsafe.Pointer(quit)))
					var pt pointStruct
					procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
					procSetForegroundWnd.Call(hwnd) // required or the menu won't dismiss
					cmd, _, _ := procTrackPopupMenu.Call(menu, tpmRightButton|tpmReturnCmd,
						uintptr(pt.X), uintptr(pt.Y), 0, hwnd, 0)
					procDestroyMenu.Call(menu)
					switch cmd {
					case menuIDOpen:
						showMainWindow(mainTitle)
					case menuIDQuit:
						if main := mainWindowHWND(mainTitle); main != 0 {
							procShowWindow.Call(main, swShow) // Gio needs a visible window to close cleanly
							procPostMessageW.Call(main, wmClose, 0, 0)
						}
					}
				}
				return 0
			}
			r, _, _ := procDefWindowProcW.Call(hwnd, msg, wParam, lParam)
			return r
		})

		wc := wndClassEx{
			CbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
			LpfnWndProc:   wndProc,
			HInstance:     hInst,
			LpszClassName: className,
		}
		procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
		hwnd, _, _ := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(className)),
			0, 0, 0, 0, 0, 0, 0, 0, hInst, 0)
		if hwnd == 0 {
			done <- 0
			return
		}

		// Icon: the exe's own embedded icon (small size for the tray).
		var iconBig, iconSmall uintptr
		if exe, err := os.Executable(); err == nil {
			if p, err := syscall.UTF16PtrFromString(exe); err == nil {
				procExtractIconExTray.Call(uintptr(unsafe.Pointer(p)), 0,
					uintptr(unsafe.Pointer(&iconBig)), uintptr(unsafe.Pointer(&iconSmall)), 1)
			}
		}
		icon := iconSmall
		if icon == 0 {
			icon = iconBig
		}

		nid = notifyIconData{
			CbSize: uint32(unsafe.Sizeof(nid)), HWnd: hwnd, UID: 1,
			UFlags: nifMsg | nifIcon | nifTip, UCallbackMessage: wmTrayCallback, HIcon: icon,
		}
		tip, _ := syscall.UTF16FromString("NetLogger — monitoring")
		copy(nid.SzTip[:], tip)
		procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
		done <- hwnd

		var m msgStruct
		for {
			r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if r == 0 || int32(r) == -1 { // WM_QUIT or error
				break
			}
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
			procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
		}
		procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
	}()

	hwnd := <-done
	return func() {
		if hwnd != 0 {
			// Remove the icon from the teardown goroutine's thread context: the
			// shell handles NIM_DELETE from any thread.
			nid := notifyIconData{CbSize: uint32(unsafe.Sizeof(notifyIconData{})), HWnd: hwnd, UID: 1}
			procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
		}
	}
}
