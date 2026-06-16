//go:generate goversioninfo -64 -o resource.syso

// Command netlogger-app is the portable, self-elevating native NetLogger app.
package main

import (
	"log"
	"os"
	"path/filepath"

	"netlogger/internal/appcore"
	"netlogger/internal/applog"
	"netlogger/internal/datadir"
	"netlogger/internal/singleton"
	"netlogger/internal/ui"
)

func main() {
	dir, err := datadir.Resolve()
	if err != nil {
		os.Exit(1)
	}
	logDir := dir
	if exe, err := os.Executable(); err == nil {
		logDir = filepath.Dir(exe)
	}
	logFile, err := applog.Init(logDir)
	if err == nil {
		defer logFile.Close()
	}

	release, ok, err := singleton.Acquire("NetLogger.Portable.SingleInstance")
	if err != nil {
		log.Printf("single-instance check failed: %v (continuing)", err)
	}
	if !ok {
		log.Printf("another instance is already running; exiting")
		return
	}
	defer release()

	a := appcore.New(dir)
	if err := a.Start(); err != nil {
		log.Printf("engine start failed: %v", err)
		return
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
}
