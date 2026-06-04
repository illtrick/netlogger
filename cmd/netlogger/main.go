package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kardianos/service"

	"netlogger/internal/agentsvc"
	"netlogger/internal/version"
)

func dataDir() string {
	if pd := os.Getenv("ProgramData"); pd != "" {
		return filepath.Join(pd, "NetLogger")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".netlogger")
}

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "version" {
		fmt.Println("netlogger", version.Version)
		return
	}

	dir := dataDir()
	_ = os.MkdirAll(dir, 0o755)

	prog := &agentsvc.Program{
		DBPath: filepath.Join(dir, "netlogger.db"),
		Listen: "127.0.0.1:8088",
	}
	svcConfig := &service.Config{
		Name:        "NetLogger",
		DisplayName: "NetLogger Agent",
		Description: "NetLogger network diagnostic agent.",
	}
	s, err := service.New(prog, svcConfig)
	if err != nil {
		fmt.Fprintln(os.Stderr, "service init:", err)
		os.Exit(1)
	}

	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "install", "uninstall", "start", "stop":
			if err := service.Control(s, os.Args[1]); err != nil {
				fmt.Fprintln(os.Stderr, os.Args[1], "failed:", err)
				os.Exit(1)
			}
			fmt.Println("netlogger:", os.Args[1], "ok")
			return
		case "run":
			// run in foreground (Ctrl+C to stop)
		default:
			fmt.Fprintln(os.Stderr, "usage: netlogger [version|install|uninstall|start|stop|run]")
			os.Exit(2)
		}
	}

	if err := s.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "run:", err)
		os.Exit(1)
	}
}
