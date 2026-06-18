// Package appcore is the in-process engine controller for the portable app: it
// opens the store, starts the bundled iperf3 server, runs a self-probe loop, and
// exposes a thread-safe Snapshot the UI renders. No service, no HTTP server.
package appcore

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"netlogger/internal/appsettings"
	"netlogger/internal/discovery"
	"netlogger/internal/firewall"
	"netlogger/internal/gateway"
	"netlogger/internal/httpauth"
	"netlogger/internal/identity"
	"netlogger/internal/iperf"
	"netlogger/internal/keepawake"
	"netlogger/internal/nicstat"
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
	PreventSleep     bool
	NICs             []NICInfo
	Build            string // this binary's build identity
	BuildWarning     string // set when a peer runs a mismatched build
	LastReset        string // outcome of the most recent ResetAll (empty until one runs)
	// Events is the merged mesh-wide connectivity timeline (this machine + every
	// peer's pulled events), oldest first, host-labeled; the UI renders newest first.
	Events []MergedEvent
}

// NICInfo is one adapter's state plus the discard/error delta since the prior poll.
type NICInfo struct {
	Name             string
	Description      string
	LinkSpeed        string
	Status           string
	Power            string
	RxErrors         int64
	RxDiscards       int64
	TxErrors         int64
	TxDiscards       int64
	RecentRxDiscards int64
	RecentTxDiscards int64
	RecentRxErrors   int64
	RecentTxErrors   int64
}

// sleepKeeper keeps the machine awake; injectable for tests.
type sleepKeeper interface{ Stop() }

// App is the single-machine engine controller.
type App struct {
	dataDir string
	dbPath  string

	// Seams (defaulted in New; overridden in tests).
	Ping        func(addr string, timeout time.Duration) (probe.Result, error)
	StartIperf  func(dataDir string) (stop func(), version string)
	ProbeUDP    func(target string, count int, interval, timeout time.Duration) (probe.UDPStats, error)
	StartKeeper func() sleepKeeper // default wraps keepawake.Start
	tick        time.Duration

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
	FetchLinks func(baseURL string) (LinkReport, error)
	// FetchEvents fetches a peer's recent-event ring; defaults to an HTTP call.
	FetchEvents func(baseURL string) ([]EventInfo, error)
	linksSrv    *http.Server
	reportMu    sync.Mutex
	peerReports map[string]LinkReport
	peerEvents  map[string][]MergedEvent // peer id → that peer's host-stamped events (guarded by reportMu)
	peerHosts   map[string]string        // peer id → host, remembered across the session (guarded by reportMu)
	lastReset   string                   // outcome of the most recent ResetAll, for the UI (guarded by a.mu)

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

	settingsPath string
	sleepMu      sync.Mutex
	preventSleep bool
	keeper       sleepKeeper

	CollectNICs func() []nicstat.NIC // default nicstat.Collect; injectable
	nicTick     time.Duration
	nicMu       sync.Mutex
	nics        []NICInfo

	// eventMu (leaf) guards an in-memory ring of the most recent connectivity
	// events so the UI can show a live log without re-reading the store.
	eventMu sync.Mutex
	events  []EventInfo

	// heatMu (leaf) guards the cross-machine heatmap cache: the params the UI
	// last asked for, and the peer rows the sync loop most recently pulled.
	heatMu         sync.Mutex
	heatFrom       int64
	heatTo         int64
	heatBucket     int
	heatPeerRows   map[string][]HeatRow // peer host → its rows for (heatPeerFrom,heatPeerBucket)
	heatPeerFrom   int64
	heatPeerBucket int
	heatKick       chan struct{} // signals the sync loop to pull immediately on a window change
}

// EventInfo is one connectivity-timeline entry; also the /api/events wire shape.
type EventInfo struct {
	UnixMicro int64  `json:"ts_unix_us"`
	Online    bool   `json:"online"`
	Detail    string `json:"detail"`
}

// MergedEvent is one entry in the mesh-wide timeline, tagged with the host that
// observed it. (Timestamps are each machine's wall clock; cross-machine ordering
// assumes the LAN's clocks are roughly NTP-synced — a clock-offset handshake is
// a future refinement.)
type MergedEvent struct {
	Host      string `json:"host"`
	UnixMicro int64  `json:"ts_unix_us"`
	Online    bool   `json:"online"`
	Detail    string `json:"detail"`
}

const recentWindow = 60

const (
	controlPort     = 8088
	discoveryGroup  = "239.255.74.76"
	discoveryPort   = 48076
	udpEchoPort     = 8089
	udpProbeCount   = 200
	internetTarget  = "8.8.8.8"
	histLen         = 120
	nicPollInterval = 8 * time.Second
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
	Build        string // peer's binary build id (from its pulled link report), for skew checks
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
	settingsPath := appsettings.Path(dataDir)
	return &App{
		dataDir:     dataDir,
		dbPath:      filepath.Join(dataDir, "netlogger.db"),
		Ping:        probe.PingICMP,
		StartIperf:  defaultStartIperf,
		ProbeUDP:    probe.ProbeUDP,
		StartKeeper: func() sleepKeeper { return keepawake.Start() },
		tick:        time.Second,
		CollectNICs: nicstat.Collect,
		nicTick:     nicPollInterval,
		peerStats:   make(map[string]*peerStat),
		udpStats:    make(map[string]*udpStat),
		linkStates:  make(map[string]*linkState),
		peerReports: make(map[string]LinkReport),
		peerEvents:  make(map[string][]MergedEvent),
		peerHosts:   make(map[string]string),
		heatKick:    make(chan struct{}, 1),
		FetchLinks: func(baseURL string) (LinkReport, error) {
			return fetchLinks(&http.Client{Timeout: 1500 * time.Millisecond}, baseURL)
		},
		FetchEvents: func(baseURL string) ([]EventInfo, error) {
			return fetchEvents(&http.Client{Timeout: 1500 * time.Millisecond}, baseURL)
		},
		InternetIP:   internetTarget,
		firstSeen:    make(map[string]time.Time),
		rttHist:      make(map[string]*histRing),
		lossHist:     make(map[string]*histRing),
		gwHist:       newHistRing(histLen),
		netHist:      newHistRing(histLen),
		settingsPath: settingsPath,
		preventSleep: appsettings.Load(settingsPath).PreventSleep,
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
		// /api/command is intentionally open on the trusted LAN (httpauth uses an
		// empty token, so only the Host-allowlist applies): any peer may trigger a
		// synchronized session reset. Acceptable for a LAN diagnostic tool.
		mux.Handle("/api/command", commandHandler(a.ResetSession))
		mux.Handle("/api/events", eventsHandler(a.recentEvents))
		mux.Handle("/api/lossbuckets", lossBucketsHandler(a.LossHeat))
		a.linksSrv = &http.Server{Addr: "0.0.0.0:" + strconv.Itoa(controlPort), Handler: httpauth.Middleware("")(mux)}
		go func() { _ = a.linksSrv.ListenAndServe() }()
	}

	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.wg.Add(6)
	go a.probeLoop(ctx)
	go a.peerLoop(ctx)
	go a.udpLoop(ctx)
	go a.linkPullLoop(ctx)
	go a.nicLoop(ctx)
	go a.heatSyncLoop(ctx)

	a.sleepMu.Lock()
	if a.preventSleep && a.keeper == nil {
		a.keeper = a.StartKeeper()
	}
	a.sleepMu.Unlock()

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
				_, _ = a.store.Insert(store.Sample{TSUnixUS: time.Now().UnixMicro(), ProbeType: "icmp", SrcHost: a.nodeID, DstHost: "__gateway__", Direction: "rtt", RTTus: res.RTT.Microseconds(), Lost: !ok})
			}
			if a.InternetIP != "" {
				res, err := a.Ping(a.InternetIP, 2*time.Second)
				ok := err == nil && !res.Lost
				a.statFor("__internet__").record(ok, float64(res.RTT.Microseconds())/1000.0)
				if ok {
					a.netHist.push(float64(res.RTT.Microseconds()) / 1000.0)
				}
				_, _ = a.store.Insert(store.Sample{TSUnixUS: time.Now().UnixMicro(), ProbeType: "icmp", SrcHost: a.nodeID, DstHost: "__internet__", Direction: "rtt", RTTus: res.RTT.Microseconds(), Lost: !ok})
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

// hostName returns this node's hostname (valid after Start).
func (a *App) hostName() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.host
}

// linkReport builds this node's current outbound link report from its UDP stats.
// rememberPeer records a peer id→host mapping so historical samples (e.g. in the
// loss heatmap) resolve to a name even after the peer goes away.
func (a *App) rememberPeer(id, host string) {
	if id == "" || host == "" {
		return
	}
	a.reportMu.Lock()
	a.peerHosts[id] = host
	a.reportMu.Unlock()
}

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
	return LinkReport{NodeID: id, Host: host, Build: version.Build, Links: links}
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
				a.rememberPeer(p.ID, p.Host)
				rep, err := a.FetchLinks("http://" + p.Addr)
				if err == nil && rep.NodeID != "" {
					a.reportMu.Lock()
					a.peerReports[rep.NodeID] = rep
					a.reportMu.Unlock()
				}
				if ctx.Err() != nil {
					return // don't start a second fetch if we're shutting down
				}
				if evs, err := a.FetchEvents("http://" + p.Addr); err == nil {
					me := make([]MergedEvent, len(evs))
					for i, e := range evs {
						me[i] = MergedEvent{Host: peerLabel(p), UnixMicro: e.UnixMicro, Online: e.Online, Detail: e.Detail}
					}
					a.reportMu.Lock()
					a.peerEvents[p.ID] = me
					a.reportMu.Unlock()
				}
			}
		}
	}
}

func (a *App) nicLoop(ctx context.Context) {
	defer a.wg.Done()
	prev := map[string]nicstat.NIC{}
	discarding := map[string]bool{} // per-adapter discard-episode edge state
	poll := func() {
		raw := a.CollectNICs()
		// Record link-state / speed / error changes against the prior poll
		// BEFORE prev is updated, so they land on the connectivity timeline
		// next to the loss episodes for correlation.
		for _, ev := range nicEvents(prev, raw, discarding) {
			a.recordEvent(ev.online, ev.detail)
		}
		out := make([]NICInfo, 0, len(raw))
		for _, n := range raw {
			p := prev[n.Name]
			out = append(out, NICInfo{
				Name: n.Name, Description: n.Description, LinkSpeed: n.LinkSpeed, Status: n.Status, Power: n.Power,
				RxErrors: n.RxErrors, RxDiscards: n.RxDiscards, TxErrors: n.TxErrors, TxDiscards: n.TxDiscards,
				RecentRxDiscards: nonNeg(n.RxDiscards - p.RxDiscards),
				RecentTxDiscards: nonNeg(n.TxDiscards - p.TxDiscards),
				RecentRxErrors:   nonNeg(n.RxErrors - p.RxErrors),
				RecentTxErrors:   nonNeg(n.TxErrors - p.TxErrors),
			})
			prev[n.Name] = n
		}
		a.nicMu.Lock()
		a.nics = out
		a.nicMu.Unlock()
	}
	poll() // once at startup (prev empty → deltas are the full counts on the 2nd poll)
	t := time.NewTicker(a.nicTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			poll()
		}
	}
}

func nonNeg(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
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
		Build:        version.Build,
		LastReset:    a.lastReset,
	}
	selfHost := a.host
	a.mu.Unlock()

	// Build the matrix + merged event timeline OUTSIDE a.mu (linkReport locks
	// a.mu internally).
	a.reportMu.Lock()
	reps := make(map[string]LinkReport, len(a.peerReports))
	for k, v := range a.peerReports {
		reps[k] = v
	}
	peerEvs := make([][]MergedEvent, 0, len(a.peerEvents))
	for _, v := range a.peerEvents {
		peerEvs = append(peerEvs, v)
	}
	a.reportMu.Unlock()
	snap.Matrix = assembleMatrix(a.linkReport(), reps)
	snap.BuildWarning = buildWarning(version.Build, reps)
	for i := range snap.Peers {
		if r, ok := reps[snap.Peers[i].ID]; ok {
			snap.Peers[i].Build = r.Build
		}
	}
	snap.Events = mergeEvents(selfHost, a.recentEvents(), peerEvs, eventRingCap)

	a.sleepMu.Lock()
	preventSleep := a.preventSleep
	a.sleepMu.Unlock()
	snap.PreventSleep = preventSleep

	a.nicMu.Lock()
	nics := a.nics
	a.nicMu.Unlock()
	snap.NICs = nics

	return snap
}

// HeatRow is one link's loss timeline; Loss[i] is the loss% in bucket i (-1 = no data).
type HeatRow struct {
	Label string    `json:"label"`
	Loss  []float64 `json:"loss"`
}

// HeatView is the time-normalized loss heatmap over [FromUnix, FromUnix+Buckets*BucketSec).
type HeatView struct {
	FromUnix  int64     `json:"from_unix"`
	BucketSec int       `json:"bucket_sec"`
	Buckets   int       `json:"buckets"`
	Rows      []HeatRow `json:"rows"`
}

// LossHeat builds the per-link loss heatmap for [fromUnix, toUnix) at bucketSec
// resolution, every row aligned to the same bucket grid (Gateway, Internet, then
// peers) so the UI can stack them on one time axis.
func (a *App) LossHeat(fromUnix, toUnix int64, bucketSec int) HeatView {
	view := HeatView{FromUnix: fromUnix, BucketSec: bucketSec}
	if a.store == nil || bucketSec <= 0 || toUnix <= fromUnix {
		return view
	}
	m, err := a.store.LossBuckets(fromUnix*1_000_000, toUnix*1_000_000, bucketSec)
	if err != nil {
		return view
	}
	view.Buckets = int((toUnix - fromUnix + int64(bucketSec) - 1) / int64(bucketSec))

	a.mu.Lock()
	disc := a.Discovery
	a.mu.Unlock()
	idToHost := map[string]string{}
	a.reportMu.Lock()
	for id, host := range a.peerHosts { // remembered across the session
		idToHost[id] = host
	}
	a.reportMu.Unlock()
	if disc != nil {
		for _, p := range disc.Peers() {
			idToHost[p.ID] = p.Host
		}
	}

	add := func(key, label string) {
		if loss, ok := m[key]; ok {
			view.Rows = append(view.Rows, HeatRow{Label: label, Loss: loss})
		}
	}
	add("__gateway__", "Gateway")
	add("__internet__", "Internet")
	if ls, err := a.store.LinkStateBuckets(fromUnix*1_000_000, toUnix*1_000_000, bucketSec, a.NodeID()); err == nil {
		for _, x := range ls {
			if x >= 0 { // at least one flap → show the marker row
				view.Rows = append(view.Rows, HeatRow{Label: "NIC link", Loss: ls})
				break
			}
		}
	}
	var peers []HeatRow
	for key, loss := range m {
		if key == "__gateway__" || key == "__internet__" || key == "self" {
			continue
		}
		label := idToHost[key]
		if label == "" {
			label = key
			if len(label) > 8 {
				label = label[:8]
			}
		}
		peers = append(peers, HeatRow{Label: label, Loss: loss})
	}
	sort.Slice(peers, func(i, j int) bool { return peers[i].Label < peers[j].Label })
	view.Rows = append(view.Rows, peers...)
	return view
}

// ResetSession clears all in-memory diagnostics and restarts the session clock,
// so the UI/charts begin fresh. Persisted samples remain on disk (timestamped).
func (a *App) ResetSession() {
	// Clear maps/rings IN PLACE (never reassign the field words): readers like
	// histFor and gwHist.push read these field references outside a.peerMu, so
	// rewriting a field header/pointer would be a data race. Deleting keys and
	// resetting rings keeps the same identities; all content access stays under
	// a.peerMu / the ring's own lock.
	a.peerMu.Lock()
	for k := range a.peerStats {
		delete(a.peerStats, k)
	}
	for k := range a.udpStats {
		delete(a.udpStats, k)
	}
	for k := range a.rttHist {
		delete(a.rttHist, k)
	}
	for k := range a.lossHist {
		delete(a.lossHist, k)
	}
	for k := range a.firstSeen {
		delete(a.firstSeen, k)
	}
	for k := range a.linkStates {
		delete(a.linkStates, k)
	}
	a.gwHist.reset()
	a.netHist.reset()
	a.peerMu.Unlock()

	a.mu.Lock()
	a.startedAt = time.Now()
	a.recent = nil
	a.samples = 0
	a.lastRTTms = 0
	a.mu.Unlock()

	a.reportMu.Lock()
	for k := range a.peerReports {
		delete(a.peerReports, k)
	}
	for k := range a.peerEvents {
		delete(a.peerEvents, k)
	}
	a.reportMu.Unlock()

	a.nicMu.Lock()
	a.nics = nil
	a.nicMu.Unlock()

	a.eventMu.Lock()
	a.events = nil
	a.eventMu.Unlock()

	a.recordEvent(true, "session reset")
}

// ResetAll restarts the logging session on every discovered peer and on this
// machine, so the whole mesh begins a fresh session together.
func (a *App) ResetAll() {
	a.mu.Lock()
	disc := a.Discovery
	a.mu.Unlock()
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	acked, total := 0, 0
	var notes []string
	if disc != nil {
		for _, p := range disc.Peers() {
			total++
			if err := postCommand(client, "http://"+p.Addr, "reset"); err != nil {
				label := p.Host
				if label == "" {
					label = p.ID
				}
				notes = append(notes, label+" did not ack (unreachable or old build — redeploy)")
			} else {
				acked++
			}
		}
	}
	a.ResetSession()
	summary := resetSummary(acked, total, notes)
	a.mu.Lock()
	a.lastReset = summary
	a.mu.Unlock()
}

// resetSummary renders the outcome of a ResetAll for the status line: how many
// peers acknowledged the reset, plus any per-peer failure notes.
func resetSummary(acked, total int, notes []string) string {
	s := fmt.Sprintf("reset: this machine + %d/%d peers", acked, total)
	if len(notes) > 0 {
		s += " · " + strings.Join(notes, "; ")
	}
	return s
}

// SetPreventSleep turns the keep-awake behavior on/off at runtime and persists it.
func (a *App) SetPreventSleep(on bool) {
	a.sleepMu.Lock()
	a.preventSleep = on
	if on && a.keeper == nil {
		a.keeper = a.StartKeeper()
	} else if !on && a.keeper != nil {
		a.keeper.Stop()
		a.keeper = nil
	}
	path := a.settingsPath
	a.sleepMu.Unlock()
	_ = appsettings.Save(path, appsettings.Settings{PreventSleep: on})
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
		a.sleepMu.Lock()
		if a.keeper != nil {
			a.keeper.Stop()
			a.keeper = nil
		}
		a.sleepMu.Unlock()
	})
	return err
}
