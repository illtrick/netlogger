package appcore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"netlogger/internal/iperf"
	"netlogger/internal/version"
)

var timeNow = time.Now

const stressAbortAfter = 3 // consecutive iperf3 client errors against a target → abort that link

// StressOpts is a node-local stress command: load these targets at a per-link
// cap for a bounded duration, starting at an absolute wall-clock time.
type StressOpts struct {
	RunID   string   `json:"run_id"`
	Targets []string `json:"targets"`
	// TargetPorts aligns 1:1 with Targets: the iperf3 server port to hit on
	// each target (0/absent = default 5201). Assigned so no two clients share
	// one target's server — iperf3 serves ONE test at a time, so a full mesh
	// of N>=3 on a single port instantly aborts one inbound link per node
	// with "server is busy".
	TargetPorts []int `json:"target_ports,omitempty"`
	// ListenPorts are EXTRA iperf3 servers this node must run for the run's
	// duration (beyond the always-on 5201) so each inbound client has its own.
	ListenPorts    []int  `json:"listen_ports,omitempty"`
	PerLinkCapMbit int    `json:"per_link_cap_mbit"`
	Proto          string `json:"proto"`
	DurationS      int    `json:"duration_s"`
	StartAtUnixUS  int64  `json:"start_at_unix_us"`
}

// stressRateHist is how many per-second rate samples a link keeps for the
// throughput-under-load chart (one minute at 1 Hz).
const stressRateHist = 60

// LinkLoad is one target's live load/health during a run. SentMbit is the
// latest per-second rate (streamed live from iperf3); RateHist is the recent
// per-second series for charting.
type LinkLoad struct {
	Target      string    `json:"target"`
	SentMbit    float64   `json:"sent_mbit"`
	Retransmits int       `json:"retransmits"`
	RateHist    []float64 `json:"rate_hist,omitempty"`
	Aborted     bool      `json:"aborted"`
}

// StressStatus is a node's current stress state. Host names the reporting
// node — its links' SOURCE — so the UI can label each directed link
// "source → target" (every device is a target twice in a 3-node mesh;
// destination-only rows read as duplicates). Empty on pre-1.3.3 peers.
type StressStatus struct {
	Host          string     `json:"host,omitempty"`
	RunID         string     `json:"run_id"`
	Running       bool       `json:"running"`
	StartedUnixUS int64      `json:"started_unix_us"`
	EndsUnixUS    int64      `json:"ends_unix_us"`
	Links         []LinkLoad `json:"links"`
}

// stressAssignment is one node's marching orders in a full-mesh run: what to
// load (host + that host's assigned server port) and which extra iperf3
// servers to open for its own inbound clients.
type stressAssignment struct {
	Targets     []string
	TargetPorts []int
	ListenPorts []int
}

// meshAssignments maps every node id → its full-mesh assignment. iperf3
// accepts ONE test at a time, so for each target its inbound clients (in
// sorted node-id order, deterministic on every node) get distinct ports
// 5201, 5202, …; ports beyond 5201 become that target's extra listeners.
// usePorts=false collapses to the legacy single-port layout (all 5201, no
// extra listeners) for meshes that still contain pre-1.3.1 nodes — those
// would never open the extra ports, and "connection refused" is worse than
// the busy-collision it replaces.
func meshAssignments(self PeerInfo, peers []PeerInfo, usePorts bool) map[string]stressAssignment {
	all := append([]PeerInfo{self}, peers...)
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	out := make(map[string]stressAssignment, len(all))
	for _, target := range all {
		idx := 0
		for _, client := range all {
			if client.ID == target.ID {
				continue
			}
			port := iperf.DefaultServerPort
			if usePorts {
				port += idx
			}
			ca := out[client.ID]
			ca.Targets = append(ca.Targets, iperfHost(target.Addr))
			ca.TargetPorts = append(ca.TargetPorts, port)
			out[client.ID] = ca
			if port != iperf.DefaultServerPort {
				ta := out[target.ID]
				ta.ListenPorts = append(ta.ListenPorts, port)
				out[target.ID] = ta
			}
			idx++
		}
	}
	return out
}

// portsSupported reports whether every participant speaks target_ports —
// i.e. runs the same release as this node. A new client told to hit an old
// target on 5202 would get connection-refused (the old node never opens it).
func portsSupported(peers []PeerInfo, selfVersion string) bool {
	for _, p := range peers {
		if p.Version != selfVersion {
			return false
		}
	}
	return true
}

// shouldAbort reports whether a link's consecutive-error count warrants aborting it.
func shouldAbort(consecutiveErrs int) bool { return consecutiveErrs >= stressAbortAfter }

// startDelay returns the microseconds to wait before starting, given now and the
// scheduled absolute start. Clamped to >= 0.
func startDelay(nowUS, startAtUS int64) int64 {
	if d := startAtUS - nowUS; d > 0 {
		return d
	}
	return 0
}

var errStressBusy = errors.New("a stress run is already active")

const (
	stressMaxTargets = 64               // cap a peer-supplied target list (goroutines + iperf3 processes each)
	stressMaxDelay   = 10 * time.Second // the protocol schedules 2s ahead; clock skew must not wedge the node
)

// sanitizeTargets dedupes, drops loopbacks, and caps a peer-supplied target
// list, keeping the parallel port list aligned (missing/short ports → 0 =
// iperf3 default). A loopback target would make this node stress-load itself
// at memory speed — always a misrouted mesh, never a LAN measurement.
func sanitizeTargets(ts []string, ports []int) ([]string, []int) {
	seen := make(map[string]bool, len(ts))
	outT := make([]string, 0, len(ts))
	outP := make([]int, 0, len(ts))
	for i, t := range ts {
		if t == "" || seen[t] || isLoopbackTarget(t) {
			continue
		}
		seen[t] = true
		p := 0
		if i < len(ports) {
			p = ports[i]
		}
		outT = append(outT, t)
		outP = append(outP, p)
		if len(outT) >= stressMaxTargets {
			break
		}
	}
	return outT, outP
}

// sanitizeListenPorts bounds a peer-supplied extra-listener list: only ports
// just above the default (a mesh of stressMaxTargets nodes never needs more),
// deduped.
func sanitizeListenPorts(ps []int) []int {
	seen := make(map[int]bool, len(ps))
	out := make([]int, 0, len(ps))
	for _, p := range ps {
		if p <= iperf.DefaultServerPort || p > iperf.DefaultServerPort+stressMaxTargets || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// stressRun is one active stress run on this node.
type stressRun struct {
	runID    string
	cancel   context.CancelFunc
	starts   int64
	ends     int64
	mu       sync.Mutex
	links    map[string]*LinkLoad
	wg       sync.WaitGroup
	running  bool
	finalize func()   // closes the heatmap test window when the run ends
	srvStops []func() // stops for this run's extra iperf3 listeners
}

// startStressLocal schedules and launches the load goroutines. Rejects a start
// while a run is already active.
func (a *App) startStressLocal(o StressOpts) error {
	// Sanitize peer-supplied inputs before anything else.
	targets, ports := sanitizeTargets(o.Targets, o.TargetPorts)
	listenPorts := sanitizeListenPorts(o.ListenPorts)
	dur := o.DurationS
	if dur <= 0 || dur > 600 {
		dur = 60
	}

	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now().UnixMicro()
	run := &stressRun{
		runID:  o.RunID,
		cancel: cancel,
		links:  make(map[string]*LinkLoad, len(targets)),
	}
	for _, tg := range targets {
		run.links[tg] = &LinkLoad{Target: tg}
	}
	run.running = true

	a.stressMu.Lock()
	if a.stress != nil && a.stress.running {
		a.stressMu.Unlock()
		cancel()
		return errStressBusy
	}
	a.stress = run
	a.stressMu.Unlock()

	// Extra iperf3 servers for this run's inbound clients (the always-on 5201
	// serves the first; iperf3 handles one test at a time, so every additional
	// concurrent client needs its own listener). Spawned only after the busy
	// check so a rejected start never leaks processes; stopped when the run ends.
	srv := a.stressSrv
	if srv == nil {
		srv = func(port int) func() {
			s := iperf.StartServer(port)
			if s == nil {
				return nil
			}
			return s.Stop
		}
	}
	for _, p := range listenPorts {
		if stop := srv(p); stop != nil {
			run.srvStops = append(run.srvStops, stop)
		}
	}

	// Event + heatmap span OUTSIDE stressMu: recordEvent does a synchronous
	// SQLite insert that must not stall /api/stress/status or stop requests.
	a.recordTestEvent("stress test started (full mesh, " + strconv.Itoa(o.PerLinkCapMbit) + " Mbit/s)")
	run.finalize = a.markTestSpan("stress test", int64(dur)*1000)

	capMbit := o.PerLinkCapMbit
	proto := o.Proto
	delay := time.Duration(startDelay(now, o.StartAtUnixUS)) * time.Microsecond
	if delay > stressMaxDelay {
		delay = stressMaxDelay // a skewed orchestrator clock must not wedge the node in a silent "running" state
	}

	for i, tg := range targets {
		tg, port := tg, ports[i]
		run.wg.Add(1)
		go func() {
			defer run.wg.Done()
			if delay > 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(delay):
				}
			}
			run.mu.Lock()
			if run.starts == 0 {
				run.starts = time.Now().UnixMicro()
				run.ends = run.starts + int64(dur)*1_000_000
			}
			run.mu.Unlock()
			a.loadTarget(ctx, run, tg, port, capMbit, dur, proto)
		}()
	}

	a.stressWG.Add(1)
	go func() {
		defer a.stressWG.Done()
		select {
		case <-ctx.Done():
		case <-time.After(delay + time.Duration(dur)*time.Second):
			cancel()
		}
		run.wg.Wait()
		for _, stop := range run.srvStops { // the run's extra listeners die with it
			stop()
		}
		// Free the busy slot BEFORE the bookkeeping below: finalize/recordEvent
		// write to SQLite, and an immediate restart must not be spuriously
		// rejected while that happens.
		a.stressMu.Lock()
		run.mu.Lock()
		run.running = false
		run.mu.Unlock()
		a.stressMu.Unlock()
		if run.finalize != nil {
			run.finalize()
		}
		a.recordTestEvent("stress test ended")
	}()
	return nil
}

// loadTarget repeatedly runs a capped iperf3 client against one target until ctx
// is done, updating the link's live load: each streamed 1s interval refreshes
// SentMbit and extends RateHist, so the UI's bars and charts move every second
// instead of every 5s chunk. Consecutive errors auto-abort the link.
func (a *App) loadTarget(ctx context.Context, run *stressRun, target string, port, capMbit, dur int, proto string) {
	var consec int
	chunk := 5
	if dur < chunk {
		chunk = dur
	}
	onIv := func(iv iperf.Interval) {
		run.mu.Lock()
		l := run.links[target]
		l.SentMbit = math.Round(iv.BitsPerSecond/1e6*10) / 10
		l.RateHist = append(l.RateHist, l.SentMbit)
		if len(l.RateHist) > stressRateHist {
			l.RateHist = l.RateHist[len(l.RateHist)-stressRateHist:]
		}
		run.mu.Unlock()
	}
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		res, err := a.stressRunner(ctx, target, iperf.Opts{
			DurationS:   chunk,
			BitrateMbit: capMbit,
			UDP:         proto == "udp",
			Port:        port, // 0 = default 5201; assigned per link on 1.3.1+ meshes
		}, onIv)
		if ctx.Err() != nil {
			return
		}
		run.mu.Lock()
		l := run.links[target]
		if err != nil {
			consec++
			if shouldAbort(consec) {
				l.Aborted = true
				run.mu.Unlock()
				return
			}
		} else {
			consec = 0
			l.SentMbit = math.Round(res.SumBitsPerSec/1e6*10) / 10
			// Retransmits from the end summary only (interval sums lack them on
			// Windows/Cygwin) — no double counting.
			l.Retransmits += res.SumRetransmits
		}
		run.mu.Unlock()
	}
}

// stopStressLocal cancels the active run if its id matches (empty id = any).
func (a *App) stopStressLocal(runID string) {
	a.stressMu.Lock()
	run := a.stress
	a.stressMu.Unlock()
	if run == nil {
		return
	}
	if runID != "" && run.runID != runID {
		return
	}
	run.cancel()
}

// stressStatusLocal returns a snapshot of the current run (empty if none).
func (a *App) stressStatusLocal() StressStatus {
	a.mu.Lock()
	host := a.host
	a.mu.Unlock()
	a.stressMu.Lock()
	run := a.stress
	a.stressMu.Unlock()
	if run == nil {
		return StressStatus{}
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	st := StressStatus{
		Host:  host,
		RunID: run.runID, Running: run.running,
		StartedUnixUS: run.starts, EndsUnixUS: run.ends,
	}
	for _, tg := range sortedKeys(run.links) {
		l := *run.links[tg]
		// Deep-copy the rate history: the loader goroutine keeps appending to
		// the original slice under run.mu after we return.
		l.RateHist = append([]float64(nil), l.RateHist...)
		st.Links = append(st.Links, l)
	}
	return st
}

func sortedKeys(m map[string]*LinkLoad) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// stressStartHandler accepts POST StressOpts and starts a local run.
func stressStartHandler(start func(StressOpts) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var o StressOpts
		if err := json.NewDecoder(r.Body).Decode(&o); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if err := start(o); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

type stressStopReq struct {
	RunID string `json:"run_id"`
}

// stressStopHandler accepts POST {run_id} and cancels the matching run.
func stressStopHandler(stop func(string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req stressStopReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		stop(req.RunID)
		w.WriteHeader(http.StatusOK)
	}
}

// stressStatusHandler returns the node's current StressStatus.
func stressStatusHandler(status func() StressStatus) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(status())
	}
}

func postStressStart(client *http.Client, baseURL string, o StressOpts) error {
	body, _ := json.Marshal(o)
	resp, err := client.Post(baseURL+"/api/stress/start", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("stress start: status %d", resp.StatusCode)
	}
	return nil
}

func postStressStop(client *http.Client, baseURL, runID string) error {
	body, _ := json.Marshal(stressStopReq{RunID: runID})
	resp, err := client.Post(baseURL+"/api/stress/stop", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func fetchStressStatus(client *http.Client, baseURL string) (StressStatus, error) {
	resp, err := client.Get(baseURL + "/api/stress/status")
	if err != nil {
		return StressStatus{}, err
	}
	defer resp.Body.Close()
	var st StressStatus
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return StressStatus{}, err
	}
	return st, nil
}

// StressParams is the orchestrator-level request (topology is full mesh in v1).
type StressParams struct {
	PerLinkCapMbit int
	Proto          string
	DurationS      int
	NowUnixUS      int64 // injected for tests; 0 => time.Now()
}

// StartStress fans a full-mesh stress run out to every node with a shared run id
// and a start time 2 seconds ahead so all nodes begin together. It returns the
// run id and how many nodes accepted the start — 0 means nothing is running
// (e.g. the local node was still busy and every peer was unreachable), which the
// caller must surface instead of pretending a run began.
func (a *App) StartStress(self PeerInfo, peers []PeerInfo, p StressParams) (string, int) {
	now := p.NowUnixUS
	if now == 0 {
		now = timeNow().UnixMicro()
	}
	runID := "stress-" + strconv.FormatInt(now, 10)
	startAt := now + 2_000_000
	assignments := meshAssignments(self, peers, portsSupported(peers, version.Version))
	byID := map[string]PeerInfo{self.ID: self}
	for _, pr := range peers {
		byID[pr.ID] = pr
	}
	mk := func(id string) StressOpts {
		asg := assignments[id]
		return StressOpts{
			RunID: runID, Targets: asg.Targets, TargetPorts: asg.TargetPorts,
			ListenPorts: asg.ListenPorts, PerLinkCapMbit: p.PerLinkCapMbit,
			Proto: p.Proto, DurationS: p.DurationS, StartAtUnixUS: startAt,
		}
	}
	local := a.startLocalStress
	if local == nil {
		local = a.startStressLocal
	}
	started := 0
	for id, node := range byID {
		o := mk(id)
		var err error
		if id == a.nodeID {
			err = local(o)
		} else {
			err = a.FetchStressStart("http://"+node.Addr, o)
		}
		if err == nil {
			started++
		} else {
			log.Printf("stress start on %s: %v", node.Host, err)
		}
	}
	return runID, started
}

// StopStress fans a stop to self + every peer.
func (a *App) StopStress(self PeerInfo, peers []PeerInfo, runID string) {
	a.stopStressLocal(runID)
	for _, p := range peers {
		_ = a.FetchStressStop("http://"+p.Addr, runID)
	}
}

// PollStress aggregates each node's status (self + peers).
func (a *App) PollStress(self PeerInfo, peers []PeerInfo) []StressStatus {
	out := []StressStatus{a.stressStatusLocal()}
	for _, p := range peers {
		if st, err := a.FetchStressStatus("http://" + p.Addr); err == nil {
			out = append(out, st)
		}
	}
	return out
}
