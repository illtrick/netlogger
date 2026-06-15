//go:build windows

// Package singleton enforces one running instance per machine via a named mutex.
package singleton

import "golang.org/x/sys/windows"

// Acquire creates a named mutex. If another instance already holds it, ok is
// false. The caller must call release at shutdown when ok is true.
func Acquire(name string) (release func(), ok bool, err error) {
	p, err := windows.UTF16PtrFromString(`Local\` + name)
	if err != nil {
		return nil, false, err
	}
	h, err := windows.CreateMutex(nil, false, p)
	if h == 0 {
		return nil, false, err
	}
	if err == windows.ERROR_ALREADY_EXISTS {
		windows.CloseHandle(h)
		return nil, false, nil
	}
	return func() { windows.CloseHandle(h) }, true, nil
}
