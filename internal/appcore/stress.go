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
)

var timeNow = time.Now

const stressAbortAfter = 3 // consecutive iperf3 client errors against a target → abort that link

// StressOpts is a node-local stress command: load these targets at a per-link
// cap for a bounded duration, starting at an absolute wall-clock time.
type StressOpts struct {
	RunID          string   `json:"run_id"`
	Targets        []string `json:"targets"`
	PerLinkCapMbit int      `json:"per_link_cap_mbit"`
	Proto          string   `json:"proto"`
	DurationS      int      `json:"duration_s"`
	StartAtUnixUS  int64    `json:"start_at_unix_us"`
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

// StressStatus is a node's current stress state.
type StressStatus struct {
	RunID         string     `json:"run_id"`
	Running       bool       `json:"running"`
	StartedUnixUS int64      `json:"started_unix_us"`
	EndsUnixUS    int64      `json:"ends_unix_us"`
	Links         []LinkLoad `json:"links"`
}

// meshTargets maps every node id → the bare hosts of all OTHER nodes (full mesh).
func meshTargets(self PeerInfo, peers []PeerInfo) map[string][]string {
	all := append([]PeerInfo{self}, peers...)
	out := make(map[string][]string, len(all))
	for _, n := range all {
		var ts []string
		for _, other := range all {
			if other.ID != n.ID {
				ts = append(ts, iperfHost(other.Addr))
			}
		}
		out[n.ID] = ts
	}
	return out
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
// list. A loopback target would make this node stress-load itself at memory
// speed — always a misrouted mesh, never a LAN measurement.
func sanitizeTargets(ts []string) []string {
	seen := make(map[string]bool, len(ts))
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		if t == "" || seen[t] || isLoopbackTarget(t) {
			continue
		}
		seen[t] = true
		out = append(out, t)
		if len(out) >= stressMaxTargets {
			break
		}
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
	finalize func() // closes the heatmap test window when the run ends
}

// startStressLocal schedules and launches the load goroutines. Rejects a start
// while a run is already active.
func (a *App) startStressLocal(o StressOpts) error {
	// Sanitize peer-supplied inputs before anything else.
	targets := sanitizeTargets(o.Targets)
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

	for _, tg := range targets {
		tg := tg
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
			a.loadTarget(ctx, run, tg, capMbit, dur, proto)
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
func (a *App) loadTarget(ctx context.Context, run *stressRun, target string, capMbit, dur int, proto string) {
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
	a.stressMu.Lock()
	run := a.stress
	a.stressMu.Unlock()
	if run == nil {
		return StressStatus{}
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	st := StressStatus{
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
	targets := meshTargets(self, peers)
	byID := map[string]PeerInfo{self.ID: self}
	for _, pr := range peers {
		byID[pr.ID] = pr
	}
	mk := func(id string) StressOpts {
		return StressOpts{
			RunID: runID, Targets: targets[id], PerLinkCapMbit: p.PerLinkCapMbit,
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
