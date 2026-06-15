// Package appcore is the in-process engine controller for the portable app: it
// opens the store, starts the bundled iperf3 server, runs a self-probe loop, and
// exposes a thread-safe Snapshot the UI renders. No service, no HTTP server.
package appcore

import (
	"context"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"netlogger/internal/discovery"
	"netlogger/internal/firewall"
	"netlogger/internal/identity"
	"netlogger/internal/iperf"
	"netlogger/internal/probe"
	"netlogger/internal/store"
	"netlogger/internal/version"
)

// Snapshot is an immutable view of engine state for the UI.
type Snapshot struct {
	DataDir        string
	DBPath         string
	Iperf3Version  string
	Iperf3ServerUp bool
	StartedUnix    int64
	Samples        int
	LastRTTms      float64
	LossPct        float64
	Peers          []PeerInfo
}

// App is the single-machine engine controller.
type App struct {
	dataDir string
	dbPath  string

	// Seams (defaulted in New; overridden in tests).
	Ping       func(addr string, timeout time.Duration) (probe.Result, error)
	StartIperf func(dataDir string) (stop func(), version string)
	tick       time.Duration

	store     *store.Store
	iperfStop func()
	iperfVer  string
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	stopOnce  sync.Once
	startedAt time.Time

	Discovery PeerLister // injectable; if nil, Start creates a real discovery.Service
	disc      *discovery.Service
	nodeID    string
	host      string

	mu        sync.Mutex
	samples   int
	lastRTTms float64
	recent    []bool // success flags, last N, for loss %

	peerMu    sync.Mutex
	peerStats map[string]*peerStat
}

const recentWindow = 60

const (
	controlPort    = 8088
	discoveryGroup = "239.255.74.76"
	discoveryPort  = 48076
)

// PeerLister is the discovery source the UI reads peers through.
type PeerLister interface {
	Peers() []discovery.Peer
}

// PeerInfo is a discovered peer as exposed to the UI.
type PeerInfo struct {
	ID           string
	Host         string
	Addr         string
	Version      string
	LastSeenUnix int64
	RTTms        float64
	LossPct      float64
}

// New creates an App for the given (already-resolved) data dir.
func New(dataDir string) *App {
	return &App{
		dataDir:    dataDir,
		dbPath:     filepath.Join(dataDir, "netlogger.db"),
		Ping:       probe.PingICMP,
		StartIperf: defaultStartIperf,
		tick:       time.Second,
		peerStats:  make(map[string]*peerStat),
	}
}

func (a *App) statFor(id string) *peerStat {
	a.peerMu.Lock()
	defer a.peerMu.Unlock()
	s := a.peerStats[id]
	if s == nil {
		s = &peerStat{}
		a.peerStats[id] = s
	}
	return s
}

func defaultStartIperf(dir string) (func(), string) {
	if err := iperf.Bootstrap(dir); err != nil {
		log.Printf("iperf3 bootstrap: %v", err)
	}
	ver := iperf.Version()
	srv := iperf.StartServer(0)
	if srv == nil {
		// No iperf3 binary available: report no server (nil stop).
		return nil, ver
	}
	return func() { srv.Stop() }, ver
}

// Start opens the store, starts iperf, and launches the probe loop.
func (a *App) Start() error {
	st, err := store.Open(a.dbPath)
	if err != nil {
		return err
	}
	stop, ver := a.StartIperf(a.dataDir)

	// Publish startup state under the lock so a concurrent Snapshot (UI
	// goroutine) sees a consistent view — Snapshot reads these under a.mu.
	a.mu.Lock()
	a.store = st
	a.iperfStop = stop
	a.iperfVer = ver
	a.startedAt = time.Now()
	a.mu.Unlock()

	nodeID, _ := identity.NodeID(a.dataDir)
	host, _ := os.Hostname()
	var disc *discovery.Service
	if a.Discovery == nil {
		_ = firewall.AllowProgram("NetLogger")
		svc := discovery.New(discovery.Config{
			SelfID: nodeID, Host: host, ControlPort: controlPort, Version: version.Version,
			Group: discoveryGroup, Port: discoveryPort,
		})
		if err := svc.Start(); err != nil {
			log.Printf("discovery start: %v", err)
		} else {
			disc = svc
		}
	}
	a.mu.Lock()
	a.nodeID = nodeID
	a.host = host
	if disc != nil {
		a.disc = disc
		a.Discovery = disc
	}
	a.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.wg.Add(2)
	go a.probeLoop(ctx)
	go a.peerLoop(ctx)
	return nil
}

func (a *App) probeLoop(ctx context.Context) {
	defer a.wg.Done()
	t := time.NewTicker(a.tick)
	defer t.Stop()
	var seq int64
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			res, err := a.Ping("127.0.0.1", 2*time.Second)
			lost := err != nil || res.Lost
			seq++
			sm := store.Sample{
				Seq:       seq,
				TSUnixUS:  time.Now().UnixMicro(),
				ProbeType: "icmp",
				SrcHost:   "self",
				DstHost:   "self",
				Direction: "rtt",
				RTTus:     res.RTT.Microseconds(),
				Lost:      lost,
			}
			_, _ = a.store.Insert(sm)

			a.mu.Lock()
			a.samples++
			if !lost {
				a.lastRTTms = float64(res.RTT.Microseconds()) / 1000.0
			}
			a.recent = append(a.recent, !lost)
			if len(a.recent) > recentWindow {
				a.recent = a.recent[len(a.recent)-recentWindow:]
			}
			a.mu.Unlock()
		}
	}
}

// peerLoop probes every discovered peer once per tick and records per-peer stats.
func (a *App) peerLoop(ctx context.Context) {
	defer a.wg.Done()
	t := time.NewTicker(a.tick)
	defer t.Stop()
	var seq int64
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.mu.Lock()
			disc := a.Discovery
			a.mu.Unlock()
			if disc == nil {
				continue
			}
			for _, p := range disc.Peers() {
				host := p.Addr
				if h, _, err := net.SplitHostPort(p.Addr); err == nil {
					host = h
				}
				res, err := a.Ping(host, 2*time.Second)
				lost := err != nil || res.Lost
				rttms := float64(res.RTT.Microseconds()) / 1000.0
				a.statFor(p.ID).record(!lost, rttms)
				seq++
				_, _ = a.store.Insert(store.Sample{
					Seq: seq, TSUnixUS: time.Now().UnixMicro(), ProbeType: "icmp",
					SrcHost: a.nodeID, DstHost: p.ID, Direction: "rtt",
					RTTus: res.RTT.Microseconds(), Lost: lost,
				})
			}
		}
	}
}

// Snapshot returns an immutable copy of current engine state.
func (a *App) Snapshot() Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	loss := 0.0
	if n := len(a.recent); n > 0 {
		lost := 0
		for _, ok := range a.recent {
			if !ok {
				lost++
			}
		}
		loss = float64(lost) / float64(n) * 100.0
	}
	var peers []PeerInfo
	if a.Discovery != nil {
		for _, p := range a.Discovery.Peers() {
			rtt, loss := a.statFor(p.ID).read()
			peers = append(peers, PeerInfo{
				ID: p.ID, Host: p.Host, Addr: p.Addr, Version: p.Version,
				LastSeenUnix: p.LastSeen.Unix(), RTTms: rtt, LossPct: loss,
			})
		}
	}
	return Snapshot{
		DataDir:        a.dataDir,
		DBPath:         a.dbPath,
		Iperf3Version:  a.iperfVer,
		Iperf3ServerUp: a.iperfStop != nil,
		StartedUnix:    a.startedAt.Unix(),
		Samples:        a.samples,
		LastRTTms:      a.lastRTTms,
		LossPct:        loss,
		Peers:          peers,
	}
}

// Stop cancels the loop, stops iperf, and closes the store. Safe to call more
// than once; only the first call performs the teardown.
func (a *App) Stop() error {
	var err error
	a.stopOnce.Do(func() {
		if a.cancel != nil {
			a.cancel()
		}
		a.wg.Wait()
		if a.disc != nil {
			_ = a.disc.Stop()
		}
		if a.iperfStop != nil {
			a.iperfStop()
		}
		if a.store != nil {
			err = a.store.Close()
		}
	})
	return err
}
