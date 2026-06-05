// Package agentsvc wires probes + sync API + (optional) coordinator pull loop
// into a kardianos service, driven by the network config file.
package agentsvc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kardianos/service"

	"netlogger/internal/clock"
	"netlogger/internal/config"
	"netlogger/internal/coordinator"
	"netlogger/internal/httpauth"
	"netlogger/internal/iperf"
	"netlogger/internal/launch"
	"netlogger/internal/localsettings"
	"netlogger/internal/mesh"
	"netlogger/internal/probe"
	"netlogger/internal/readiness"
	"netlogger/internal/store"
	"netlogger/internal/svcctl"
	"netlogger/internal/sysinfo"
	"netlogger/internal/web"
)

// Program is the long-running node: probe loop + sync API + optional puller.
type Program struct {
	ConfigPath     string
	NodeID         string
	DBPath         string
	SettingsPath   string   // machine-local settings.json (db dir override, etc.)
	DefaultDataDir string   // default dir for the db when no override is set
	Listen         string   // host:port for this node's control server
	OpenBrowser    bool     // open the dashboard in a browser after start (interactive launch)
	Interactive    bool     // true for a foreground/double-click launch (enables self-restart)
	ServiceArgs    []string // flags to pass to elevated service-control commands

	store      *store.Store
	srv        *http.Server
	iperfSrv   *iperf.Server
	puller     *mesh.Puller
	offsets    *mesh.Offsets
	httpClient *http.Client
	cancel     context.CancelFunc
	wg         sync.WaitGroup

	cfgMu sync.Mutex
	cfg   *config.Config
}

// Start is called by the service manager; it must not block.
func (p *Program) Start(s service.Service) error {
	cfg, err := config.Load(p.ConfigPath)
	if err != nil {
		return err
	}
	p.cfgMu.Lock()
	p.cfg = cfg
	p.cfgMu.Unlock()
	self, peers, err := cfg.Resolve(p.NodeID)
	if err != nil {
		return err
	}

	st, err := store.Open(p.DBPath)
	if err != nil {
		return err
	}
	p.store = st

	// Shared control-plane token (same value on every node). Empty disables auth.
	token := os.Getenv("NETLOGGER_TOKEN")
	if token == "" {
		fmt.Fprintln(os.Stderr, "netlogger: NETLOGGER_TOKEN not set — control plane is unauthenticated (loopback only is safe)")
	}
	p.httpClient = mesh.AuthClient(token, 5*time.Second)

	host, _ := os.Hostname()
	dataDir := filepath.Dir(p.DBPath)
	// Extract the bundled iperf3 (Windows) so load tests are turnkey, then start
	// an always-on iperf3 server so this node can be a load-test target.
	if err := iperf.Bootstrap(dataDir); err != nil {
		fmt.Fprintln(os.Stderr, "netlogger: iperf3 bundle:", err)
	}
	p.iperfSrv = iperf.StartServer(0)
	api := &mesh.AgentAPI{
		Store:         st,
		NodeID:        self.ID,
		Host:          host,
		Iperf3Version: iperf.Version(),
		DataWritable:  sysinfo.DataDirWritable(dataDir),
	}
	ws := &web.Server{Host: host, ServiceState: "running"}
	ws.ConfigHandler = p.handleConfig     // GET/POST the network config (any node)
	ws.SettingsHandler = p.handleSettings // GET/POST machine-local settings (db dir)
	ws.RestartHandler = p.handleRestart   // apply config changes
	ws.ServiceHandler = p.handleService   // install/start/stop/uninstall (elevated)
	ws.QuitHandler = p.handleQuit         // stop this foreground app from the GUI
	ws.DownloadAgent = p.handleDownloadAgent
	ws.DownloadConfig = p.handleDownloadConfig

	if self.Role == "coordinator" {
		p.puller = mesh.NewPuller(st)
		p.puller.SetClient(p.httpClient)
		p.offsets = mesh.NewOffsets()
		ids := agentIDs(cfg)
		checker := readiness.NewChecker()
		checker.Client = p.httpClient
		ws.AgentsHandler = coordinator.AgentsHandler(p.puller, cfg.AddressedNodes())
		ws.ReadinessHandler = coordinator.ReadinessHandler(checker, endpointNodes(cfg))
		ws.CorrelationHandler = coordinator.CorrelationHandler(st, ids, p.offsets)
		ws.ComponentsHandler = coordinator.ComponentsHandler(st, cfg)
		ws.LoadTestHandler = coordinator.LoadTestHandler(loadTargets(cfg), nil)
		ws.ClassifyHandler = coordinator.ClassifyHandler()
		ws.TopologyHandler = coordinator.TopologyHandler(cfg)
	}

	root := http.NewServeMux()
	api.Register(root) // /api/info, /api/samples
	root.Handle("/", ws.Handler())
	handler := httpauth.Middleware(token)(root)
	p.srv = &http.Server{
		Addr:              p.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second, // cheap Slowloris defense
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		// WriteTimeout left 0: /api/loadtest streams for the iperf3 duration.
	}

	// Bind synchronously so a bad address / port-in-use fails service startup
	// loudly. Retry briefly to tolerate the hand-off during a self-restart
	// (the old process needs a moment to release the port).
	var ln net.Listener
	for i := 0; i < 10; i++ {
		ln, err = net.Listen("tcp", p.Listen)
		if err == nil {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if err != nil {
		st.Close()
		return fmt.Errorf("bind %s: %w", p.Listen, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel

	go func() { _ = p.srv.Serve(ln) }()
	if p.OpenBrowser {
		url := launch.BrowserURL(p.Listen)
		fmt.Fprintln(os.Stderr, "netlogger: dashboard at", url)
		go func() { _ = launch.OpenBrowser(url) }()
	}
	p.spawn(func() { p.probeLoop(ctx, self.ID, peers) })
	if self.Role == "coordinator" {
		p.spawn(func() { p.pullLoop(ctx, cfg.AddressedNodes()) })
		p.spawn(func() { p.offsetLoop(ctx, cfg.AddressedNodes()) })
	}
	return nil
}

// spawn runs fn in a tracked goroutine so Stop can join it before closing the store.
func (p *Program) spawn(fn func()) {
	p.wg.Add(1)
	go func() { defer p.wg.Done(); fn() }()
}

// loadTargets maps each addressed node id -> its probe host, for the load-test allowlist.
func loadTargets(cfg *config.Config) map[string]string {
	m := map[string]string{}
	for _, t := range cfg.AddressedNodes() {
		m[t.ID] = t.ProbeHost()
	}
	return m
}

// endpointNodes returns the config nodes that have a control address.
func endpointNodes(cfg *config.Config) []config.Node {
	var out []config.Node
	for _, n := range cfg.Nodes {
		if n.Address != "" {
			out = append(out, n)
		}
	}
	return out
}

func agentIDs(cfg *config.Config) []string {
	var ids []string
	for _, t := range cfg.AddressedNodes() {
		ids = append(ids, t.ID)
	}
	return ids
}

func (p *Program) offsetLoop(ctx context.Context, nodes []config.TargetRef) {
	tick := time.NewTicker(60 * time.Second)
	defer tick.Stop()
	measure := func() {
		var wg sync.WaitGroup
		for _, n := range nodes {
			wg.Add(1)
			go func(n config.TargetRef) {
				defer wg.Done()
				if off, err := mesh.MeasureOffset(p.httpClient, n.BaseURL(), 8); err == nil {
					p.offsets.Set(n.ID, off)
				}
			}(n)
		}
		wg.Wait()
	}
	measure() // once at startup
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			measure()
		}
	}
}

func (p *Program) probeLoop(ctx context.Context, src string, peers []config.TargetRef) {
	hostByID := make(map[string]string, len(peers))
	targets := make([]string, 0, len(peers))
	for _, t := range peers {
		hostByID[t.ID] = t.ProbeHost()
		targets = append(targets, t.ID)
	}
	if len(targets) == 0 {
		hostByID["self"] = "127.0.0.1" // lone node: self-ping proof of life
		targets = []string{"self"}
	}
	// Ping resolves a node id to its host and pings it; the sample is labeled
	// with the node id (what scoring/correlation key off).
	ping := func(nodeID string, timeout time.Duration) (probe.Result, error) {
		return probe.PingICMP(hostByID[nodeID], timeout)
	}
	runner := &probe.Runner{
		Store:   p.store,
		Clock:   clock.System{},
		Src:     src,
		Targets: targets,
		Ping:    ping,
		Timeout: 2 * time.Second,
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			_ = runner.Tick()
		}
	}
}

func (p *Program) pullLoop(ctx context.Context, nodes []config.TargetRef) {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	pullAll := func() {
		// Fan out so one slow/dead agent can't delay pulls from the others.
		var wg sync.WaitGroup
		for _, n := range nodes {
			wg.Add(1)
			go func(n config.TargetRef) {
				defer wg.Done()
				_, _ = p.puller.PullOnce(mesh.AgentRef{ID: n.ID, BaseURL: n.BaseURL()})
			}(n)
		}
		wg.Wait()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			pullAll()
		}
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// handleConfig serves GET (current config) and POST (validate + save) for the
// in-GUI editor. Saved changes apply on the next restart.
func (p *Program) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		p.cfgMu.Lock()
		c := p.cfg
		p.cfgMu.Unlock()
		writeJSON(w, c)
	case http.MethodPost:
		var c config.Config
		if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": "invalid json: " + err.Error()})
			return
		}
		if err := config.Save(p.ConfigPath, &c); err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		p.cfgMu.Lock()
		p.cfg = &c
		p.cfgMu.Unlock()
		writeJSON(w, map[string]any{"ok": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleSettings serves machine-local preferences for the in-GUI editor. GET
// reports where the database currently lives and the current bind address; POST
// sets a new db directory (validated writable) and/or bind address (validated
// host:port), persisting only the fields present in the request body. Changes
// apply on the next restart, since both are read once at startup.
func (p *Program) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		ls, _ := localsettings.Load(p.SettingsPath)
		writeJSON(w, map[string]any{
			"db_path":     p.DBPath,
			"db_dir":      filepath.Dir(p.DBPath),
			"default_dir": p.DefaultDataDir,
			"configured":  ls.DBDir,
			"listen":      p.Listen,
			"listen_set":  ls.Listen,
			"interactive": p.Interactive,
		})
	case http.MethodPost:
		// Pointer fields so we only touch settings a form actually sent.
		var body struct {
			DBDir  *string `json:"db_dir"`
			Listen *string `json:"listen"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": "invalid json: " + err.Error()})
			return
		}
		ls, _ := localsettings.Load(p.SettingsPath)
		if body.DBDir != nil {
			dir := strings.TrimSpace(*body.DBDir)
			if dir != "" {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					writeJSON(w, map[string]any{"ok": false, "error": "cannot create directory: " + err.Error()})
					return
				}
				if !sysinfo.DataDirWritable(dir) {
					writeJSON(w, map[string]any{"ok": false, "error": "directory is not writable: " + dir})
					return
				}
			}
			ls.DBDir = dir
		}
		if body.Listen != nil {
			addr := strings.TrimSpace(*body.Listen)
			if addr != "" {
				if _, _, err := net.SplitHostPort(addr); err != nil {
					writeJSON(w, map[string]any{"ok": false, "error": "bind address must be host:port (e.g. 0.0.0.0:8088)"})
					return
				}
			}
			ls.Listen = addr
		}
		if err := localsettings.Save(p.SettingsPath, ls); err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, map[string]any{"ok": true, "restart_required": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleService reports the service state (GET) and runs an elevated
// install/start/stop/uninstall via a UAC prompt (POST {action}).
func (p *Program) handleService(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, map[string]any{
			"supported": svcctl.Supported(),
			"state":     svcctl.Status(),
		})
	case http.MethodPost:
		var body struct {
			Action string `json:"action"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if !svcctl.ValidAction(body.Action) {
			writeJSON(w, map[string]any{"ok": false, "error": "invalid action"})
			return
		}
		exe, err := os.Executable()
		if err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		args := append(append([]string{}, p.ServiceArgs...), body.Action)
		if err := svcctl.RunElevated(exe, args); err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, map[string]any{"ok": true, "note": "approve the UAC prompt to " + body.Action + " the service"})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleDownloadAgent serves this running binary so a peer can fetch the exact
// agent (same OS/arch). Used by the GUI's push-to-peer bootstrap.
func (p *Program) handleDownloadAgent(w http.ResponseWriter, r *http.Request) {
	exe, err := os.Executable()
	if err != nil {
		http.Error(w, "agent binary unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename=netlogger.exe")
	http.ServeFile(w, r, exe)
}

// handleDownloadConfig serves the current network config as YAML, for peers.
func (p *Program) handleDownloadConfig(w http.ResponseWriter, r *http.Request) {
	p.cfgMu.Lock()
	c := p.cfg
	p.cfgMu.Unlock()
	data, err := config.ToYAML(c)
	if err != nil {
		http.Error(w, "config unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/yaml")
	w.Header().Set("Content-Disposition", "attachment; filename=network.yaml")
	_, _ = w.Write(data)
}

// handleQuit stops a foreground (double-click) app from the GUI. Under a
// service manager it refuses — use Stop instead.
func (p *Program) handleQuit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !p.Interactive {
		writeJSON(w, map[string]any{"ok": false, "error": "running as a service — use Stop"})
		return
	}
	writeJSON(w, map[string]any{"ok": true})
	go func() { time.Sleep(200 * time.Millisecond); _ = p.Stop(nil); os.Exit(0) }()
}

// handleRestart relaunches the process to apply config changes (interactive
// launches only; under a service manager the service should be restarted).
func (p *Program) handleRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !p.Interactive {
		writeJSON(w, map[string]any{"ok": false, "error": "running as a service — restart the NetLogger service to apply"})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "restarting": true})
	go p.restart()
}

// restart spawns a fresh copy of this process (which re-binds the same port via
// the bind-retry), then exits so the new one takes over. The child suppresses
// the browser pop so the operator's existing tab simply reconnects.
func (p *Program) restart() {
	time.Sleep(300 * time.Millisecond) // let the HTTP response flush
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Env = append(os.Environ(), "NETLOGGER_NO_BROWSER=1")
	_ = cmd.Start()
	_ = p.Stop(nil)
	os.Exit(0)
}

// Stop is called by the service manager on shutdown.
func (p *Program) Stop(s service.Service) error {
	if p.cancel != nil {
		p.cancel()
	}
	if p.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = p.srv.Shutdown(ctx)
	}
	if p.iperfSrv != nil {
		p.iperfSrv.Stop()
	}
	p.wg.Wait() // join probe/pull/offset loops so no goroutine is mid-write at Close
	if p.store != nil {
		_ = p.store.Close()
	}
	return nil
}
