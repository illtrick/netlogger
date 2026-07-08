//go:build darwin

package main

/*
#cgo LDFLAGS: -framework CoreGraphics
#include <CoreGraphics/CoreGraphics.h>
*/
import "C"

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"syscall"
	"time"
)

// displayReady reports whether an awake display exists. Gio's darwin backend
// needs one: CVDisplayLink creation fails while every display sleeps — e.g.
// launched at login with the lid closed — and gio v0.10's init error path
// panics instead of returning the error.
func displayReady() bool {
	id := C.CGMainDisplayID()
	if id == 0 {
		return false
	}
	return C.CGDisplayIsAsleep(C.CGDirectDisplayID(id)) == 0
}

// waitForDisplay blocks until a display is awake, so the engine and UI only
// start when the window can actually be created.
func waitForDisplay() {
	if displayReady() {
		return
	}
	log.Printf("display asleep; waiting to start the UI")
	for !displayReady() {
		time.Sleep(5 * time.Second)
	}
	log.Printf("display awake; starting")
}

const uiRetryEnv = "NETLOGGER_UI_RETRY"
const uiRetryCap = 360 // × 10s ≈ 1 hour of retries

// uiPanicRecover is deferred around app.Main: if Gio's window init panics
// anyway (a race the display wait can't fully close — the display can sleep
// between the check and init), relaunch ourselves fresh instead of dying.
// The flock, caffeinate -w child, and SQLite WAL all tolerate the exec.
func uiPanicRecover() {
	r := recover()
	if r == nil {
		return
	}
	n, _ := strconv.Atoi(os.Getenv(uiRetryEnv))
	if n >= uiRetryCap {
		log.Printf("ui init panic (retry cap %d reached): %v", uiRetryCap, r)
		panic(r)
	}
	log.Printf("ui init panic: %v — relaunching in 10s (attempt %d)", r, n+1)
	time.Sleep(10 * time.Second)
	if exe, err := os.Executable(); err == nil {
		env := append(os.Environ(), fmt.Sprintf("%s=%d", uiRetryEnv, n+1))
		_ = syscall.Exec(exe, os.Args, env)
	}
	panic(r) // exec failed; surface the original panic
}
