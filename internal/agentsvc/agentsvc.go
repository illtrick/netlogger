// Package agentsvc wires probes + sync API + (optional) coordinator pull loop
// into a kardianos service, driven by the network config file.
package agentsvc

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/kardianos/service"

	"netlogger/internal/clock"
	"netlogger/internal/config"
	"netlogger/internal/coordinator"
	"netlogger/internal/mesh"
	"netlogger/internal/probe"
	"netlogger/internal/readiness"
	"netlogger/internal/store"
	"netlogger/internal/sysinfo"
	"netlogger/internal/web"
)

// Program is the long-running node: probe loop + sync API + optional puller.
type Program struct {
	ConfigPath string
	NodeID     string
	DBPath     string
	Listen     string // host:port for this node's control server

	store   *store.Store
	srv     *http.Server
	puller  *mesh.Puller
	offsets *mesh.Offsets
	cancel  context.CancelFunc
}

// Start is called by the service manager; it must not block.
func (p *Program) Start(s service.Service) error {
	cfg, err := config.Load(p.ConfigPath)
	if err != nil {
		return err
	}
	self, peers, err := cfg.Resolve(p.NodeID)
	if err != nil {
		return err
	}

	st, err := store.Open(p.DBPath)
	if err != nil {
		return err
	}
	p.store = st

	host, _ := os.Hostname()
	dataDir := filepath.Dir(p.DBPath)
	api := &mesh.AgentAPI{
		Store:         st,
		NodeID:        self.ID,
		Host:          host,
		Iperf3Version: sysinfo.Iperf3Version(),
		DataWritable:  sysinfo.DataDirWritable(dataDir),
	}
	ws := &web.Server{Host: host, ServiceState: "running"}

	if self.Role == "coordinator" {
		p.puller = mesh.NewPuller(st)
		p.offsets = mesh.NewOffsets()
		ids := agentIDs(cfg)
		ws.AgentsHandler = coordinator.AgentsHandler(p.puller, cfg.AddressedNodes())
		ws.ReadinessHandler = coordinator.ReadinessHandler(readiness.NewChecker(), endpointNodes(cfg))
		ws.CorrelationHandler = coordinator.CorrelationHandler(st, ids, p.offsets)
		ws.ComponentsHandler = coordinator.ComponentsHandler(st, cfg)
		ws.LoadTestHandler = coordinator.LoadTestHandler()
		ws.ClassifyHandler = coordinator.ClassifyHandler()
	}

	root := http.NewServeMux()
	api.Register(root) // /api/info, /api/samples
	root.Handle("/", ws.Handler())
	p.srv = &http.Server{
		Addr:              p.Listen,
		Handler:           root,
		ReadHeaderTimeout: 10 * time.Second, // cheap Slowloris defense
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		// WriteTimeout left 0: /api/loadtest streams for the iperf3 duration.
	}

	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel

	go p.srv.ListenAndServe()
	go p.probeLoop(ctx, self.ID, peers)
	if self.Role == "coordinator" {
		go p.pullLoop(ctx, cfg.AddressedNodes())
		go p.offsetLoop(ctx, cfg.AddressedNodes())
	}
	return nil
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
	client := &http.Client{Timeout: 4 * time.Second}
	tick := time.NewTicker(60 * time.Second)
	defer tick.Stop()
	measure := func() {
		for _, n := range nodes {
			if off, err := mesh.MeasureOffset(client, n.BaseURL(), 8); err == nil {
				p.offsets.Set(n.ID, off)
			}
		}
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
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			for _, n := range nodes {
				_, _ = p.puller.PullOnce(mesh.AgentRef{ID: n.ID, BaseURL: n.BaseURL()})
			}
		}
	}
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
	if p.store != nil {
		_ = p.store.Close()
	}
	return nil
}
