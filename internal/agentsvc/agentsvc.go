// Package agentsvc wires the probe runner + web server into a kardianos service.
package agentsvc

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/kardianos/service"

	"netlogger/internal/clock"
	"netlogger/internal/probe"
	"netlogger/internal/store"
	"netlogger/internal/web"
)

// Program is the long-running agent: probe loop + status web server.
type Program struct {
	DBPath string
	Listen string // e.g. "127.0.0.1:8088"

	store  *store.Store
	srv    *http.Server
	cancel context.CancelFunc
}

// Start is called by the service manager; it must not block.
func (p *Program) Start(s service.Service) error {
	st, err := store.Open(p.DBPath)
	if err != nil {
		return err
	}
	p.store = st

	host, _ := os.Hostname()
	ws := &web.Server{Host: host, ServiceState: "running"}
	p.srv = &http.Server{Addr: p.Listen, Handler: ws.Handler()}

	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel

	go p.srv.ListenAndServe()
	go p.loop(ctx, host)
	return nil
}

func (p *Program) loop(ctx context.Context, host string) {
	runner := &probe.Runner{
		Store:   p.store,
		Clock:   clock.System{},
		Src:     host,
		Targets: []string{"127.0.0.1"}, // M1: self-ping proof of life; targets come from config in M2+
		Ping:    probe.PingICMP,
		Timeout: 2 * time.Second,
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = runner.Tick()
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
