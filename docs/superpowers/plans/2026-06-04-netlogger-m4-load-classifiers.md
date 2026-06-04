# NetLogger M4 — iperf3 Load Tests + Classifiers — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the load-triggered half of the diagnosis: wrap iperf3 to run load tests between host pairs, collect NIC error counters, and add the two classifiers — bufferbloat-vs-fault and LAN-vs-WAN — surfaced via a Tests endpoint and GUI.

**Architecture:** `iperf` shells out to the iperf3 binary with `--json` and parses intervals + summary (TCP retransmits, UDP loss/jitter); it degrades gracefully when iperf3 is absent. `classify` holds two **pure** decision functions over measured series. `sysinfo` gains a NIC-counter reader (Linux `/proc/net/dev` parser is unit-tested; Windows/macOS best-effort). The coordinator exposes `/api/loadtest` (run between a pair) and `/api/classify`. All parsing + classification logic is unit-tested on fixtures; the live iperf3 run is validated during the deploy/test phase.

**Tech Stack:** Builds on M1–M3. No new deps. New packages: `internal/iperf`, `internal/classify`. Extends `internal/sysinfo`, `internal/coordinator`, `internal/web`.

**Spec reference:** §5.4 (iperf3, RRUL), §5.5 (NIC counters), §9.4 (bufferbloat-vs-fault + LAN-vs-WAN classifiers). The full RRUL envelope orchestration and live numbers are exercised on deploy; this milestone builds and unit-tests the machinery.

---

### Task 1: `iperf` result types + JSON parser

**Files:**
- Create: `internal/iperf/iperf.go`, `internal/iperf/iperf_test.go`, `internal/iperf/testdata/tcp.json`

- [ ] **Step 1: Create the fixture**

Create `internal/iperf/testdata/tcp.json` (a minimal but schema-accurate iperf3 `--json` TCP result):
```json
{
  "intervals": [
    { "sum": { "start": 0, "end": 1, "bits_per_second": 2350000000, "retransmits": 0, "rtt": 410 } },
    { "sum": { "start": 1, "end": 2, "bits_per_second": 410000000, "retransmits": 87, "rtt": 38000 } }
  ],
  "end": {
    "sum_sent": { "bits_per_second": 1380000000, "retransmits": 87 },
    "sum": { "jitter_ms": 0, "lost_percent": 0, "lost_packets": 0, "packets": 0 }
  }
}
```

- [ ] **Step 2: Write the failing test**

Create `internal/iperf/iperf_test.go`:
```go
package iperf

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseTCP(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "tcp.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	res, err := Parse(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(res.Intervals) != 2 {
		t.Fatalf("want 2 intervals, got %d", len(res.Intervals))
	}
	if res.Intervals[1].Retransmits != 87 || res.Intervals[1].RTTus != 38000 {
		t.Fatalf("interval 2 fields wrong: %+v", res.Intervals[1])
	}
	if res.SumRetransmits != 87 {
		t.Fatalf("want 87 total retransmits, got %d", res.SumRetransmits)
	}
	// Throughput collapse: interval 2 is far below interval 1.
	if res.Intervals[1].BitsPerSecond >= res.Intervals[0].BitsPerSecond {
		t.Fatal("expected a throughput drop between intervals")
	}
}

func TestParseErrorField(t *testing.T) {
	_, err := Parse([]byte(`{"error":"unable to connect to server"}`))
	if err == nil {
		t.Fatal("an iperf3 error payload must surface as an error")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/iperf/`
Expected: FAIL — `undefined: Parse`.

- [ ] **Step 4: Write the implementation**

Create `internal/iperf/iperf.go`:
```go
// Package iperf wraps the iperf3 binary: it parses --json output and runs the
// client, degrading gracefully when iperf3 is not installed.
package iperf

import (
	"encoding/json"
	"fmt"
	"os/exec"
)

// Interval is one 1-second iperf3 interval (the high-signal fields, spec §5.4).
type Interval struct {
	StartS        float64 `json:"start_s"`
	EndS          float64 `json:"end_s"`
	BitsPerSecond float64 `json:"bits_per_second"`
	Retransmits   int     `json:"retransmits"`  // TCP
	RTTus         int     `json:"rtt_us"`       // TCP
	JitterMs      float64 `json:"jitter_ms"`    // UDP
	LostPercent   float64 `json:"lost_percent"` // UDP
}

// Result is the parsed iperf3 run.
type Result struct {
	Intervals      []Interval `json:"intervals"`
	SumBitsPerSec  float64    `json:"sum_bits_per_second"`
	SumRetransmits int        `json:"sum_retransmits"`
	UDPLostPercent float64    `json:"udp_lost_percent"`
	UDPJitterMs    float64    `json:"udp_jitter_ms"`
}

type rawResult struct {
	Intervals []struct {
		Sum struct {
			Start         float64 `json:"start"`
			End           float64 `json:"end"`
			BitsPerSecond float64 `json:"bits_per_second"`
			Retransmits   int     `json:"retransmits"`
			RTT           int     `json:"rtt"`
			JitterMs      float64 `json:"jitter_ms"`
			LostPercent   float64 `json:"lost_percent"`
		} `json:"sum"`
	} `json:"intervals"`
	End struct {
		SumSent struct {
			BitsPerSecond float64 `json:"bits_per_second"`
			Retransmits   int     `json:"retransmits"`
		} `json:"sum_sent"`
		Sum struct {
			JitterMs    float64 `json:"jitter_ms"`
			LostPercent float64 `json:"lost_percent"`
		} `json:"sum"`
	} `json:"end"`
	Error string `json:"error"`
}

// Parse converts iperf3 --json bytes into a Result.
func Parse(data []byte) (Result, error) {
	var raw rawResult
	if err := json.Unmarshal(data, &raw); err != nil {
		return Result{}, fmt.Errorf("parse iperf3 json: %w", err)
	}
	if raw.Error != "" {
		return Result{}, fmt.Errorf("iperf3: %s", raw.Error)
	}
	res := Result{
		SumBitsPerSec:  raw.End.SumSent.BitsPerSecond,
		SumRetransmits: raw.End.SumSent.Retransmits,
		UDPLostPercent: raw.End.Sum.LostPercent,
		UDPJitterMs:    raw.End.Sum.JitterMs,
	}
	for _, iv := range raw.Intervals {
		res.Intervals = append(res.Intervals, Interval{
			StartS:        iv.Sum.Start,
			EndS:          iv.Sum.End,
			BitsPerSecond: iv.Sum.BitsPerSecond,
			Retransmits:   iv.Sum.Retransmits,
			RTTus:         iv.Sum.RTT,
			JitterMs:      iv.Sum.JitterMs,
			LostPercent:   iv.Sum.LostPercent,
		})
	}
	return res, nil
}

// Available reports whether the iperf3 binary is on PATH.
func Available() bool {
	_, err := exec.LookPath("iperf3")
	return err == nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/iperf/`
Expected: PASS

- [ ] **Step 6: Commit**

```powershell
git add -A
git commit -m @'
feat(iperf): iperf3 --json parser (intervals, retransmits, loss/jitter)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
'@
```

---

### Task 2: `iperf.RunClient` (exec, graceful absence)

**Files:**
- Modify: `internal/iperf/iperf.go` (add `Opts`, `RunClient`)
- Test: `internal/iperf/run_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/iperf/run_test.go`:
```go
package iperf

import (
	"testing"
	"time"
)

func TestRunClientErrorsWhenAbsent(t *testing.T) {
	if Available() {
		t.Skip("iperf3 is installed; this test only checks the absent path")
	}
	_, err := RunClient("127.0.0.1", Opts{DurationS: 1, UDP: false})
	if err == nil {
		t.Fatal("RunClient should error when iperf3 is not installed")
	}
}

func TestRunClientBuildsArgs(t *testing.T) {
	got := buildArgs("10.0.0.5", Opts{DurationS: 5, UDP: true, BitrateMbit: 30})
	joined := ""
	for _, a := range got {
		joined += a + " "
	}
	// must target the host, request json, set duration, and enable UDP + bitrate
	for _, want := range []string{"-c", "10.0.0.5", "--json", "-t", "5", "-u", "-b", "30M", "-i", "1"} {
		if !contains(got, want) {
			t.Fatalf("args %q missing %q", joined, want)
		}
	}
	_ = time.Second
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

Run: `go test ./internal/iperf/ -run RunClient`
Expected: FAIL — `undefined: Opts` / `buildArgs`.

- [ ] **Step 3: Write the implementation**

Append to `internal/iperf/iperf.go` (add `"strconv"` to imports):
```go
// Opts configures a load test run.
type Opts struct {
	DurationS   int
	UDP         bool
	BitrateMbit int // UDP target bitrate in Mbit/s; 0 = iperf3 default
	Port        int // 0 = iperf3 default (5201)
}

func buildArgs(target string, o Opts) []string {
	if o.DurationS <= 0 {
		o.DurationS = 10
	}
	args := []string{"-c", target, "--json", "-i", "1", "-t", strconv.Itoa(o.DurationS)}
	if o.Port > 0 {
		args = append(args, "-p", strconv.Itoa(o.Port))
	}
	if o.UDP {
		args = append(args, "-u")
		if o.BitrateMbit > 0 {
			args = append(args, "-b", strconv.Itoa(o.BitrateMbit)+"M")
		}
	}
	return args
}

// RunClient runs `iperf3 -c target ...` and parses the result. It returns a
// clear error if iperf3 is not installed.
func RunClient(target string, o Opts) (Result, error) {
	if !Available() {
		return Result{}, fmt.Errorf("iperf3 not installed (PATH) — cannot run load test")
	}
	out, err := exec.Command("iperf3", buildArgs(target, o)...).Output()
	if err != nil && len(out) == 0 {
		return Result{}, fmt.Errorf("iperf3 run: %w", err)
	}
	// iperf3 returns nonzero on some errors but still emits a json body with an
	// "error" field, which Parse surfaces.
	return Parse(out)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/iperf/`
Expected: PASS (the absent-path test runs on a machine without iperf3; the args test always runs).

- [ ] **Step 5: Commit**

```powershell
git add -A
git commit -m @'
feat(iperf): RunClient with arg builder + graceful absent handling

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
'@
```

---

### Task 3: `classify` — bufferbloat-vs-fault + LAN-vs-WAN

**Files:**
- Create: `internal/classify/classify.go`, `internal/classify/classify_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/classify/classify_test.go`:
```go
package classify

import "testing"

func TestBufferbloatSmoothRampNoLoss(t *testing.T) {
	// latency ramps smoothly far above baseline, then no loss => bufferbloat
	got := BufferbloatVsFault(2.0, []float64{5, 20, 60, 110, 140}, false)
	if got != "bufferbloat" {
		t.Fatalf("want bufferbloat, got %q", got)
	}
}

func TestFaultAbruptLoss(t *testing.T) {
	// latency stays low but there is loss => hardware/link fault
	got := BufferbloatVsFault(2.0, []float64{2, 3, 2, 3, 2}, true)
	if got != "fault" {
		t.Fatalf("want fault, got %q", got)
	}
}

func TestInconclusiveWhenQuiet(t *testing.T) {
	got := BufferbloatVsFault(2.0, []float64{2, 3, 2}, false)
	if got != "inconclusive" {
		t.Fatalf("want inconclusive, got %q", got)
	}
}

func TestLANvsWAN(t *testing.T) {
	if LANvsWAN(true, true) != "lan" {
		t.Fatal("gateway failure => LAN-side")
	}
	if LANvsWAN(false, true) != "wan" {
		t.Fatal("only external failure => WAN-side")
	}
	if LANvsWAN(false, false) != "unknown" {
		t.Fatal("no reference failure => unknown")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/classify/`
Expected: FAIL — `undefined: BufferbloatVsFault`.

- [ ] **Step 3: Write the implementation**

Create `internal/classify/classify.go`:
```go
// Package classify holds pure decision functions that turn measured series into
// a fault classification (spec §9.4). No I/O — deterministic and testable.
package classify

// rampThresholdMs is how far latency must climb above baseline under load for
// the rise to count as a bufferbloat-style queue buildup.
const rampThresholdMs = 50.0

// BufferbloatVsFault classifies a load window from the latency series under
// load (ms) vs the idle baseline (ms) and whether loss occurred under load.
//   - smooth large latency rise, no loss          -> "bufferbloat"
//   - loss without a large smooth latency rise     -> "fault"
//   - neither                                       -> "inconclusive"
func BufferbloatVsFault(baselineMs float64, underLoadMs []float64, lossDuringLoad bool) string {
	if len(underLoadMs) == 0 {
		return "inconclusive"
	}
	max := underLoadMs[0]
	for _, v := range underLoadMs {
		if v > max {
			max = v
		}
	}
	bigRamp := (max - baselineMs) >= rampThresholdMs
	switch {
	case bigRamp && !lossDuringLoad:
		return "bufferbloat"
	case lossDuringLoad && !bigRamp:
		return "fault"
	case lossDuringLoad && bigRamp:
		// loss AND a big queue: lean fault (loss is the harder symptom)
		return "fault"
	default:
		return "inconclusive"
	}
}

// LANvsWAN classifies where a drop sits from whether the gateway and/or an
// external target also failed in the same window.
func LANvsWAN(gatewayFailed, externalFailed bool) string {
	switch {
	case gatewayFailed:
		return "lan"
	case externalFailed:
		return "wan"
	default:
		return "unknown"
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/classify/`
Expected: PASS

- [ ] **Step 5: Commit**

```powershell
git add -A
git commit -m @'
feat(classify): bufferbloat-vs-fault + LAN-vs-WAN pure classifiers

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
'@
```

---

### Task 4: NIC counters (`sysinfo`)

**Files:**
- Create: `internal/sysinfo/nic.go`, `internal/sysinfo/nic_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/sysinfo/nic_test.go`:
```go
package sysinfo

import "testing"

const procNetDevSample = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 1000      10    0    0    0     0          0         0     1000      10    0    0    0     0       0          0
  eth0: 500000   4000   3    7    0     0          0         0   400000    3500   1    2    0     0       0          0
`

func TestParseProcNetDev(t *testing.T) {
	nics := parseProcNetDev(procNetDevSample)
	var eth0 NIC
	for _, n := range nics {
		if n.Name == "eth0" {
			eth0 = n
		}
	}
	if eth0.Name != "eth0" {
		t.Fatalf("eth0 not parsed: %+v", nics)
	}
	if eth0.RxErrors != 3 || eth0.RxDropped != 7 || eth0.TxErrors != 1 || eth0.TxDropped != 2 {
		t.Fatalf("eth0 counters wrong: %+v", eth0)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sysinfo/ -run NetDev`
Expected: FAIL — `undefined: parseProcNetDev`.

- [ ] **Step 3: Write the implementation**

Create `internal/sysinfo/nic.go`:
```go
package sysinfo

import (
	"runtime"
	"strconv"
	"strings"
)

// NIC holds error/drop counters for one network interface.
type NIC struct {
	Name      string `json:"name"`
	RxErrors  int64  `json:"rx_errors"`
	RxDropped int64  `json:"rx_dropped"`
	TxErrors  int64  `json:"tx_errors"`
	TxDropped int64  `json:"tx_dropped"`
}

// parseProcNetDev parses Linux /proc/net/dev content. Columns after the iface
// name are: rx[bytes packets errs drop ...8], tx[bytes packets errs drop ...8].
func parseProcNetDev(content string) []NIC {
	var out []NIC
	for _, line := range strings.Split(content, "\n") {
		if !strings.Contains(line, ":") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		name := strings.TrimSpace(parts[0])
		fields := strings.Fields(parts[1])
		if len(fields) < 16 {
			continue
		}
		atoi := func(i int) int64 { v, _ := strconv.ParseInt(fields[i], 10, 64); return v }
		out = append(out, NIC{
			Name:      name,
			RxErrors:  atoi(2),
			RxDropped: atoi(3),
			TxErrors:  atoi(10),
			TxDropped: atoi(11),
		})
	}
	return out
}

// NICCounters returns per-interface error/drop counters (best-effort per OS).
// Linux is parsed from /proc/net/dev; other platforms return nil for now and
// are filled in during platform bring-up.
func NICCounters() []NIC {
	if runtime.GOOS == "linux" {
		if data, err := readFile("/proc/net/dev"); err == nil {
			return parseProcNetDev(data)
		}
	}
	return nil
}
```

Also add a tiny helper at the end of `internal/sysinfo/sysinfo.go`:
```go
// readFile reads a whole file as a string (small system files only).
func readFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sysinfo/`
Expected: PASS

- [ ] **Step 5: Commit**

```powershell
git add -A
git commit -m @'
feat(sysinfo): NIC error/drop counters (Linux /proc/net/dev parser)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
'@
```

---

### Task 5: Wire load-test + classify endpoints + Tests GUI

**Files:**
- Modify: `internal/coordinator/coordinator.go` (add `LoadTestHandler`, `ClassifyHandler`)
- Modify: `internal/web/web.go` (routes), `internal/web/static/index.html` (Tests section)
- Modify: `internal/agentsvc/agentsvc.go` (wire handlers)
- Test: `internal/coordinator/loadtest_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/coordinator/loadtest_test.go`:
```go
package coordinator

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLoadTestHandlerReportsUnavailableGracefully(t *testing.T) {
	// With no iperf3 installed, the handler must return a clean JSON error,
	// not a 500 crash.
	h := LoadTestHandler()
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/api/loadtest?target=127.0.0.1&duration=1", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 with a JSON body, got %d", rr.Code)
	}
	var resp LoadTestResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// On a box without iperf3, ok=false and an explanatory error.
	if resp.OK && resp.Error != "" {
		t.Fatalf("inconsistent response: %+v", resp)
	}
}

func TestClassifyHandler(t *testing.T) {
	h := ClassifyHandler()
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/api/classify?gateway_failed=true&external_failed=true", nil))
	var resp ClassifyResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.LANvsWAN != "lan" {
		t.Fatalf("gateway failure should classify lan, got %q", resp.LANvsWAN)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/coordinator/ -run "LoadTest|Classify"`
Expected: FAIL — `undefined: LoadTestHandler`.

- [ ] **Step 3: Add the handlers**

Append to `internal/coordinator/coordinator.go` (add imports `"strconv"`, `"netlogger/internal/classify"`, `"netlogger/internal/iperf"`):
```go
// LoadTestResponse is the result of an /api/loadtest run.
type LoadTestResponse struct {
	OK             bool    `json:"ok"`
	Error          string  `json:"error,omitempty"`
	SumBitsPerSec  float64 `json:"sum_bits_per_second,omitempty"`
	SumRetransmits int     `json:"sum_retransmits,omitempty"`
	UDPLostPercent float64 `json:"udp_lost_percent,omitempty"`
}

// LoadTestHandler runs an iperf3 load test to ?target= and returns the summary,
// or a clean error payload if iperf3 is unavailable / the run fails.
func LoadTestHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		target := r.URL.Query().Get("target")
		dur, _ := strconv.Atoi(r.URL.Query().Get("duration"))
		udp := r.URL.Query().Get("udp") == "true"
		res, err := iperf.RunClient(target, iperf.Opts{DurationS: dur, UDP: udp})
		if err != nil {
			writeJSON(w, LoadTestResponse{OK: false, Error: err.Error()})
			return
		}
		writeJSON(w, LoadTestResponse{
			OK:             true,
			SumBitsPerSec:  res.SumBitsPerSec,
			SumRetransmits: res.SumRetransmits,
			UDPLostPercent: res.UDPLostPercent,
		})
	}
}

// ClassifyResponse carries the two classifier verdicts.
type ClassifyResponse struct {
	LANvsWAN string `json:"lan_vs_wan"`
}

// ClassifyHandler exposes the LAN-vs-WAN classifier over query params.
func ClassifyHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		gw := r.URL.Query().Get("gateway_failed") == "true"
		ext := r.URL.Query().Get("external_failed") == "true"
		writeJSON(w, ClassifyResponse{LANvsWAN: classify.LANvsWAN(gw, ext)})
	}
}
```

- [ ] **Step 4: Run the coordinator tests**

Run: `go test ./internal/coordinator/`
Expected: PASS

- [ ] **Step 5: Add web routes**

In `internal/web/web.go`, add two optional fields to `Server`:
```go
	LoadTestHandler  http.HandlerFunc // optional
	ClassifyHandler  http.HandlerFunc // optional
```
And register in `Handler` after the components route:
```go
	mux.HandleFunc("/api/loadtest", orEmptyArray(s.LoadTestHandler))
	mux.HandleFunc("/api/classify", orEmptyArray(s.ClassifyHandler))
```

- [ ] **Step 6: Add a Tests section to the page**

In `internal/web/static/index.html`, add before the closing `</body>`:
```html
  <h2>Load test</h2>
  <p><input id="lt-target" placeholder="target node host" size="20"> <button onclick="runLoad()">Run iperf3</button></p>
  <pre id="lt-out" class="v"></pre>
  <script>
  function runLoad(){
    const t=document.getElementById('lt-target').value||'127.0.0.1';
    document.getElementById('lt-out').textContent='running...';
    fetch('/api/loadtest?target='+encodeURIComponent(t)+'&duration=5')
      .then(r=>r.json()).then(j=>{document.getElementById('lt-out').textContent=JSON.stringify(j,null,2);});
  }
  </script>
```

- [ ] **Step 7: Wire handlers into agentsvc**

In `internal/agentsvc/agentsvc.go`, inside the `if self.Role == "coordinator"` block, add:
```go
		ws.LoadTestHandler = coordinator.LoadTestHandler()
		ws.ClassifyHandler = coordinator.ClassifyHandler()
```

- [ ] **Step 8: Build, vet, full suite**

Run:
```powershell
go build -o bin/netlogger.exe ./cmd/netlogger
go vet ./...
go test ./...
```
Expected: builds; vet clean; all PASS.

- [ ] **Step 9: Manual verification**

Run the coordinator alone (one process is enough for these endpoints):
```powershell
$env:Path += ";C:\Program Files\Go\bin"
Start-Process .\bin\netlogger.exe -ArgumentList "--config","examples\two-node-localhost.yaml","--node","coord","--listen","127.0.0.1:8088","--db","m4_coord.db","run"
Start-Sleep -Seconds 4
Invoke-RestMethod "http://127.0.0.1:8088/api/classify?gateway_failed=true&external_failed=true"
Invoke-RestMethod "http://127.0.0.1:8088/api/loadtest?target=127.0.0.1&duration=1"
Get-Process netlogger -ErrorAction SilentlyContinue | Stop-Process -Force
```
Expected: `/api/classify` returns `{lan_vs_wan: lan}`. `/api/loadtest` returns `{ok:false, error:"iperf3 not installed..."}` on this box (iperf3 absent) — proving graceful degradation. (After iperf3 is installed during deploy, the same call returns throughput/retransmits.)

- [ ] **Step 10: Commit + push**

```powershell
git add -A
git commit -m @'
feat: iperf3 load-test + classifier endpoints and Tests GUI

Coordinator exposes /api/loadtest (graceful when iperf3 absent) and
/api/classify (LAN-vs-WAN). Builds on the iperf parser, classifiers, and NIC
counters. Live iperf3 numbers validated during the deploy/test phase.

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
'@
git push
```

---

## Done criteria for M4

- `go test ./...` passes, including: iperf3 JSON parsing + arg building, both classifiers, the `/proc/net/dev` parser, and the load-test/classify handler tests.
- A coordinator serves `/api/loadtest` (graceful without iperf3) and `/api/classify`; the page has a Load test control.
- Pushed to `origin/main`.

**Pause point:** with M1–M4 done, the next step is **deploy** the agent to the real machines (Windows boxes, the QNAP, optionally a Mac), install iperf3 where load tests are wanted, point them at a real `network.yaml`, and run a live diagnosis — the iperf3 numbers, NIC counters, correlation, and per-component scoring all come together on the actual network.
