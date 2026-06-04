package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kardianos/service"

	"netlogger/internal/agentsvc"
	"netlogger/internal/config"
	"netlogger/internal/launch"
	"netlogger/internal/localsettings"
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
	cfgPath := flag.String("config", filepath.Join(dataDir(), "network.yaml"), "path to network config file")
	nodeID := flag.String("node", "", "this machine's node id in the config (defaults to hostname)")
	listen := flag.String("listen", "", "control server host:port (overrides the saved setting; default 0.0.0.0:8088)")
	dbName := flag.String("db", "netlogger.db", "sqlite db filename under the data dir")
	flag.Parse()

	args := flag.Args()
	if len(args) >= 1 && args[0] == "version" {
		fmt.Println("netlogger", version.Version)
		return
	}

	node := *nodeID
	if node == "" {
		node, _ = os.Hostname()
	}

	dir := dataDir()
	_ = os.MkdirAll(dir, 0o755)

	// Machine-local settings (db directory override, etc.) live in the fixed
	// data dir and are read at startup so both interactive and service launches
	// — and self-restarts — honor the same db location.
	settingsPath := localsettings.Path(dir)
	ls, err := localsettings.Load(settingsPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "netlogger: could not read settings:", err)
		ls = &localsettings.Settings{}
	}
	dbPath := localsettings.ResolveDBPath(dir, *dbName, ls)
	_ = os.MkdirAll(filepath.Dir(dbPath), 0o755)

	// Bind address: explicit --listen flag wins; else the saved setting; else
	// 0.0.0.0:8088 so peers can reach this node out of the box (the loopback
	// auth bypass still keeps the local dashboard usable without a token).
	listenAddr := localsettings.ResolveListen(*listen, "0.0.0.0:8088", ls)

	prog := &agentsvc.Program{
		ConfigPath:     *cfgPath,
		NodeID:         node,
		DBPath:         dbPath,
		SettingsPath:   settingsPath,
		DefaultDataDir: dir,
		Listen:         listenAddr,
		ServiceArgs:    []string{"--config", *cfgPath, "--node", node, "--listen", listenAddr, "--db", *dbName},
	}
	svcConfig := &service.Config{
		Name:        "NetLogger",
		DisplayName: "NetLogger Agent",
		Description: "NetLogger network diagnostic agent.",
		Arguments:   []string{"--config", *cfgPath, "--node", node, "--listen", listenAddr, "--db", *dbName},
	}
	s, err := service.New(prog, svcConfig)
	if err != nil {
		fmt.Fprintln(os.Stderr, "service init:", err)
		os.Exit(1)
	}

	if len(args) >= 1 {
		switch args[0] {
		case "install", "uninstall", "start", "stop":
			if err := service.Control(s, args[0]); err != nil {
				fmt.Fprintln(os.Stderr, args[0], "failed:", err)
				os.Exit(1)
			}
			fmt.Println("netlogger:", args[0], "ok")
			return
		case "run":
			// foreground
		default:
			fmt.Fprintln(os.Stderr, "usage: netlogger [flags] [version|install|uninstall|start|stop|run]")
			os.Exit(2)
		}
	}

	// Interactive launch (double-click or `run`): make it just work — create a
	// starter config for this machine if none exists, and open the dashboard.
	// When launched by the service manager, service.Interactive() is false, so
	// we run headless without spawning a browser.
	prog.Interactive = service.Interactive()
	if prog.Interactive {
		if err := config.WriteStarter(*cfgPath, node, launch.HostPort(listenAddr)); err != nil {
			fmt.Fprintln(os.Stderr, "netlogger: could not create starter config:", err)
		}
		// Suppress the browser pop on a self-restart child (the operator's tab
		// is already open and reconnecting).
		prog.OpenBrowser = os.Getenv("NETLOGGER_NO_BROWSER") == ""
	}

	if err := s.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "run:", err)
		os.Exit(1)
	}
}
