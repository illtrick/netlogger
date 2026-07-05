//go:generate goversioninfo -64 -o resource.syso

// Command netlogger-app is the portable, self-elevating native NetLogger app.
package main

import (
	"log"
	"os"

	"gioui.org/app"

	"netlogger/internal/appcore"
	"netlogger/internal/applog"
	"netlogger/internal/datadir"
	"netlogger/internal/singleton"
	"netlogger/internal/ui"
)

// main hands the main OS thread to Gio: macOS requires the Cocoa event loop
// to run there (without it the app bounces in the Dock and never shows a
// window). All real work happens in run() on another goroutine; os.Exit
// fires only after run's defers complete.
func main() {
	go func() {
		os.Exit(run())
	}()
	app.Main()
}

func run() int {
	dir, err := datadir.Resolve()
	if err != nil {
		return 1
	}
	// Log beside the exe on portable platforms, in the data dir when the exe
	// lives inside a .app bundle (never write into the bundle).
	logFile, err := applog.Init(datadir.SidecarDir(dir))
	if err == nil {
		defer logFile.Close()
	}

	release, ok, err := singleton.Acquire("NetLogger.Portable.SingleInstance")
	if err != nil {
		log.Printf("single-instance check failed: %v (continuing)", err)
	}
	if !ok {
		log.Printf("another instance is already running; exiting")
		return 0
	}
	defer release()

	a := appcore.New(dir)
	if err := a.Start(); err != nil {
		log.Printf("engine start failed: %v", err)
		return 1
	}
	defer func() {
		if err := a.Stop(); err != nil {
			log.Printf("engine stop error: %v", err)
		}
	}()

	log.Printf("NetLogger started; data dir %s", dir)
	if err := ui.Run(a); err != nil {
		log.Printf("ui exited with error: %v", err)
	}
	log.Printf("NetLogger shutting down")
	return 0
}
