package appcore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"sync"
	"time"

	"netlogger/internal/iperf"
)

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

// LinkLoad is one target's live load/health during a run.
type LinkLoad struct {
	Target      string  `json:"target"`
	SentMbit    float64 `json:"sent_mbit"`
	Retransmits int     `json:"retransmits"`
	Aborted     bool    `json:"aborted"`
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

// stressRun is one active stress run on this node.
type stressRun struct {
	runID   string
	cancel  context.CancelFunc
	starts  int64
	ends    int64
	mu      sync.Mutex
	links   map[string]*LinkLoad
	wg      sync.WaitGroup
	running bool
}

// startStressLocal schedules and launches the load goroutines. Rejects a start
// while a run is already active.
func (a *App) startStressLocal(o StressOpts) error {
	a.stressMu.Lock()
	defer a.stressMu.Unlock()
	if a.stress != nil && a.stress.running {
		return errStressBusy
	}
	dur := o.DurationS
	if dur <= 0 || dur > 600 {
		dur = 60
	}
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now().UnixMicro()
	run := &stressRun{
		runID:  o.RunID,
		cancel: cancel,
		links:  make(map[string]*LinkLoad, len(o.Targets)),
	}
	for _, tg := range o.Targets {
		run.links[tg] = &LinkLoad{Target: tg}
	}
	a.stress = run

	capMbit := o.PerLinkCapMbit
	proto := o.Proto
	delay := time.Duration(startDelay(now, o.StartAtUnixUS)) * time.Microsecond

	run.running = true
	for _, tg := range o.Targets {
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

	go func() {
		select {
		case <-ctx.Done():
		case <-time.After(delay + time.Duration(dur)*time.Second):
			cancel()
		}
		run.wg.Wait()
		a.stressMu.Lock()
		run.mu.Lock()
		run.running = false
		run.mu.Unlock()
		a.stressMu.Unlock()
	}()
	return nil
}

// loadTarget repeatedly runs a capped iperf3 client against one target until ctx
// is done, updating the link's live load. Consecutive errors auto-abort the link.
func (a *App) loadTarget(ctx context.Context, run *stressRun, target string, capMbit, dur int, proto string) {
	var consec int
	chunk := 5
	if dur < chunk {
		chunk = dur
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
		})
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
		l := run.links[tg]
		st.Links = append(st.Links, *l)
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
