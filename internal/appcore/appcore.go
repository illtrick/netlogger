// Package appcore is the in-process engine controller for the portable app: it
// opens the store, starts the bundled iperf3 server, runs a self-probe loop, and
// exposes a thread-safe Snapshot the UI renders. No service, no HTTP server.
package appcore

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"netlogger/internal/discovery"
	"netlogger/internal/firewall"
	"netlogger/internal/gateway"
	"netlogger/internal/httpauth"
	"netlogger/internal/identity"
	"netlogger/internal/iperf"
	"netlogger/internal/probe"
	"netlogger/internal/store"
	"netlogger/internal/version"
)

// Snapshot is an immutable view of engine state for the UI.
type Snapshot struct {
	DataDir          string
	DBPath           string
	Iperf3Version    string
	Iperf3ServerUp   bool
	StartedUnix      int64
	Samples          int
	LastRTTms        float64
	LossPct          float64
	Peers            []PeerInfo
	GatewayIP        string
	GatewayRTTms     float64
	GatewayLossPct   float64
	Matrix           Matrix
	SessionUptimeSec int64
	InternetIP       string
	InternetRTTms    float64
	InternetLossPct  float64
	GatewayHist      []float64
	InternetHist     []float64
}

// App is the single-machine engine controller.
type App struct {
	dataDir string
	dbPath  string

	// Seams (defaulted in New; overridden in tests).
	Ping       func(addr string, timeout time.Duration) (probe.Result, error)
	StartIperf func(dataDir string) (stop func(), version string)
	ProbeUDP   func(target string, count int, interval, timeout time.Duration) (probe.UDPStats, error)
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

	// FetchLinks fetches a peer's link report; defaults to an HTTP client call.
	FetchLinks  func(baseURL string) (LinkReport, error)
	linksSrv    *http.Server
	reportMu    sync.Mutex
	peerReports map[string]LinkReport

	// GatewayIP is the router to probe; auto-detected in Start when empty.
	GatewayIP string
	// InternetIP is a public host to probe for internet reachability.
	InternetIP string

	// History rings and per-peer uptime tracking.
	firstSeen map[string]time.Time
	rttHist   map[string]*histRing
	lossHist  map[string]*histRing
	gwHist    *histRing
	netHist   *histRing

	mu        sync.Mutex
	samples   int
	lastRTTms float64
	recent    []bool // success flags, last N, for loss %

	peerMu     sync.Mutex
	peerStats  map[string]*peerStat
	udpStats   map[string]*udpStat
	linkStates map[string]*linkState
	udpEcho    *probe.UDPEcho
}

const recentWindow = 60

const (
	controlPort    = 8088
	discoveryGroup = "239.255.74.76"
	discoveryPort  = 48076
	udpEchoPort    = 8089
	udpProbeCount  = 200
	internetTarget = "8.8.8.8"
	histLen        = 120
)

var (
	udpProbeInterval = 5 * time.Millisecond
	udpProbeTimeout  = 200 * time.Millisecond
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
	JitterMs     float64
	UDPLossPct   float64
	DropEpisodes int
	UpForSec     int64
	RTTHist      []float64
	LossHist     []float64
}

// New creates an App for the given (already-resolved) data dir.
func New(dataDir string) *App {
	return &App{
		dataDir:     dataDir,
		dbPath:      filepath.Join(dataDir, "netlogger.db"),
		Ping:        probe.PingICMP,
		StartIperf:  defaultStartIperf,
		ProbeUDP:    probe.ProbeUDP,
		tick:        time.Second,
		peerStats:   make(map[string]*peerStat),
		udpStats:    make(map[string]*udpStat),
		linkStates:  make(map[string]*linkState),
		peerReports: make(map[string]LinkReport),
		FetchLinks: func(baseURL string) (LinkReport, error) {
			return fetchLinks(&http.Client{Timeout: 1500 * time.Millisecond}, baseURL)
		},
		InternetIP: internetTarget,
		firstSeen:  make(map[string]time.Time),
		rttHist:    make(map[string]*histRing),
		lossHist:   make(map[string]*histRing),
		gwHist:     newHistRing(histLen),
		netHist:    newHistRing(histLen),
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

func (a *App) udpStatFor(id string) *udpStat {
	a.peerMu.Lock()
	defer a.peerMu.Unlock()
	s := a.udpStats[id]
	if s == nil {
		s = &udpStat{}
		a.udpStats[id] = s
	}
	return s
}

func (a *App) histFor(m map[string]*histRing, id string) *histRing {
	a.peerMu.Lock()
	defer a.peerMu.Unlock()
	r := m[id]
	if r == nil {
		r = newHistRing(histLen)
		m[id] = r
	}
	return r
}

func (a *App) markSeen(id string) (time.Time, bool) {
	a.peerMu.Lock()
	defer a.peerMu.Unlock()
	if t, ok := a.firstSeen[id]; ok {
		return t, false
	}
	now := time.Now()
	a.firstSeen[id] = now
	return now, true
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
		_ = firewall.AllowPing("NetLogger ICMP")
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
	if a.GatewayIP == "" {
		a.GatewayIP = gateway.Default()
	}
	a.mu.Unlock()

	if e, err := probe.StartUDPEcho("0.0.0.0:" + strconv.Itoa(udpEchoPort)); err != nil {
		log.Printf("udp echo start: %v", err)
	} else {
		a.udpEcho = e
	}

	if a.disc != nil {
		mux := http.NewServeMux()
		mux.Handle("/api/links", linksHandler(a.linkReport))
		a.linksSrv = &http.Server{Addr: "0.0.0.0:" + strconv.Itoa(controlPort), Handler: httpauth.Middleware("")(mux)}
		go func() { _ = a.linksSrv.ListenAndServe() }()
	}

	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.wg.Add(4)
	go a.probeLoop(ctx)
	go a.peerLoop(ctx)
	go a.udpLoop(ctx)
	go a.linkPullLoop(ctx)
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
			if a.GatewayIP != "" {
				res, err := a.Ping(a.GatewayIP, 2*time.Second)
				ok := err == nil && !res.Lost
				a.statFor("__gateway__").record(ok, float64(res.RTT.Microseconds())/1000.0)
				if ok {
					a.gwHist.push(float64(res.RTT.Microseconds()) / 1000.0)
				}
			}
			if a.InternetIP != "" {
				res, err := a.Ping(a.InternetIP, 2*time.Second)
				ok := err == nil && !res.Lost
				a.statFor("__internet__").record(ok, float64(res.RTT.Microseconds())/1000.0)
				if ok {
					a.netHist.push(float64(res.RTT.Microseconds()) / 1000.0)
				}
			}
		}
	}
}

// udpLoop runs a high-rate isochronous UDP burst against each discovered peer's
// echo responder, capturing jitter + micro-drop episodes that 1 Hz ICMP misses.
func (a *App) udpLoop(ctx context.Context) {
	defer a.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		a.mu.Lock()
		disc := a.Discovery
		a.mu.Unlock()
		var peers []discovery.Peer
		if disc != nil {
			peers = disc.Peers()
		}
		if len(peers) == 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(a.tick):
			}
			continue
		}
		for _, p := range peers {
			if ctx.Err() != nil {
				return
			}
			host := p.Addr
			if h, _, err := net.SplitHostPort(p.Addr); err == nil {
				host = h
			}
			target := net.JoinHostPort(host, strconv.Itoa(udpEchoPort))
			st, err := a.ProbeUDP(target, udpProbeCount, udpProbeInterval, udpProbeTimeout)
			if err == nil {
				a.udpStatFor(p.ID).record(st)
				rtt, _, loss, _ := a.udpStatFor(p.ID).read()
				a.histFor(a.rttHist, p.ID).push(rtt)
				a.histFor(a.lossHist, p.ID).push(loss)
				if _, isNew := a.markSeen(p.ID); isNew {
					a.recordEvent(true, "peer "+peerLabel(p)+" joined")
				}
				_, _ = a.store.Insert(store.Sample{
					TSUnixUS:  time.Now().UnixMicro(),
					ProbeType: "udp_iso",
					SrcHost:   a.nodeID,
					DstHost:   p.ID,
					Direction: "rtt",
					RTTus:     st.AvgRTT.Microseconds(),
					JitterUS:  st.Jitter.Microseconds(),
					Lost:      st.LossPct > 0,
				})
				if changed, degraded := a.linkStateFor(p.ID).step(st.LossPct); changed {
					if degraded {
						a.recordEvent(false, "link to "+peerLabel(p)+" degraded (loss "+strconv.FormatFloat(st.LossPct, 'f', 1, 64)+"%)")
					} else {
						a.recordEvent(true, "link to "+peerLabel(p)+" recovered")
					}
				}
			}
		}
	}
}

// NodeID returns this node's stable id (valid after Start).
func (a *App) NodeID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.nodeID
}

// linkReport builds this node's current outbound link report from its UDP stats.
func (a *App) linkReport() LinkReport {
	a.mu.Lock()
	id, host, disc := a.nodeID, a.host, a.Discovery
	a.mu.Unlock()
	var links []LinkStat
	if disc != nil {
		for _, p := range disc.Peers() {
			rtt, jitter, loss, eps := a.udpStatFor(p.ID).read()
			links = append(links, LinkStat{PeerID: p.ID, RTTms: rtt, JitterMs: jitter, LossPct: loss, Drops: eps})
		}
	}
	return LinkReport{NodeID: id, Host: host, Links: links}
}

// linkPullLoop fetches each discovered peer's link report so this window can show
// the full mesh (every directed link), not just its own outbound links.
func (a *App) linkPullLoop(ctx context.Context) {
	defer a.wg.Done()
	t := time.NewTicker(a.tick)
	defer t.Stop()
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
				if ctx.Err() != nil {
					return // exit promptly on shutdown between peers
				}
				rep, err := a.FetchLinks("http://" + p.Addr)
				if err == nil && rep.NodeID != "" {
					a.reportMu.Lock()
					a.peerReports[rep.NodeID] = rep
					a.reportMu.Unlock()
				}
			}
		}
	}
}

// Snapshot returns an immutable copy of current engine state.
func (a *App) Snapshot() Snapshot {
	a.mu.Lock()
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
			rtt, lp := a.statFor(p.ID).read()
			_, jitter, uloss, episodes := a.udpStatFor(p.ID).read()
			pi := PeerInfo{
				ID: p.ID, Host: p.Host, Addr: p.Addr, Version: p.Version,
				LastSeenUnix: p.LastSeen.Unix(), RTTms: rtt, LossPct: lp,
				JitterMs: jitter, UDPLossPct: uloss, DropEpisodes: episodes,
			}
			fs, _ := a.markSeen(p.ID)
			pi.UpForSec = int64(time.Since(fs).Seconds())
			pi.RTTHist = a.histFor(a.rttHist, p.ID).values()
			pi.LossHist = a.histFor(a.lossHist, p.ID).values()
			peers = append(peers, pi)
		}
	}
	var gwRTT, gwLoss float64
	if a.GatewayIP != "" {
		gwRTT, gwLoss = a.statFor("__gateway__").read()
	}
	var netRTT, netLoss float64
	if a.InternetIP != "" {
		netRTT, netLoss = a.statFor("__internet__").read()
	}
	snap := Snapshot{
		DataDir:        a.dataDir,
		DBPath:         a.dbPath,
		Iperf3Version:  a.iperfVer,
		Iperf3ServerUp: a.iperfStop != nil,
		StartedUnix:    a.startedAt.Unix(),
		Samples:        a.samples,
		LastRTTms:      a.lastRTTms,
		LossPct:        loss,
		Peers:          peers,
		GatewayIP:      a.GatewayIP, GatewayRTTms: gwRTT, GatewayLossPct: gwLoss,
		SessionUptimeSec: int64(time.Since(a.startedAt).Seconds()),
		InternetIP:       a.InternetIP, InternetRTTms: netRTT, InternetLossPct: netLoss,
		GatewayHist:  a.gwHist.values(),
		InternetHist: a.netHist.values(),
	}
	a.mu.Unlock()

	// Build the matrix OUTSIDE a.mu (linkReport locks a.mu internally).
	a.reportMu.Lock()
	reps := make(map[string]LinkReport, len(a.peerReports))
	for k, v := range a.peerReports {
		reps[k] = v
	}
	a.reportMu.Unlock()
	snap.Matrix = assembleMatrix(a.linkReport(), reps)
	return snap
}

// ResetSession clears all in-memory diagnostics and restarts the session clock,
// so the UI/charts begin fresh. Persisted samples remain on disk (timestamped).
func (a *App) ResetSession() {
	a.peerMu.Lock()
	a.peerStats = make(map[string]*peerStat)
	a.udpStats = make(map[string]*udpStat)
	a.rttHist = make(map[string]*histRing)
	a.lossHist = make(map[string]*histRing)
	a.firstSeen = make(map[string]time.Time)
	a.linkStates = make(map[string]*linkState)
	a.gwHist = newHistRing(histLen)
	a.netHist = newHistRing(histLen)
	a.peerMu.Unlock()

	a.mu.Lock()
	a.startedAt = time.Now()
	a.recent = nil
	a.samples = 0
	a.lastRTTms = 0
	a.mu.Unlock()

	a.reportMu.Lock()
	a.peerReports = make(map[string]LinkReport)
	a.reportMu.Unlock()

	a.recordEvent(true, "session reset")
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
		if a.linksSrv != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = a.linksSrv.Shutdown(ctx)
			cancel()
		}
		if a.disc != nil {
			_ = a.disc.Stop()
		}
		if a.udpEcho != nil {
			_ = a.udpEcho.Close()
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
