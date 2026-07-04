# Mesh Stress Test Implementation Plan (Build #2)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** A coordinated, full-mesh stress test: every node loads every other node with rate-capped iperf3 traffic for a bounded duration, while the existing heatmap keeps measuring latency/loss — reproducing load-triggered faults. Includes a manual kill-switch, a hard duration cap, and auto-abort of a link that hard-faults.

**Architecture:** Reuse the per-agent control plane and always-on `iperf3 -s`. The orchestrator fans `/api/stress/start` to every node with a shared near-future `start_at` and that node's target list (full mesh = all other nodes). Each node runs one rate-capped, context-cancellable iperf3 client per target in a goroutine, tracking per-link load/health. `/api/stress/stop` cancels; `/api/stress/status` reports. The live readout is the existing heatmap plus a per-link status strip.

**Tech Stack:** Go (cgo-free → no `-race`; pure logic TDD'd, goroutine glue reasoned by inspection + lifecycle tests), Gio v0.10.0, bundled iperf3 3.21.

**Scope note:** Build #2 of the Tests subsystem spec (`docs/superpowers/specs/2026-06-18-netlogger-tests-design.md` §4.3). Builds on Build #1 (`Speed*` types, the `/api/speedtest` pattern, `iperf.Opts`). v1 uses an **absolute `start_at`** (machines are NTP-synced on a LAN, and the heatmap aligns by absolute time regardless); M3 clock-offset correction is a noted refinement, not in this plan.

---

## File Structure

**Modify:**
- `internal/iperf/iperf.go` — add `RunClientCtx` (context-cancellable); emit `-b` rate cap for TCP too.
- `internal/appcore/appcore.go` — `App` gains `stressMu`/`stress` + seams; mount endpoints in `Start`; cancel stress on shutdown.
- `internal/ui/ui.go` — route the Stress sub-view; Start/Stop click handling.
- `internal/ui/tests.go` — `testsState` gains stress fields; segmented sub-view switch; `layoutStress`.

**Create:**
- `internal/appcore/stress.go` — types, pure helpers, `stressRun` + load goroutine, node-local start/stop/status, handlers + clients, orchestrator fan-out.
- `internal/appcore/stress_test.go` — pure + httptest + lifecycle coverage.

---

## Task 1: iperf killable runner + TCP rate cap

**Files:**
- Modify: `internal/iperf/iperf.go`
- Test: `internal/iperf/speedargs_test.go`

- [ ] **Step 1: Write the failing test** (append):

```go
func TestBuildArgsTCPRateCap(t *testing.T) {
	// A TCP run with a bitrate cap must emit -b (application pacing), not only UDP.
	got := buildArgs("h", Opts{DurationS: 5, BitrateMbit: 200})
	if !contains(got, "-b") || !contains(got, "200M") {
		t.Fatalf("tcp cap should emit -b 200M: %v", got)
	}
	if contains(got, "-u") {
		t.Fatalf("tcp cap must not imply -u: %v", got)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/iperf/ -run TestBuildArgsTCPRateCap -v` → expect FAIL (current code only emits `-b` inside the `if o.UDP` block).

- [ ] **Step 3: Implement.** In `buildArgs`, move the bitrate cap out of the UDP-only block. Replace the trailing UDP block:

```go
	if o.UDP {
		args = append(args, "-u")
	}
	if o.BitrateMbit > 0 {
		args = append(args, "-b", strconv.Itoa(o.BitrateMbit)+"M")
	}
```

(So `-u` is added only for UDP, but `-b` is added whenever a cap is set — valid for both TCP and UDP in iperf3.)

Then add a context-cancellable runner. Add `"context"` to the imports, and add:

```go
// RunClientCtx is RunClient with a context: cancelling ctx kills the iperf3
// process (used by the stress test's kill-switch and duration cap).
func RunClientCtx(ctx context.Context, target string, o Opts) (Result, error) {
	bin := binary()
	if bin == "" {
		return Result{}, fmt.Errorf("iperf3 not found (bundle it next to NetLogger or install it) — cannot run load test")
	}
	cc := exec.CommandContext(ctx, bin, buildArgs(target, o)...)
	hideConsole(cc)
	out, err := cc.Output()
	if err != nil && len(out) == 0 {
		return Result{}, fmt.Errorf("iperf3 run: %w", err)
	}
	return Parse(out)
}
```

Refactor `RunClient` to delegate:

```go
func RunClient(target string, o Opts) (Result, error) {
	return RunClientCtx(context.Background(), target, o)
}
```

- [ ] **Step 4: Run** `go test ./internal/iperf/ -v` → expect PASS (all, incl. the new cap test and the pre-existing speed-flag tests — `TestBuildArgsSpeedFlags` uses no `BitrateMbit`, so it is unaffected).

- [ ] **Step 5: Commit** (after `gofmt -w internal/iperf/iperf.go internal/iperf/speedargs_test.go`):

```
git add internal/iperf/iperf.go internal/iperf/speedargs_test.go
git commit -m "feat(iperf): context-cancellable RunClientCtx + TCP rate cap (-b)"
```

---

## Task 2: Stress types + pure helpers

**Files:**
- Create: `internal/appcore/stress.go`
- Create: `internal/appcore/stress_test.go`

- [ ] **Step 1: Write the failing tests** — create `internal/appcore/stress_test.go`:

```go
package appcore

import "testing"

func TestMeshTargets(t *testing.T) {
	self := PeerInfo{ID: "self", Host: "ryzen", Addr: "10.0.0.1:8088"}
	peers := []PeerInfo{
		{ID: "p", Host: "proj", Addr: "10.0.0.2:8088"},
		{ID: "s", Host: "laptop", Addr: "10.0.0.3:8088"},
	}
	m := meshTargets(self, peers)
	// every node targets every OTHER node (full mesh).
	if len(m["self"]) != 2 || len(m["p"]) != 2 || len(m["s"]) != 2 {
		t.Fatalf("each node should target 2 others: %+v", m)
	}
	// targets are bare hosts (iperf3 hits :5201), control port stripped.
	for _, ts := range m {
		for _, tg := range ts {
			if tg == "10.0.0.1:8088" {
				t.Fatalf("target still has control port: %v", tg)
			}
		}
	}
}

func TestStressAbortPredicate(t *testing.T) {
	if shouldAbort(2) {
		t.Fatalf("2 consecutive errors should not abort")
	}
	if !shouldAbort(3) {
		t.Fatalf("3 consecutive errors should abort")
	}
}

func TestStressStartDelay(t *testing.T) {
	// start_at in the future → positive delay; in the past → zero.
	if d := startDelay(1000, 1500); d != 500 {
		t.Fatalf("future delay = %d us, want 500", d)
	}
	if d := startDelay(2000, 1500); d != 0 {
		t.Fatalf("past delay should clamp to 0, got %d", d)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/appcore/ -run 'TestMesh|TestStressAbort|TestStressStartDelay' -v` → expect FAIL (undefined).

- [ ] **Step 3: Implement.** Create `internal/appcore/stress.go`:

```go
package appcore

const stressAbortAfter = 3 // consecutive iperf3 client errors against a target → abort that link

// StressOpts is a node-local stress command: load these targets at a per-link
// cap for a bounded duration, starting at an absolute wall-clock time.
type StressOpts struct {
	RunID          string   `json:"run_id"`
	Targets        []string `json:"targets"`         // bare hosts to load (iperf3 :5201)
	PerLinkCapMbit int      `json:"per_link_cap_mbit"`
	Proto          string   `json:"proto"`           // "tcp" | "udp"
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
```

- [ ] **Step 4: Run** `go test ./internal/appcore/ -run 'TestMesh|TestStressAbort|TestStressStartDelay' -v` → expect PASS.

- [ ] **Step 5: Commit** (after gofmt):

```
git add internal/appcore/stress.go internal/appcore/stress_test.go
git commit -m "feat(appcore): stress types + mesh-target/abort/start-delay helpers"
```

---

## Task 3: Node-local stress manager (start/stop/status + load goroutines)

**Files:**
- Modify: `internal/appcore/stress.go`
- Modify: `internal/appcore/appcore.go` (`App` struct: add `stressMu sync.Mutex` and `stress *stressRun`; add a `stressRunner` seam)
- Test: `internal/appcore/stress_test.go`

- [ ] **Step 1: Write the failing test** (append). Uses an injected runner so no real iperf3 runs:

```go
import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"netlogger/internal/iperf"
)

func TestStressLifecycleWithFakeRunner(t *testing.T) {
	a := &App{}
	var calls int64
	// Fake runner: counts invocations, returns a small result, respects ctx.
	a.stressRunner = func(ctx context.Context, target string, o iperf.Opts) (iperf.Result, error) {
		atomic.AddInt64(&calls, 1)
		select {
		case <-ctx.Done():
			return iperf.Result{}, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
		return iperf.Result{SumBitsPerSec: 100e6}, nil
	}

	opts := StressOpts{RunID: "r1", Targets: []string{"10.0.0.2", "10.0.0.3"}, PerLinkCapMbit: 50, Proto: "tcp", DurationS: 1, StartAtUnixUS: 0}
	if err := a.startStressLocal(opts); err != nil {
		t.Fatalf("start: %v", err)
	}
	// A second start with the same run is rejected.
	if err := a.startStressLocal(opts); err == nil {
		t.Fatalf("duplicate start should be rejected")
	}
	time.Sleep(30 * time.Millisecond)
	st := a.stressStatusLocal()
	if !st.Running || len(st.Links) != 2 {
		t.Fatalf("expected running with 2 links, got %+v", st)
	}
	a.stopStressLocal("r1")
	time.Sleep(20 * time.Millisecond)
	if a.stressStatusLocal().Running {
		t.Fatalf("stop should end the run")
	}
	if atomic.LoadInt64(&calls) == 0 {
		t.Fatalf("runner was never called")
	}
}

func TestStressAutoAbortsFailingLink(t *testing.T) {
	a := &App{}
	a.stressRunner = func(ctx context.Context, target string, o iperf.Opts) (iperf.Result, error) {
		if target == "bad" {
			return iperf.Result{}, errors.New("connection refused")
		}
		select {
		case <-ctx.Done():
			return iperf.Result{}, ctx.Err()
		case <-time.After(2 * time.Millisecond):
		}
		return iperf.Result{SumBitsPerSec: 100e6}, nil
	}
	_ = a.startStressLocal(StressOpts{RunID: "r2", Targets: []string{"bad", "good"}, DurationS: 2, Proto: "tcp"})
	time.Sleep(60 * time.Millisecond)
	st := a.stressStatusLocal()
	var badAborted bool
	for _, l := range st.Links {
		if l.Target == "bad" {
			badAborted = l.Aborted
		}
	}
	if !badAborted {
		t.Fatalf("the failing link should auto-abort: %+v", st.Links)
	}
	a.stopStressLocal("r2")
}

var _ = sync.Mutex{} // keep sync imported if unused elsewhere
```

- [ ] **Step 2: Run** `go test ./internal/appcore/ -run 'TestStressLifecycle|TestStressAutoAborts' -v` → expect FAIL (undefined `stressRunner`, `startStressLocal`, etc.).

- [ ] **Step 3: Implement.**

(a) In `internal/appcore/appcore.go`, add to the `App` struct (near the other seams):

```go
	// stressRunner runs one capped iperf3 client (defaults to iperf.RunClientCtx);
	// injectable for tests.
	stressRunner func(ctx context.Context, target string, o iperf.Opts) (iperf.Result, error)
	stressMu     sync.Mutex
	stress       *stressRun
```

In `New`, default the seam in the struct literal:

```go
		stressRunner: iperf.RunClientCtx,
```

(`context` and `iperf` are already imported in appcore.go.)

(b) Append to `internal/appcore/stress.go` (add imports `context`, `sync`, `time`, `netlogger/internal/iperf`, `math`):

```go
// stressRun is one active stress run on this node.
type stressRun struct {
	runID   string
	cancel  context.CancelFunc
	starts  int64 // started wall-clock (us)
	ends    int64 // hard duration cap (us)
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
	if dur <= 0 || dur > 600 { // hard duration cap (<=10 min)
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

	// Lifecycle goroutine: cancel at the hard duration cap, then mark stopped.
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
```

Add `"errors"` to the `stress.go` imports and declare the sentinel error near the top of the file:

```go
var errStressBusy = errors.New("a stress run is already active")
```

And a small deterministic key sorter (add `"sort"` to imports):

```go
func sortedKeys(m map[string]*LinkLoad) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
```

- [ ] **Step 4: Run** `go test ./internal/appcore/ -run 'TestStress' -v` then the whole package `go test ./internal/appcore/ -count=2` → expect PASS (no flakiness).

- [ ] **Step 5: Commit** (after gofmt):

```
git add internal/appcore/stress.go internal/appcore/appcore.go internal/appcore/stress_test.go
git commit -m "feat(appcore): node-local stress manager (capped load, duration cap, auto-abort, kill-switch)"
```

---

## Task 4: Stress endpoints + clients

**Files:**
- Modify: `internal/appcore/stress.go`
- Test: `internal/appcore/stress_test.go`

- [ ] **Step 1: Write the failing tests** (append; reuse `net/http`, `httptest`, `strings`, `encoding/json` — add to the test import block):

```go
func TestStressStartStopStatusHandlers(t *testing.T) {
	a := &App{}
	a.stressRunner = func(ctx context.Context, target string, o iperf.Opts) (iperf.Result, error) {
		select {
		case <-ctx.Done():
			return iperf.Result{}, ctx.Err()
		case <-time.After(2 * time.Millisecond):
		}
		return iperf.Result{SumBitsPerSec: 10e6}, nil
	}
	start := stressStartHandler(a.startStressLocal)
	rr := httptest.NewRecorder()
	body := `{"run_id":"rx","targets":["10.0.0.2"],"per_link_cap_mbit":50,"proto":"tcp","duration_s":2}`
	start(rr, httptest.NewRequest(http.MethodPost, "/api/stress/start", strings.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("start status %d", rr.Code)
	}
	time.Sleep(20 * time.Millisecond)

	status := stressStatusHandler(a.stressStatusLocal)
	rr2 := httptest.NewRecorder()
	status(rr2, httptest.NewRequest(http.MethodGet, "/api/stress/status", nil))
	var st StressStatus
	_ = json.Unmarshal(rr2.Body.Bytes(), &st)
	if !st.Running {
		t.Fatalf("status should report running: %s", rr2.Body.String())
	}

	stop := stressStopHandler(a.stopStressLocal)
	rr3 := httptest.NewRecorder()
	stop(rr3, httptest.NewRequest(http.MethodPost, "/api/stress/stop", strings.NewReader(`{"run_id":"rx"}`)))
	if rr3.Code != http.StatusOK {
		t.Fatalf("stop status %d", rr3.Code)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/appcore/ -run TestStressStartStopStatusHandlers -v` → expect FAIL.

- [ ] **Step 3: Implement.** Append to `internal/appcore/stress.go` (ensure `net/http`, `encoding/json`, `bytes`, `fmt` imported):

```go
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

// --- orchestrator-side clients ---

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
```

- [ ] **Step 4: Run** `go test ./internal/appcore/ -run TestStress -v` → PASS.

- [ ] **Step 5: Commit** (gofmt):

```
git add internal/appcore/stress.go internal/appcore/stress_test.go
git commit -m "feat(appcore): /api/stress start/stop/status handlers + clients"
```

---

## Task 5: Orchestrator fan-out (StartStress / StopStress / PollStress)

**Files:**
- Modify: `internal/appcore/stress.go`
- Modify: `internal/appcore/appcore.go` (`App` seams: `FetchStressStart`, `FetchStressStop`, `FetchStressStatus`; defaults in `New`)
- Test: `internal/appcore/stress_test.go`

- [ ] **Step 1: Write the failing test** (append):

```go
func TestStartStressFansOutPerNodeTargets(t *testing.T) {
	a := &App{nodeID: "self"}
	started := map[string]StressOpts{}
	var localOpts StressOpts
	a.startLocalStress = func(o StressOpts) error { localOpts = o; return nil }
	a.FetchStressStart = func(baseURL string, o StressOpts) error {
		started[baseURL] = o
		return nil
	}
	self := PeerInfo{ID: "self", Host: "ryzen", Addr: "10.0.0.1:8088"}
	peers := []PeerInfo{{ID: "p", Host: "proj", Addr: "10.0.0.2:8088"}}

	a.StartStress(self, peers, StressParams{PerLinkCapMbit: 100, Proto: "tcp", DurationS: 30, NowUnixUS: 1_000_000})

	// self runs locally with the peer as its only target (bare host).
	if len(localOpts.Targets) != 1 || localOpts.Targets[0] != "10.0.0.2" {
		t.Fatalf("self targets wrong: %+v", localOpts.Targets)
	}
	// the peer is commanded with self as its target.
	po, ok := started["http://10.0.0.2:8088"]
	if !ok || len(po.Targets) != 1 || po.Targets[0] != "10.0.0.1" {
		t.Fatalf("peer command wrong: %+v", started)
	}
	// shared run id + a 2s-ahead start time.
	if localOpts.RunID == "" || localOpts.RunID != po.RunID {
		t.Fatalf("run ids must match and be non-empty")
	}
	if localOpts.StartAtUnixUS != 1_000_000+2_000_000 {
		t.Fatalf("start_at should be now+2s, got %d", localOpts.StartAtUnixUS)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/appcore/ -run TestStartStressFansOut -v` → expect FAIL.

- [ ] **Step 3: Implement.**

(a) In `appcore.go` `App` struct add seams:

```go
	// Stress orchestration seams (default to the HTTP clients / local manager).
	FetchStressStart  func(baseURL string, o StressOpts) error
	FetchStressStop   func(baseURL, runID string) error
	FetchStressStatus func(baseURL string) (StressStatus, error)
	startLocalStress  func(StressOpts) error
```

In `New` struct literal, default them:

```go
		FetchStressStart: func(baseURL string, o StressOpts) error {
			return postStressStart(&http.Client{Timeout: 10 * time.Second}, baseURL, o)
		},
		FetchStressStop: func(baseURL, runID string) error {
			return postStressStop(&http.Client{Timeout: 5 * time.Second}, baseURL, runID)
		},
		FetchStressStatus: func(baseURL string) (StressStatus, error) {
			return fetchStressStatus(&http.Client{Timeout: 3 * time.Second}, baseURL)
		},
```

`startLocalStress` cannot reference a method in the literal cleanly before `a` exists; set it after building the struct. Change `New` to assign to a local var first if it doesn't already, OR add this just before `return` in `New` is not possible since `New` returns a literal. Instead, lazily default it in `StartStress` (see below) — if `a.startLocalStress` is nil, use `a.startStressLocal`.

(b) Append to `stress.go`:

```go
import "strconv" // add to the import block

// StressParams is the orchestrator-level request (topology is full mesh in v1).
type StressParams struct {
	PerLinkCapMbit int
	Proto          string
	DurationS      int
	NowUnixUS      int64 // injected for tests; 0 => time.Now()
}

// StartStress fans a full-mesh stress run out to every node with a shared run id
// and a start time 2 seconds ahead so all nodes begin together.
func (a *App) StartStress(self PeerInfo, peers []PeerInfo, p StressParams) string {
	now := p.NowUnixUS
	if now == 0 {
		now = timeNowUnixUS()
	}
	runID := "stress-" + strconv.FormatInt(now, 10)
	startAt := now + 2_000_000 // +2s
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
	for id, node := range byID {
		o := mk(id)
		if id == a.nodeID {
			_ = local(o)
		} else {
			_ = a.FetchStressStart("http://"+node.Addr, o)
		}
	}
	return runID
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

func timeNowUnixUS() int64 { return timeNow().UnixMicro() }
```

Add a tiny indirection so tests never hit the wall clock in the fan-out path: at the top of `stress.go` add `import "time"` (already there) and `var timeNow = time.Now`. (If `time` is already imported from Task 3, reuse it; just add the `var timeNow = time.Now` line once.)

- [ ] **Step 4: Run** `go test ./internal/appcore/ -run TestStartStressFansOut -v` and full package `-count=2` → PASS.

- [ ] **Step 5: Commit** (gofmt):

```
git add internal/appcore/stress.go internal/appcore/appcore.go internal/appcore/stress_test.go
git commit -m "feat(appcore): StartStress/StopStress/PollStress orchestrator fan-out (shared run id + start_at)"
```

---

## Task 6: Mount endpoints + cancel on shutdown

**Files:**
- Modify: `internal/appcore/appcore.go` (`Start` mux; `Stop`/shutdown path)

- [ ] **Step 1: Mount the endpoints.** In `Start`, inside the `if a.disc != nil {` mux block, after the `/api/speedtest` handler, add:

```go
		mux.Handle("/api/stress/start", stressStartHandler(a.startStressLocal))
		mux.Handle("/api/stress/stop", stressStopHandler(a.stopStressLocal))
		mux.Handle("/api/stress/status", stressStatusHandler(a.stressStatusLocal))
```

- [ ] **Step 2: Cancel any active stress on shutdown.** Find the `Stop`/shutdown method (where `a.cancel()` is called and goroutines are awaited). Add, before cancelling the main context:

```go
	a.stopStressLocal("") // kill any in-flight stress load on app shutdown
```

(If unsure where `Stop` lives, search: `grep -n "func (a \*App) Stop" internal/appcore/appcore.go`.)

- [ ] **Step 3: Build + test.**

Run: `go build ./cmd/netlogger-app/ && go test ./internal/appcore/ -count=1`
Expected: builds; PASS.

- [ ] **Step 4: Commit** (gofmt):

```
git add internal/appcore/appcore.go
git commit -m "feat(appcore): mount /api/stress endpoints + cancel stress on shutdown"
```

---

## Task 7: Stress sub-view UI (live readout + kill-switch)

**Files:**
- Modify: `internal/ui/tests.go` (`testsState` + a `layoutStress`; segmented sub-view switch)
- Modify: `internal/ui/ui.go` (Start/Stop click handling + polling)
- Test: `internal/ui/tests_test.go` (pure helpers)

This is Gio UI — TDD the pure helpers; verify rendering at a manual gate.

- [ ] **Step 1: Write failing pure-helper tests** (append to `tests_test.go`):

```go
func TestStressLoadColor(t *testing.T) {
	if stressHealthColor(true) != colBad {
		t.Fatalf("aborted link should be red")
	}
	if stressHealthColor(false) != colGood {
		t.Fatalf("healthy link should be green")
	}
}

func TestSubViewLabel(t *testing.T) {
	if subLabel(0) != "Speed (LAN)" || subLabel(1) != "Stress" || subLabel(2) != "Internet" {
		t.Fatalf("sub-view labels wrong")
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/ui/ -run 'TestStressLoadColor|TestSubViewLabel' -v` → FAIL.

- [ ] **Step 3: Implement.** In `internal/ui/tests.go`:

(a) Extend `testsState` with stress fields:

```go
	sub        int // 0 Speed, 1 Stress, 2 Internet
	speedSeg   widget.Clickable
	stressSeg  widget.Clickable
	startStress widget.Clickable
	stopStress  widget.Clickable
	stressMu    sync.Mutex
	stressOn    bool
	stressNodes []appcore.StressStatus
```

(b) Add pure helpers:

```go
func subLabel(i int) string {
	switch i {
	case 1:
		return "Stress"
	case 2:
		return "Internet"
	default:
		return "Speed (LAN)"
	}
}

func stressHealthColor(aborted bool) color.NRGBA {
	if aborted {
		return colBad
	}
	return colGood
}
```

(c) Add `layoutStress(gtx, th, st)` that renders: a "Full mesh · per-link cap 200 Mbit/s" caption, a Start/Stop button (Start when not running, Stop/kill-switch when running), and a per-link status strip built from `st.stressNodes` (host → target rows with `SentMbit` and a health dot via `stressHealthColor(l.Aborted)`). Follow the row/Flex pattern already used in `layoutSpeedMatrix`. Keep copy plain (no editorializing — see the project's UI-copy rule).

(d) In `layoutTests`, render a small segmented control (Speed / Stress) switching `st.sub`, and dispatch to the Speed sub-view (existing) or `layoutStress`. (Internet is Build #3 — show a muted "coming soon" caption or leave the segment out; do NOT build it here.)

- [ ] **Step 4:** Wire into `ui.go` `Run`: handle `st.startStress.Clicked` → `go a.StartStress(snap.SelfPeer, snap.Peers, appcore.StressParams{PerLinkCapMbit: 200, Proto: "tcp", DurationS: 120})` and set `st.stressOn = true`; `st.stopStress.Clicked` → `a.StopStress(snap.SelfPeer, snap.Peers, "")` + `st.stressOn = false`. Poll status on a timer (reuse the 1s invalidate loop): every ~1s while `st.stressOn`, `go func(){ ns := a.PollStress(snap.SelfPeer, snap.Peers); st.stressMu.Lock(); st.stressNodes = ns; st.stressMu.Unlock() }()`. Guard `stressNodes` reads/writes with `st.stressMu` (same discipline as the speed matrix).

- [ ] **Step 5:** Run `go test ./internal/ui/` → PASS. Build the app: `go build -ldflags "-H windowsgui -s -w" ./cmd/netlogger-app/`. `gofmt -l internal/ui/` + `go vet ./internal/ui/` clean.

- [ ] **Step 6: MANUAL GATE (real mesh):** Deploy the elevated build to ≥2 machines on the same build. Start a stress run; confirm: the heatmap goes hot under load; the per-link strip shows sent Mbit/s; Stop halts load promptly; the run auto-ends at the duration cap; a pulled cable on one link shows that link auto-aborting while others continue.

- [ ] **Step 7: Commit** (gofmt):

```
git add internal/ui/tests.go internal/ui/ui.go internal/ui/tests_test.go
git commit -m "feat(ui): Stress sub-view — full-mesh start/stop kill-switch + live per-link readout"
```

---

## Self-Review

**Spec coverage (§4.3 + stress rows of the decisions table):**
- Full-mesh topology → `meshTargets` (Task 2), fan-out (Task 5).
- Per-link rate cap (default 200, adjustable) → `-b` cap (Task 1), `PerLinkCapMbit` plumbed through (Tasks 3–5, 7). (v1 ships the 200 default; a slider is a small follow-on noted in Task 7.)
- Auto-abort on hard fault → `shouldAbort` + `loadTarget` consecutive-error logic (Tasks 2–3).
- Hard duration cap → clamp + lifecycle goroutine (Task 3).
- Manual kill-switch → `stopStressLocal` / `/api/stress/stop` / Stop button (Tasks 3, 4, 7).
- Synchronized start → shared `start_at` = now+2s (Task 5); per-node delay (Task 3). (Absolute clock; M3-offset correction is a noted refinement.)
- Live readout = heatmap + per-link strip → Task 7 (heatmap already exists; embed/poll status).

**Placeholder scan:** none. The `startLocalStress` nil-default indirection is explicitly explained (it can't reference a method inside the `New` literal). Internet sub-view explicitly deferred to Build #3.

**Type consistency:** `StressOpts`, `StressParams`, `StressStatus`, `LinkLoad`, `stressRun`, `meshTargets`, `shouldAbort`, `startDelay`, `startStressLocal`/`stopStressLocal`/`stressStatusLocal`, `StartStress`/`StopStress`/`PollStress`, handler/client names are consistent across tasks. `stressRunner` seam type matches `iperf.RunClientCtx` signature `func(context.Context, string, iperf.Opts) (iperf.Result, error)`.

**Known follow-ons (out of scope):** adjustable cap slider + duration field in the UI; M3 clock-offset-corrected start; persisting stress run summaries to the store/export; Internet test (Build #3).
