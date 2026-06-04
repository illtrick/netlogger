// Package agentsvc wires probes + sync API + (optional) coordinator pull loop
// into a kardianos service, driven by the network config file.
package agentsvc

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/kardianos/service"

	"netlogger/internal/clock"
	"netlogger/internal/config"
	"netlogger/internal/mesh"
	"netlogger/internal/probe"
	"netlogger/internal/store"
	"netlogger/internal/web"
)

// Program is the long-running node: probe loop + sync API + optional puller.
type Program struct {
	ConfigPath string
	NodeID     string
	DBPath     string
	Listen     string // host:port for this node's control server

	store  *store.Store
	srv    *http.Server
	puller *mesh.Puller
	cancel context.CancelFunc
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
	api := &mesh.AgentAPI{Store: st, NodeID: self.ID, Host: host}
	ws := &web.Server{Host: host, ServiceState: "running"}

	root := http.NewServeMux()
	api.Register(root) // /api/info, /api/samples
	root.Handle("/", ws.Handler())
	p.srv = &http.Server{Addr: p.Listen, Handler: root}

	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel

	go p.srv.ListenAndServe()
	go p.probeLoop(ctx, self.ID, peers)

	if self.Role == "coordinator" {
		p.puller = mesh.NewPuller(st)
		go p.pullLoop(ctx, cfg.AddressedNodes())
	}
	return nil
}

func (p *Program) probeLoop(ctx context.Context, src string, peers []config.TargetRef) {
	targets := make([]string, 0, len(peers))
	for _, t := range peers {
		targets = append(targets, t.ProbeHost())
	}
	if len(targets) == 0 {
		targets = []string{"127.0.0.1"} // lone node: self-ping proof of life
	}
	runner := &probe.Runner{
		Store:   p.store,
		Clock:   clock.System{},
		Src:     src,
		Targets: targets,
		Ping:    probe.PingICMP,
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
