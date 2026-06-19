# LAN Speed Test + Test Matrix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build NetLogger's point-to-point LAN throughput test — orchestrated between any two live devices from any device — surfaced in a new Tests tab as an N×N Test Matrix with per-pair drill-in.

**Architecture:** Reuse the always-on per-agent `iperf3 -s` server and the Host-allowlisted control plane. Extend `internal/iperf` with parallel-stream / reverse / bidir / warm-up flags. Add a `/api/speedtest` endpoint: when POSTed, a node runs an iperf3 *client* against a given target and returns parsed results. The orchestrator (whichever device's UI is open) fans these commands out — to itself or to a remote "From" node — and assembles a `SpeedMatrix`. A new Tests tab renders the matrix and single-pair runs.

**Tech Stack:** Go (cgo-free, so no `go test -race`), Gio v0.10.0 (immediate-mode UI; pure helpers TDD'd, rendering verified at manual gates), bundled iperf3 3.21.

**Scope note:** This is Build #1 of the Tests subsystem spec (`docs/superpowers/specs/2026-06-18-netlogger-tests-design.md`). The Stress test (Build #2) and Internet test (Build #3) get their own plans. Type names use the `Speed*` prefix to avoid colliding with the existing latency/loss `Matrix`/`MatrixCell` in `internal/appcore/links.go`.

---

## File Structure

**Modify:**
- `internal/iperf/iperf.go` — extend `Opts` (Streams/Reverse/Bidir/OmitS), `Result` (+`SumRecvBitsPerSec`), `buildArgs`, `Parse`.
- `internal/appcore/appcore.go` — mount `/api/speedtest`; add `FetchSpeed` seam field; pre-flight iperf feature check.
- `internal/ui/ui.go` — add top-nav state (Dashboard/Tests/Events) and route the item list per tab.

**Create:**
- `internal/appcore/speedtest.go` — `SpeedReq`, `SpeedResult`, `runSpeedTest`, `speedTestHandler`, `postSpeedTest`, `App.SpeedTest`, `SpeedMatrix`/`SpeedPair`, `App.SpeedSweep`, cell coloring threshold helper.
- `internal/appcore/speedtest_test.go` — pure + httptest coverage.
- `internal/iperf/speedargs_test.go` — flag-matrix + reverse-parse coverage.
- `internal/ui/tests.go` — Tests tab: segmented control, Speed sub-view (pickers + single result), matrix grid + drill-in.

---

## Task 1: Extend iperf Opts and buildArgs

**Files:**
- Modify: `internal/iperf/iperf.go` (`Opts` struct ~line 150, `buildArgs` ~line 157)
- Test: `internal/iperf/speedargs_test.go` (create)

- [ ] **Step 1: Write the failing test**

```go
package iperf

import (
	"reflect"
	"testing"
)

func TestBuildArgsSpeedFlags(t *testing.T) {
	got := buildArgs("10.0.0.5", Opts{DurationS: 10, Streams: 4, OmitS: 2, Port: 5201})
	want := []string{"-c", "10.0.0.5", "--json", "-i", "1", "-t", "10", "-p", "5201", "-P", "4", "-O", "2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tcp+streams+omit:\n got %v\nwant %v", got, want)
	}

	rev := buildArgs("h", Opts{DurationS: 5, Reverse: true})
	if !contains(rev, "-R") {
		t.Fatalf("reverse should add -R: %v", rev)
	}

	bid := buildArgs("h", Opts{DurationS: 5, Bidir: true})
	if !contains(bid, "--bidir") {
		t.Fatalf("bidir should add --bidir: %v", bid)
	}

	udp := buildArgs("h", Opts{DurationS: 5, UDP: true, BitrateMbit: 200, Streams: 3})
	if !contains(udp, "-u") || !contains(udp, "-b") || !contains(udp, "200M") || !contains(udp, "-P") {
		t.Fatalf("udp capped + streams: %v", udp)
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/iperf/ -run TestBuildArgsSpeedFlags -v`
Expected: FAIL — `Opts` has no field `Streams` (compile error).

- [ ] **Step 3: Extend `Opts` and `buildArgs`**

In `internal/iperf/iperf.go`, replace the `Opts` struct with:

```go
// Opts configures a load test run.
type Opts struct {
	DurationS   int
	UDP         bool
	BitrateMbit int // UDP target bitrate in Mbit/s; 0 = iperf3 default
	Port        int // 0 = iperf3 default (5201)
	Streams     int // -P parallel streams; 0 = single stream (one-thread-per-stream needs iperf3 >= 3.16)
	Reverse     bool // -R: server sends, client receives (download from the client's seat)
	Bidir       bool // --bidir: simultaneous both directions (needs iperf3 >= 3.7)
	OmitS       int  // -O: omit the first N seconds (skip TCP slow-start)
}
```

Replace `buildArgs` with:

```go
func buildArgs(target string, o Opts) []string {
	if o.DurationS <= 0 {
		o.DurationS = 10
	}
	args := []string{"-c", target, "--json", "-i", "1", "-t", strconv.Itoa(o.DurationS)}
	if o.Port > 0 {
		args = append(args, "-p", strconv.Itoa(o.Port))
	}
	if o.Streams > 0 {
		args = append(args, "-P", strconv.Itoa(o.Streams))
	}
	if o.OmitS > 0 {
		args = append(args, "-O", strconv.Itoa(o.OmitS))
	}
	if o.Reverse {
		args = append(args, "-R")
	}
	if o.Bidir {
		args = append(args, "--bidir")
	}
	if o.UDP {
		args = append(args, "-u")
		if o.BitrateMbit > 0 {
			args = append(args, "-b", strconv.Itoa(o.BitrateMbit)+"M")
		}
	}
	return args
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/iperf/ -run TestBuildArgsSpeedFlags -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/iperf/iperf.go internal/iperf/speedargs_test.go
git commit -m "feat(iperf): add -P/-R/--bidir/-O flags to Opts and buildArgs"
```

---

## Task 2: Direction-aware parse (received throughput for -R / --bidir)

**Files:**
- Modify: `internal/iperf/iperf.go` (`Result` struct ~line 27, `rawResult` ~line 36, `Parse` ~line 62)
- Test: `internal/iperf/speedargs_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestParseReceivedBits(t *testing.T) {
	js := []byte(`{"intervals":[],"end":{
		"sum_sent":{"bits_per_second":100000000,"retransmits":7},
		"sum_received":{"bits_per_second":940000000},
		"sum":{"jitter_ms":0.3,"lost_percent":0.1}}}`)
	res, err := Parse(js)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.SumBitsPerSec != 100000000 {
		t.Fatalf("sent = %v, want 1e8", res.SumBitsPerSec)
	}
	if res.SumRecvBitsPerSec != 940000000 {
		t.Fatalf("received = %v, want 9.4e8", res.SumRecvBitsPerSec)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/iperf/ -run TestParseReceivedBits -v`
Expected: FAIL — `Result` has no field `SumRecvBitsPerSec`.

- [ ] **Step 3: Add the received field**

In `Result` (after `SumBitsPerSec float64`), add:

```go
	SumRecvBitsPerSec float64 `json:"sum_recv_bits_per_second"` // client-received rate; the meaningful number for -R/--bidir download
```

In `rawResult.End`, add alongside `SumSent`:

```go
		SumReceived struct {
			BitsPerSecond float64 `json:"bits_per_second"`
		} `json:"sum_received"`
```

In `Parse`, after setting `SumBitsPerSec`, add:

```go
	res.SumRecvBitsPerSec = raw.End.SumReceived.BitsPerSecond
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/iperf/ -v`
Expected: PASS (all iperf tests, including pre-existing ones — `SumBitsPerSec` semantics unchanged).

- [ ] **Step 5: Commit**

```bash
git add internal/iperf/iperf.go internal/iperf/speedargs_test.go
git commit -m "feat(iperf): parse sum_received bits for reverse/bidir download rate"
```

---

## Task 3: SpeedReq / SpeedResult + runSpeedTest (down-then-up)

**Files:**
- Create: `internal/appcore/speedtest.go`
- Create: `internal/appcore/speedtest_test.go`

- [ ] **Step 1: Write the failing test**

```go
package appcore

import (
	"errors"
	"testing"

	"netlogger/internal/iperf"
)

func TestRunSpeedTestBoth(t *testing.T) {
	// Fake runner: forward run reports upload; reverse run reports download.
	run := func(target string, o iperf.Opts) (iperf.Result, error) {
		if o.Reverse {
			return iperf.Result{SumRecvBitsPerSec: 940e6, UDPJitterMs: 0, UDPLostPercent: 0}, nil
		}
		return iperf.Result{SumBitsPerSec: 887e6, SumRetransmits: 12}, nil
	}
	got := runSpeedTest(run, "10.0.0.5", SpeedReq{Direction: "both", Streams: 4, DurationS: 10, OmitS: 2})
	if got.Err != "" {
		t.Fatalf("unexpected err: %s", got.Err)
	}
	if round1(got.DownMbit) != 940 || round1(got.UpMbit) != 887 {
		t.Fatalf("down/up = %v/%v, want 940/887", got.DownMbit, got.UpMbit)
	}
	if got.Retransmits != 12 {
		t.Fatalf("retransmits = %d, want 12", got.Retransmits)
	}
}

func TestRunSpeedTestErrorSurfaces(t *testing.T) {
	run := func(target string, o iperf.Opts) (iperf.Result, error) {
		return iperf.Result{}, errors.New("iperf3 not found")
	}
	got := runSpeedTest(run, "h", SpeedReq{Direction: "down"})
	if got.Err == "" {
		t.Fatalf("expected Err to be set")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/appcore/ -run TestRunSpeedTest -v`
Expected: FAIL — undefined `runSpeedTest`, `SpeedReq`, `SpeedResult`, `round1`.

- [ ] **Step 3: Implement types + runSpeedTest**

Create `internal/appcore/speedtest.go`:

```go
package appcore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"

	"netlogger/internal/iperf"
)

// SpeedReq is a request to run an iperf3 client against a target.
type SpeedReq struct {
	Target    string `json:"target"`              // host:port or host of the iperf3 server to hit
	Direction string `json:"direction"`           // "down" | "up" | "both" | "bidir"
	Proto     string `json:"proto"`               // "tcp" | "udp" ("" => tcp)
	Streams   int    `json:"streams"`             // 0 => 1
	DurationS int    `json:"duration_s"`          // 0 => 10
	OmitS     int    `json:"omit_s"`              // warm-up seconds to skip
	CapMbit   int    `json:"cap_mbit,omitempty"`  // UDP rate cap
}

// SpeedResult is the parsed outcome of a speed test, in Mbit/s.
type SpeedResult struct {
	DownMbit    float64 `json:"down_mbit"`
	UpMbit      float64 `json:"up_mbit"`
	Retransmits int     `json:"retransmits"`
	JitterMs    float64 `json:"jitter_ms"`
	LossPct     float64 `json:"loss_pct"`
	Proto       string  `json:"proto"`
	Err         string  `json:"err,omitempty"`
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
func mbit(bps float64) float64 { return round1(bps / 1e6) }

// runner matches iperf.RunClient; injected for tests.
type runner func(target string, o iperf.Opts) (iperf.Result, error)

// runSpeedTest executes the requested direction(s) and maps to SpeedResult.
// "both" runs upload (forward) then download (-R) sequentially.
func runSpeedTest(run runner, target string, req SpeedReq) SpeedResult {
	res := SpeedResult{Proto: req.Proto}
	if res.Proto == "" {
		res.Proto = "tcp"
	}
	base := iperf.Opts{
		DurationS:   req.DurationS,
		Streams:     req.Streams,
		OmitS:       req.OmitS,
		UDP:         req.Proto == "udp",
		BitrateMbit: req.CapMbit,
	}
	doUp := func() bool {
		r, err := run(target, base)
		if err != nil {
			res.Err = err.Error()
			return false
		}
		res.UpMbit = mbit(r.SumBitsPerSec)
		res.Retransmits += r.SumRetransmits
		res.JitterMs = r.UDPJitterMs
		res.LossPct = r.UDPLostPercent
		return true
	}
	doDown := func() bool {
		o := base
		o.Reverse = true
		r, err := run(target, o)
		if err != nil {
			res.Err = err.Error()
			return false
		}
		res.DownMbit = mbit(r.SumRecvBitsPerSec)
		res.Retransmits += r.SumRetransmits
		if r.UDPJitterMs > res.JitterMs {
			res.JitterMs = r.UDPJitterMs
		}
		if r.UDPLostPercent > res.LossPct {
			res.LossPct = r.UDPLostPercent
		}
		return true
	}
	switch req.Direction {
	case "up":
		doUp()
	case "down":
		doDown()
	case "bidir":
		o := base
		o.Bidir = true
		r, err := run(target, o)
		if err != nil {
			res.Err = err.Error()
		} else {
			res.UpMbit = mbit(r.SumBitsPerSec)
			res.DownMbit = mbit(r.SumRecvBitsPerSec)
			res.Retransmits = r.SumRetransmits
			res.JitterMs = r.UDPJitterMs
			res.LossPct = r.UDPLostPercent
		}
	default: // "both"
		if doUp() {
			doDown()
		}
	}
	return res
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/appcore/ -run TestRunSpeedTest -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/appcore/speedtest.go internal/appcore/speedtest_test.go
git commit -m "feat(appcore): runSpeedTest maps iperf results to down/up (down-then-up)"
```

---

## Task 4: /api/speedtest handler + postSpeedTest client

**Files:**
- Modify: `internal/appcore/speedtest.go`
- Test: `internal/appcore/speedtest_test.go`

- [ ] **Step 1: Write the failing test**

```go
import (
	"net/http"
	"net/http/httptest"
	"strings"
)

func TestSpeedTestHandlerRoundTrip(t *testing.T) {
	run := func(target string, o iperf.Opts) (iperf.Result, error) {
		return iperf.Result{SumBitsPerSec: 500e6}, nil
	}
	h := speedTestHandler(func(req SpeedReq) SpeedResult { return runSpeedTest(run, req.Target, req) })

	body := `{"target":"10.0.0.5","direction":"up","streams":4,"duration_s":3}`
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodPost, "/api/speedtest", strings.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	var out SpeedResult
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if round1(out.UpMbit) != 500 {
		t.Fatalf("up = %v, want 500", out.UpMbit)
	}
}

func TestSpeedTestHandlerRejectsGet(t *testing.T) {
	h := speedTestHandler(func(req SpeedReq) SpeedResult { return SpeedResult{} })
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/api/speedtest", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", rr.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/appcore/ -run TestSpeedTestHandler -v`
Expected: FAIL — undefined `speedTestHandler`.

- [ ] **Step 3: Implement handler + client**

Append to `internal/appcore/speedtest.go`:

```go
// speedTestHandler accepts POST SpeedReq and returns SpeedResult JSON.
func speedTestHandler(do func(SpeedReq) SpeedResult) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req SpeedReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Target == "" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if req.DurationS <= 0 || req.DurationS > 60 {
			req.DurationS = 10 // clamp
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(do(req))
	}
}

// postSpeedTest asks a remote node (baseURL = its control URL) to run a client.
func postSpeedTest(client *http.Client, baseURL string, req SpeedReq) (SpeedResult, error) {
	body, _ := json.Marshal(req)
	resp, err := client.Post(baseURL+"/api/speedtest", "application/json", bytes.NewReader(body))
	if err != nil {
		return SpeedResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return SpeedResult{}, fmt.Errorf("speedtest: status %d", resp.StatusCode)
	}
	var out SpeedResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return SpeedResult{}, err
	}
	return out, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/appcore/ -run TestSpeedTestHandler -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/appcore/speedtest.go internal/appcore/speedtest_test.go
git commit -m "feat(appcore): /api/speedtest handler + postSpeedTest client"
```

---

## Task 5: Orchestrate any pair from anywhere (App.SpeedTest)

**Files:**
- Modify: `internal/appcore/speedtest.go`
- Modify: `internal/appcore/appcore.go` (add `FetchSpeed` seam field to `App`, default in `New`)
- Test: `internal/appcore/speedtest_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestAppSpeedTestRemoteVsLocal(t *testing.T) {
	a := &App{nodeID: "self", host: "ryzen"}
	// Local runner (self is the From node).
	a.localSpeed = func(req SpeedReq) SpeedResult { return SpeedResult{UpMbit: 111, Proto: "tcp"} }
	// Remote runner (From node is a peer): record the call.
	var gotURL string
	a.FetchSpeed = func(baseURL string, req SpeedReq) (SpeedResult, error) {
		gotURL = baseURL
		return SpeedResult{UpMbit: 222}, nil
	}

	local := a.SpeedTest(PeerInfo{ID: "self", Host: "ryzen", Addr: "127.0.0.1:8088"}, "10.0.0.9", SpeedReq{Direction: "up"})
	if local.UpMbit != 111 {
		t.Fatalf("self-from should run locally, got %v", local.UpMbit)
	}
	remote := a.SpeedTest(PeerInfo{ID: "p1", Host: "proj", Addr: "10.0.0.2:8088"}, "10.0.0.9", SpeedReq{Direction: "up"})
	if remote.UpMbit != 222 || gotURL != "http://10.0.0.2:8088" {
		t.Fatalf("peer-from should POST to peer, got %v url=%q", remote.UpMbit, gotURL)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/appcore/ -run TestAppSpeedTest -v`
Expected: FAIL — `App` has no `localSpeed`, `FetchSpeed`, `SpeedTest`.

- [ ] **Step 3: Implement orchestration**

In `internal/appcore/appcore.go`, add to the `App` struct (near `FetchLinks`/`FetchEvents`):

```go
	// FetchSpeed asks a peer to run a speed test (defaults to postSpeedTest).
	FetchSpeed func(baseURL string, req SpeedReq) (SpeedResult, error)
	// localSpeed runs a speed test on this node (defaults to runSpeedTest+iperf.RunClient).
	localSpeed func(req SpeedReq) SpeedResult
```

In `New` (where other seams are defaulted), add:

```go
	a.FetchSpeed = func(baseURL string, req SpeedReq) (SpeedResult, error) {
		return postSpeedTest(&http.Client{Timeout: 90 * time.Second}, baseURL, req)
	}
	a.localSpeed = func(req SpeedReq) SpeedResult {
		return runSpeedTest(iperf.RunClient, req.Target, req)
	}
```

(Confirm `net/http` and `time` are imported in appcore.go — they are.)

Append to `internal/appcore/speedtest.go`:

```go
// SpeedTest orchestrates a single pair: it tells the `from` node to run an
// iperf3 client against `targetAddr`. If `from` is this node, it runs locally;
// otherwise it POSTs to the peer's control plane. Orchestrator-agnostic — the
// caller need not be either endpoint.
func (a *App) SpeedTest(from PeerInfo, targetAddr string, req SpeedReq) SpeedResult {
	req.Target = targetAddr
	if from.ID == a.nodeID {
		return a.localSpeed(req)
	}
	out, err := a.FetchSpeed("http://"+from.Addr, req)
	if err != nil {
		return SpeedResult{Err: err.Error()}
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/appcore/ -run TestAppSpeedTest -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/appcore/appcore.go internal/appcore/speedtest.go internal/appcore/speedtest_test.go
git commit -m "feat(appcore): App.SpeedTest orchestrates any pair (local or remote From)"
```

---

## Task 6: SpeedMatrix assembly + cell coloring

**Files:**
- Modify: `internal/appcore/speedtest.go`
- Test: `internal/appcore/speedtest_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestSpeedNodesAndPairs(t *testing.T) {
	self := PeerInfo{ID: "self", Host: "ryzen", Addr: "127.0.0.1:8088"}
	peers := []PeerInfo{
		{ID: "b", Host: "proj", Addr: "10.0.0.2:8088"},
		{ID: "a", Host: "nas", Addr: "10.0.0.3:8088"},
	}
	nodes := speedNodes(self, peers)
	if len(nodes) != 3 || nodes[0].Host != "nas" || nodes[2].Host != "ryzen" {
		t.Fatalf("nodes not sorted by host: %+v", nodes) // nas, proj, ryzen
	}
	pairs := speedPairs(nodes)
	if len(pairs) != 6 { // 3*3 - 3 diagonal
		t.Fatalf("pairs = %d, want 6", len(pairs))
	}
	for _, p := range pairs {
		if p.From.ID == p.To.ID {
			t.Fatalf("diagonal pair leaked: %+v", p)
		}
	}
}

func TestSpeedColor(t *testing.T) {
	if speedColorBucket(950) != "good" || speedColorBucket(600) != "watch" || speedColorBucket(120) != "bad" || speedColorBucket(-1) != "none" {
		t.Fatalf("color buckets wrong")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/appcore/ -run 'TestSpeedNodes|TestSpeedColor' -v`
Expected: FAIL — undefined `speedNodes`, `speedPairs`, `speedColorBucket`, `SpeedPair`.

- [ ] **Step 3: Implement assembly + coloring**

Append to `internal/appcore/speedtest.go`:

```go
import "sort" // add to the existing import block

// SpeedNode is a row/column of the matrix.
type SpeedNode struct {
	ID   string
	Host string
	Addr string
}

// SpeedPair is one directed test From->To.
type SpeedPair struct {
	From SpeedNode
	To   SpeedNode
}

// SpeedMatrix is the assembled grid of completed runs, keyed From\x00To.
type SpeedMatrix struct {
	Nodes []SpeedNode
	Cells map[string]SpeedResult
}

func speedKey(from, to string) string { return from + "\x00" + to }

func toNode(p PeerInfo) SpeedNode { return SpeedNode{ID: p.ID, Host: p.Host, Addr: p.Addr} }

// speedNodes returns self + peers, sorted by host then id for a stable layout.
func speedNodes(self PeerInfo, peers []PeerInfo) []SpeedNode {
	out := []SpeedNode{toNode(self)}
	for _, p := range peers {
		out = append(out, toNode(p))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Host != out[j].Host {
			return out[i].Host < out[j].Host
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// speedPairs enumerates every directed off-diagonal pair.
func speedPairs(nodes []SpeedNode) []SpeedPair {
	var ps []SpeedPair
	for _, f := range nodes {
		for _, t := range nodes {
			if f.ID != t.ID {
				ps = append(ps, SpeedPair{From: f, To: t})
			}
		}
	}
	return ps
}

// speedColorBucket maps a download Mbit/s to a severity bucket. -1 = not run.
func speedColorBucket(mbit float64) string {
	switch {
	case mbit < 0:
		return "none"
	case mbit >= 900:
		return "good"
	case mbit >= 400:
		return "watch"
	default:
		return "bad"
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/appcore/ -run 'TestSpeedNodes|TestSpeedColor' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/appcore/speedtest.go internal/appcore/speedtest_test.go
git commit -m "feat(appcore): SpeedMatrix nodes/pairs + cell color buckets"
```

---

## Task 7: SpeedSweep — run all pairs with bounded concurrency

**Files:**
- Modify: `internal/appcore/speedtest.go`
- Test: `internal/appcore/speedtest_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestSpeedSweepRunsEveryPair(t *testing.T) {
	a := &App{nodeID: "self"}
	a.localSpeed = func(req SpeedReq) SpeedResult { return SpeedResult{DownMbit: 900} }
	a.FetchSpeed = func(baseURL string, req SpeedReq) (SpeedResult, error) { return SpeedResult{DownMbit: 500}, nil }

	self := PeerInfo{ID: "self", Host: "ryzen", Addr: "127.0.0.1:8088"}
	peers := []PeerInfo{{ID: "p", Host: "proj", Addr: "10.0.0.2:8088"}}
	m := a.SpeedSweep(self, peers, SpeedReq{Direction: "down", DurationS: 3})

	if len(m.Nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(m.Nodes))
	}
	if len(m.Cells) != 2 { // ryzen->proj and proj->ryzen
		t.Fatalf("cells = %d, want 2", len(m.Cells))
	}
	// self is the From for ryzen->proj => local runner => 900.
	if c := m.Cells[speedKey("self", "p")]; c.DownMbit != 900 {
		t.Fatalf("self->p should be local 900, got %v", c.DownMbit)
	}
	// proj is the From for proj->ryzen => remote => 500.
	if c := m.Cells[speedKey("p", "self")]; c.DownMbit != 500 {
		t.Fatalf("p->self should be remote 500, got %v", c.DownMbit)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/appcore/ -run TestSpeedSweep -v`
Expected: FAIL — undefined `SpeedSweep`.

- [ ] **Step 3: Implement the sweep**

Append to `internal/appcore/speedtest.go`:

```go
// SpeedSweep runs every directed pair and assembles a SpeedMatrix. Pairs run
// with bounded concurrency (4 at a time) so a large mesh doesn't open dozens of
// simultaneous iperf3 streams (which would distort the measurements).
func (a *App) SpeedSweep(self PeerInfo, peers []PeerInfo, req SpeedReq) SpeedMatrix {
	nodes := speedNodes(self, peers)
	pairs := speedPairs(nodes)
	cells := make(map[string]SpeedResult, len(pairs))
	byID := map[string]PeerInfo{self.ID: self}
	for _, p := range peers {
		byID[p.ID] = p
	}

	type result struct {
		key string
		res SpeedResult
	}
	sem := make(chan struct{}, 4)
	ch := make(chan result, len(pairs))
	for _, pr := range pairs {
		pr := pr
		sem <- struct{}{}
		go func() {
			defer func() { <-sem }()
			from := byID[pr.From.ID]
			ch <- result{key: speedKey(pr.From.ID, pr.To.ID), res: a.SpeedTest(from, pr.To.Addr, req)}
		}()
	}
	for range pairs {
		r := <-ch
		cells[r.key] = r.res
	}
	return SpeedMatrix{Nodes: nodes, Cells: cells}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/appcore/ -run TestSpeedSweep -v`
Expected: PASS.

- [ ] **Step 5: Run the full appcore + iperf suites twice (catch ordering flakiness)**

Run: `go test ./internal/appcore/ ./internal/iperf/ -count=2`
Expected: PASS both runs.

- [ ] **Step 6: Commit**

```bash
git add internal/appcore/speedtest.go internal/appcore/speedtest_test.go
git commit -m "feat(appcore): SpeedSweep runs all pairs (bounded concurrency) into a SpeedMatrix"
```

---

## Task 8: Mount /api/speedtest + iperf feature pre-flight

**Files:**
- Modify: `internal/appcore/appcore.go` (`Start`, mux block ~line 376-386; `defaultStartIperf` ~line 311)
- Test: manual (verified by Task 9+ end-to-end and the existing handler test)

- [ ] **Step 1: Mount the endpoint**

In `Start`, inside the `if a.disc != nil {` mux block, after the existing `mux.Handle("/api/lossbuckets", ...)` line, add:

```go
		mux.Handle("/api/speedtest", speedTestHandler(func(req SpeedReq) SpeedResult {
			return runSpeedTest(iperf.RunClient, req.Target, req)
		}))
```

- [ ] **Step 2: Add a one-time iperf feature-capability log**

In `defaultStartIperf` (after `ver := iperf.Version()`), add:

```go
	if ver != "" {
		log.Printf("iperf3 ready: %s (parallel/-P and --bidir require >= 3.16/3.7)", ver)
	}
```

This surfaces the bundled version in the log so a too-old binary is diagnosable. (No behavior gate — 3.21 is bundled; the log is the pre-flight record.)

- [ ] **Step 3: Build the whole app (no console) to confirm it compiles**

Run: `go build -ldflags "-H windowsgui -s -w" ./cmd/netlogger-app/`
Expected: builds with no errors; produces `netlogger-app.exe`.

- [ ] **Step 4: Run the full test suite**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/appcore/appcore.go
git commit -m "feat(appcore): mount /api/speedtest + log iperf3 capability on start"
```

---

## Task 9: Tests tab nav + Speed sub-view (single pair)

**Files:**
- Modify: `internal/ui/ui.go` (`Run`, the event loop ~line 42-150)
- Create: `internal/ui/tests.go`

This task is UI (Gio immediate-mode). Pure helpers are TDD'd; rendering is verified at a manual gate (the repo convention — there is no headless Gio test harness here).

- [ ] **Step 1: Write the failing test for the pure nav helper**

Create `internal/ui/tests_test.go`:

```go
package ui

import "testing"

func TestNextTab(t *testing.T) {
	if nextTab(navDashboard, navTests) != navTests {
		t.Fatalf("explicit select should win")
	}
	if tabLabel(navTests) != "Tests" || tabLabel(navEvents) != "Events" || tabLabel(navDashboard) != "Dashboard" {
		t.Fatalf("tab labels wrong")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/ -run TestNextTab -v`
Expected: FAIL — undefined `navDashboard`, `nextTab`, `tabLabel`.

- [ ] **Step 3: Implement nav types + Speed sub-view scaffold**

Create `internal/ui/tests.go`:

```go
package ui

import (
	"fmt"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"netlogger/internal/appcore"
)

type navTab int

const (
	navDashboard navTab = iota
	navTests
	navEvents
)

func tabLabel(t navTab) string {
	switch t {
	case navTests:
		return "Tests"
	case navEvents:
		return "Events"
	default:
		return "Dashboard"
	}
}

// nextTab returns the selected tab (explicit selection wins); kept as a pure
// helper so tab routing is testable without a window.
func nextTab(current, selected navTab) navTab { return selected }

// testsState holds the Tests tab's widget state across frames.
type testsState struct {
	sub        int // 0 = Speed, 1 = Stress, 2 = Internet (stress/internet are later builds)
	fromIdx    int
	toIdx      int
	runBtn     widget.Clickable
	runAllBtn  widget.Clickable
	last       appcore.SpeedResult
	matrix     appcore.SpeedMatrix
	haveMatrix bool
	status     string
}

// layoutTests renders the Tests tab. `nodes` is self+peers for the pickers.
func layoutTests(gtx layout.Context, th *material.Theme, st *testsState, nodes []appcore.SpeedNode) layout.Dimensions {
	// Speed sub-view only for Build #1; Stress/Internet are placeholders wired in later builds.
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return material.Body1(th, "Speed (LAN)").Layout(gtx)
		}),
		layout.Rigid(gap(8)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if st.last.Err != "" {
				lbl := material.Body2(th, "error: "+st.last.Err)
				lbl.Color = colBad
				return lbl.Layout(gtx)
			}
			txt := fmt.Sprintf("down %.0f / up %.0f Mbit/s", st.last.DownMbit, st.last.UpMbit)
			return material.H6(th, txt).Layout(gtx)
		}),
		layout.Rigid(gap(8)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, material.Button(th, &st.runBtn, "Run all pairs").Layout)
		}),
	)
}
```

- [ ] **Step 4: Run the nav helper test to verify it passes**

Run: `go test ./internal/ui/ -run TestNextTab -v`
Expected: PASS.

- [ ] **Step 5: Wire the nav bar + tab routing into `Run`**

In `internal/ui/ui.go` `Run`, after `var hZoomOut, hZoomIn, hNow widget.Clickable` (~line 55), add:

```go
		var nav navTab = navDashboard
		var navDash, navTst, navEvt widget.Clickable
		var tst testsState
```

After the existing button-click handlers (after the `hNow.Clicked` block ~line 116), add:

```go
		if navDash.Clicked(gtx) {
			nav = nextTab(nav, navDashboard)
		}
		if navTst.Clicked(gtx) {
			nav = nextTab(nav, navTests)
		}
		if navEvt.Clicked(gtx) {
			nav = nextTab(nav, navEvents)
		}
		if tst.runBtn.Clicked(gtx) {
			self, peers := snap.SelfPeer, snap.Peers
			go func() {
				m := a.SpeedSweep(self, peers, appcore.SpeedReq{Direction: "both", Streams: 4, DurationS: 10, OmitS: 2})
				tst.matrix = m
				tst.haveMatrix = true
			}()
			tst.status = "running matrix…"
		}
```

Replace the `items := []layout.Widget{...}` construction so the per-tab body is selected. Keep the existing dashboard slice as `dashItems`, then:

```go
		navBar := func(gtx layout.Context) layout.Dimensions {
			tab := func(b *widget.Clickable, t navTab) layout.FlexChild {
				return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Right: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						btn := material.Button(th, b, tabLabel(t))
						if nav != t {
							btn.Background = colCard
						}
						return btn.Layout(gtx)
					})
				})
			}
			return layout.Flex{}.Layout(gtx, tab(&navDash, navDashboard), tab(&navTst, navTests), tab(&navEvt, navEvents))
		}

		var items []layout.Widget
		switch nav {
		case navTests:
			items = []layout.Widget{
				func(gtx layout.Context) layout.Dimensions { return navBar(gtx) },
				gap(16),
				cardSection(func(gtx layout.Context) layout.Dimensions {
					return layoutTests(gtx, th, &tst, snap.SpeedNodes())
				}),
			}
		case navEvents:
			items = []layout.Widget{
				func(gtx layout.Context) layout.Dimensions { return navBar(gtx) },
				gap(16),
				cardSection(func(gtx layout.Context) layout.Dimensions { return layoutEvents(gtx, th, snap) }),
			}
		default:
			items = append([]layout.Widget{
				func(gtx layout.Context) layout.Dimensions { return navBar(gtx) },
				gap(16),
			}, dashItems...)
		}
```

Where `dashItems` is the existing `items` slice (rename the existing literal to `dashItems`, and remove the trailing Events card from the dashboard list since Events now has its own tab).

- [ ] **Step 6: Add the Snapshot helpers `SelfPeer`, `SpeedNodes`**

In `internal/appcore/appcore.go`, add a `SelfPeer PeerInfo` field to `Snapshot` and populate it in `Snapshot()` (self id/host/control addr). Add to `internal/appcore/speedtest.go`:

```go
// SpeedNodes returns self + peers as matrix nodes for the UI pickers.
func (s Snapshot) SpeedNodes() []SpeedNode { return speedNodes(s.SelfPeer, s.Peers) }
```

Populate `SelfPeer` in `Snapshot()` using `a.nodeID`, `a.host`, and `"127.0.0.1:"+strconv.Itoa(controlPort)`.

- [ ] **Step 7: Build and MANUAL GATE**

Run: `go build -ldflags "-H windowsgui -s -w -X netlogger/internal/version.Build=$(git rev-parse --short HEAD)" ./cmd/netlogger-app/`
Then run the exe. Manual checks:
- Nav bar shows Dashboard / Tests / Events; clicking switches views; Dashboard looks unchanged minus the Events card.
- Tests tab shows the Speed sub-view and a "Run all pairs" button.

Expected: all three tabs render; no panic.

- [ ] **Step 8: Commit**

```bash
git add internal/ui/ui.go internal/ui/tests.go internal/ui/tests_test.go internal/appcore/appcore.go internal/appcore/speedtest.go
git commit -m "feat(ui): Tests tab nav + Speed sub-view scaffold + SpeedSweep trigger"
```

---

## Task 10: Render the Test Matrix grid + cell drill-in

**Files:**
- Modify: `internal/ui/tests.go`
- Test: `internal/ui/tests_test.go` (pure cell-style helper)

- [ ] **Step 1: Write the failing test for the cell style helper**

```go
func TestMatrixCellStyle(t *testing.T) {
	if matrixCellColor(950) != colGood || matrixCellColor(600) != colWatch || matrixCellColor(100) != colBad {
		t.Fatalf("cell colors wrong")
	}
	if matrixCellText(-1) != "—" || matrixCellText(941) != "941" {
		t.Fatalf("cell text wrong")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/ -run TestMatrixCellStyle -v`
Expected: FAIL — undefined `matrixCellColor`, `matrixCellText`.

- [ ] **Step 3: Implement the matrix render + helpers**

In `internal/ui/tests.go`, add:

```go
import (
	"image"
	"image/color"

	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
)

func matrixCellColor(downMbit float64) color.NRGBA {
	switch {
	case downMbit >= 900:
		return colGood
	case downMbit >= 400:
		return colWatch
	default:
		return colBad
	}
}

func matrixCellText(downMbit float64) string {
	if downMbit < 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f", downMbit)
}

// layoutSpeedMatrix draws an N×N grid: rows = client (From), cols = server (To).
// A cell shows the From->To download Mbit/s, colored by severity; the diagonal
// is muted. Uniform column/row dims via equal Flex weights.
func layoutSpeedMatrix(gtx layout.Context, th *material.Theme, m appcore.SpeedMatrix) layout.Dimensions {
	if len(m.Nodes) == 0 {
		return material.Body2(th, "no live devices").Layout(gtx)
	}
	cellH := gtx.Dp(unit.Dp(46))
	cell := func(bg color.NRGBA, txt string, fg color.NRGBA) layout.FlexChild {
		return layout.Flex{Weight: 1}.Rigid // placeholder; replaced below
	}
	_ = cell
	// Header row: corner + column hosts.
	headerRow := layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		var cols []layout.FlexChild
		cols = append(cols, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: image.Pt(gtx.Constraints.Min.X, 0)} }))
		for _, n := range m.Nodes {
			n := n
			cols = append(cols, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				lbl := material.Caption(th, n.Host)
				lbl.Color = colTextSec
				return layout.Center.Layout(gtx, lbl.Layout)
			}))
		}
		return layout.Flex{}.Layout(gtx, cols...)
	})
	rows := []layout.FlexChild{headerRow, layout.Rigid(gap(6))}
	for _, from := range m.Nodes {
		from := from
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			var cols []layout.FlexChild
			cols = append(cols, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				lbl := material.Caption(th, from.Host)
				lbl.Color = colTextSec
				return layout.E.Layout(gtx, layout.Inset{Right: unit.Dp(8)}.Layout(gtx, lbl.Layout))
			}))
			for _, to := range m.Nodes {
				to := to
				cols = append(cols, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Right: unit.Dp(4), Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min = image.Pt(gtx.Constraints.Max.X, cellH)
						bg := colCardAlt
						txt := "—"
						fg := colTextMut
						if from.ID != to.ID {
							res, ok := m.Cells[speedKeyUI(from.ID, to.ID)]
							if ok && res.Err == "" {
								bg = matrixCellColor(res.DownMbit)
								txt = matrixCellText(res.DownMbit)
								fg = colBg
							} else {
								txt = "·"
							}
						}
						rect := image.Rectangle{Max: gtx.Constraints.Min}
						paint.FillShape(gtx.Ops, bg, clip.UniformRRect(rect, gtx.Dp(unit.Dp(8))).Op(gtx.Ops))
						lbl := material.Body2(th, txt)
						lbl.Color = fg
						return layout.Center.Layout(gtx, lbl.Layout)
					})
				}))
			}
			return layout.Flex{}.Layout(gtx, cols...)
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}

// speedKeyUI mirrors appcore.speedKey for cell lookup (kept local; the appcore
// key format is From\x00To).
func speedKeyUI(from, to string) string { return from + "\x00" + to }
```

Note: remove the `cell`/`_ = cell` placeholder lines before building — they are illustrative scaffolding; the real cells are built inline in the row loop. Confirm `op` import is used or drop it.

Then in `layoutTests`, when `st.haveMatrix`, render `layoutSpeedMatrix(gtx, th, st.matrix)` below the single-result line.

- [ ] **Step 4: Run the helper test to verify it passes**

Run: `go test ./internal/ui/ -run TestMatrixCellStyle -v`
Expected: PASS.

- [ ] **Step 5: Build + MANUAL GATE (real mesh)**

Run: `go build -ldflags "-H windowsgui -s -w -X netlogger/internal/version.Build=$(git rev-parse --short HEAD)" ./cmd/netlogger-app/`
Deploy to ryzen + at least one peer. Manual checks:
- Tests tab → "Run all pairs" populates the grid; columns and rows are uniform; the diagonal is muted.
- A genuinely slow link (or a deliberately throttled one) shows amber/red; healthy links show green near wire speed.

Expected: matrix renders with aligned cells; values match an independent `iperf3 -c <peer>` spot check (±10%).

- [ ] **Step 6: Run the full suite twice and commit**

Run: `go test ./... -count=2`
Expected: PASS.

```bash
git add internal/ui/tests.go internal/ui/tests_test.go
git commit -m "feat(ui): render Test Matrix grid with uniform cells + severity colors"
```

---

## Self-Review

**Spec coverage (build #1 portion of `2026-06-18-netlogger-tests-design.md`):**
- §3 Tests tab + segmented sub-views → Task 9 (nav + Speed sub-view; Stress/Internet sub-views are later builds, scaffolded as placeholders).
- §4.2 iperf extensions (`-P`/`-R`/`--bidir`/`-O`, received-rate parse) → Tasks 1–2.
- §3 LAN orchestrate-any-pair, down-then-up default → Tasks 3, 5.
- §3 Test Matrix from day one + drill-in → Tasks 6, 7, 10. (Per-cell click-drill-in is a follow-on interaction; Task 10 renders the grid and values — clickable drill-in to a single-pair detail is a small UI add tracked as the first item of any follow-up.)
- §4.1 `/api/speedtest` → Tasks 4, 8.
- §7 testing (pure TDD + httptest + `-count=2` + manual gates) → throughout.
- §9 iperf version pre-flight → Task 8 Step 2.
- §5 persistence/export of results → **deferred**: not in this plan; add when wiring export (the spec marks it as history/export, non-blocking for a working matrix). Flagged here so it isn't lost.

**Placeholder scan:** Task 10 Step 3 contains an explicitly-labeled illustrative `cell` scaffold with a written instruction to delete it before building — not a silent placeholder. All other steps carry complete code.

**Type consistency:** `SpeedReq`/`SpeedResult`/`SpeedNode`/`SpeedPair`/`SpeedMatrix`, `speedKey` (From\x00To), `runSpeedTest(runner, target, req)`, `App.SpeedTest(from, targetAddr, req)`, `App.SpeedSweep(self, peers, req)`, `speedColorBucket` (appcore) vs `matrixCellColor`/`matrixCellText` (ui) are consistent across tasks. `runner` = `func(target string, o iperf.Opts) (iperf.Result, error)` matches `iperf.RunClient`. UI `speedKeyUI` mirrors appcore `speedKey` (documented).

**Known follow-ons (out of scope for build #1, intentionally):** per-cell click drill-in detail panel; persistence of results to a `loadtest_results` store table + export inclusion; the Stress (Build #2) and Internet (Build #3) plans.
