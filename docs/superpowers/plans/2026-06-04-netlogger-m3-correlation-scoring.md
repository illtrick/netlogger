# NetLogger M3 — Clock Correlation + Per-Component Scoring — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Measure clock offset between coordinator and each agent (NTP-style, min-δ), detect per-path failure events, correlate them by uncertainty-interval overlap (simultaneous → shared device vs independent → per-host), and roll the result into a per-component **health + coverage** score exposed at `/api/correlation` and `/api/components`.

**Architecture:** Agents serve `/api/time` (their T2/T3). The coordinator's `MeasureOffset` runs N round-trips, keeps the min-δ sample, and clamps absurd offsets. `correlate.DetectEvents` turns a node's samples into failure events; `correlate.Correlate` shifts each event into coordinator time, widens it by the clock uncertainty + duration, and groups overlapping events. `score.Score` walks the config topology, attributes tested/failing host-pairs to the components on their path, and assigns health + coverage. All correlation/scoring logic is pure functions over in-memory data — deterministic and unit-testable.

**Tech Stack:** Builds on M1/M2. No new deps. New packages: `internal/correlate`, `internal/score`. Extends `internal/mesh` (offset), `internal/store` (read aggregated samples), `internal/coordinator` + `internal/web` (endpoints).

**Spec reference:** §6 (offset handshake, uncertainty intervals), §9/§9a (correlation + scoring). Bufferbloat/fault + LAN/WAN classifiers and NIC counters are M4. Clock resolution/drift terms beyond δ/2 are simplified here (δ/2 + a per-node floor); the full drift budget is a later refinement.

---

### Task 1: Clock offset handshake (`/api/time` + `MeasureOffset`)

**Files:**
- Modify: `internal/mesh/agentapi.go` (add `Time` handler + route)
- Create: `internal/mesh/offset.go`, `internal/mesh/offset_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/mesh/offset_test.go`:
```go
package mesh

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// timeServerWithOffset serves /api/time as if the agent clock is ahead by skew.
func timeServerWithOffset(t *testing.T, skew time.Duration) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/time", func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().Add(skew).UTC().UnixMicro()
		_ = json.NewEncoder(w).Encode(TimePair{T2UnixUS: now, T3UnixUS: now})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestMeasureOffsetRecoversSkew(t *testing.T) {
	srv := timeServerWithOffset(t, 250*time.Millisecond)
	off, err := MeasureOffset(srv.Client(), srv.URL, 8)
	if err != nil {
		t.Fatalf("measure: %v", err)
	}
	if !off.Reliable {
		t.Fatal("offset should be reliable for a 250ms skew")
	}
	// Should recover ~+250ms (agent ahead) within a generous tolerance.
	got := off.OffsetUS
	if got < 150_000 || got > 350_000 {
		t.Fatalf("offset %dus not near +250000us", got)
	}
	if off.RTTus < 0 {
		t.Fatalf("negative RTT: %d", off.RTTus)
	}
}

func TestMeasureOffsetClampsAbsurd(t *testing.T) {
	srv := timeServerWithOffset(t, 60*time.Second)
	off, err := MeasureOffset(srv.Client(), srv.URL, 4)
	if err != nil {
		t.Fatalf("measure: %v", err)
	}
	if off.Reliable {
		t.Fatal("a 60s offset must be marked unreliable (clamped)")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mesh/ -run Offset`
Expected: FAIL — `undefined: TimePair`.

- [ ] **Step 3: Add the `/api/time` handler**

In `internal/mesh/agentapi.go`, add the `TimePair` type (near `Info`) and a `Time` handler, and register it. Add:
```go
// TimePair carries the agent's receive (T2) and send (T3) timestamps for the
// NTP-style 4-timestamp offset handshake.
type TimePair struct {
	T2UnixUS int64 `json:"t2_unix_us"`
	T3UnixUS int64 `json:"t3_unix_us"`
}

// Time handles GET /api/time. T2 is recorded on entry, T3 just before sending.
func (a *AgentAPI) Time(w http.ResponseWriter, r *http.Request) {
	t2 := time.Now().UTC().UnixMicro()
	w.Header().Set("Content-Type", "application/json")
	t3 := time.Now().UTC().UnixMicro()
	_ = json.NewEncoder(w).Encode(TimePair{T2UnixUS: t2, T3UnixUS: t3})
}
```
In `Register`, add the route:
```go
	mux.HandleFunc("/api/time", a.Time)
```

- [ ] **Step 4: Write the offset measurer**

Create `internal/mesh/offset.go`:
```go
package mesh

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// clampUS is the maximum believable offset; beyond this the agent clock is
// treated as broken and excluded from correlation (spec §6).
const clampUS = 30_000_000 // 30s

// Offset is a measured clock offset between an agent and the coordinator.
type Offset struct {
	OffsetUS int64 // agent_clock - coordinator_clock, microseconds
	RTTus    int64 // the min round-trip delay (δ) observed
	Reliable bool  // false if |offset| exceeds the clamp
}

// HalfUncUS is the clock-uncertainty half-width contributed by this offset
// (δ/2), used to widen correlation intervals.
func (o Offset) HalfUncUS() int64 { return o.RTTus / 2 }

// MeasureOffset runs n round-trips to baseURL/api/time and returns the offset
// from the sample with the smallest delay (least queuing — most trustworthy).
func MeasureOffset(client *http.Client, baseURL string, n int) (Offset, error) {
	if n < 1 {
		n = 1
	}
	best := Offset{RTTus: 1 << 62}
	var got bool
	for i := 0; i < n; i++ {
		t1 := time.Now().UTC().UnixMicro()
		resp, err := client.Get(baseURL + "/api/time")
		t4 := time.Now().UTC().UnixMicro()
		if err != nil {
			return Offset{}, err
		}
		var tp TimePair
		derr := json.NewDecoder(resp.Body).Decode(&tp)
		resp.Body.Close()
		if derr != nil {
			return Offset{}, derr
		}
		delta := (t4 - t1) - (tp.T3UnixUS - tp.T2UnixUS)
		offset := ((tp.T2UnixUS - t1) + (tp.T3UnixUS - t4)) / 2
		if delta < best.RTTus {
			best = Offset{OffsetUS: offset, RTTus: delta}
			got = true
		}
	}
	if !got {
		return Offset{}, fmt.Errorf("no offset samples")
	}
	abs := best.OffsetUS
	if abs < 0 {
		abs = -abs
	}
	best.Reliable = abs <= clampUS
	return best, nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/mesh/`
Expected: PASS

- [ ] **Step 6: Commit**

```powershell
git add -A
git commit -m @'
feat(mesh): NTP-style clock offset handshake (min-delta, clamp)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
'@
```

---

### Task 2: Read aggregated samples + thread-safe offsets

**Files:**
- Modify: `internal/store/store.go` (add `AgentSamplesAll`)
- Create: `internal/mesh/offsets.go`, `internal/mesh/offsets_test.go`
- Test: `internal/store/aggregate_test.go` (extend)

- [ ] **Step 1: Write the failing tests**

Append to `internal/store/aggregate_test.go`:
```go
func TestAgentSamplesAll(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "all.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	for i := 1; i <= 3; i++ {
		_ = s.Upsert("ncase", Sample{Seq: int64(i), TSUnixUS: int64(i * 10), ProbeType: "icmp", SrcHost: "ncase", DstHost: "ryzen", Lost: i == 2})
	}
	rows, err := s.AgentSamplesAll("ncase")
	if err != nil {
		t.Fatalf("all: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("want 3, got %d", len(rows))
	}
	if !rows[1].Lost || rows[1].Seq != 2 {
		t.Fatalf("row 2 should be the lost one: %+v", rows[1])
	}
}
```

Create `internal/mesh/offsets_test.go`:
```go
package mesh

import "testing"

func TestOffsetsStoreAndGet(t *testing.T) {
	o := NewOffsets()
	if _, ok := o.Get("ncase"); ok {
		t.Fatal("unknown agent should not be present")
	}
	o.Set("ncase", Offset{OffsetUS: 1234, RTTus: 200, Reliable: true})
	got, ok := o.Get("ncase")
	if !ok || got.OffsetUS != 1234 {
		t.Fatalf("get wrong: %+v ok=%v", got, ok)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/store/ -run All ./internal/mesh/ -run Offsets`
Expected: FAIL — `undefined: AgentSamplesAll` / `NewOffsets`.

- [ ] **Step 3: Add `AgentSamplesAll`**

At the end of `internal/store/store.go`:
```go
// AgentSamplesAll returns all aggregated rows for agentID ordered by seq.
func (s *Store) AgentSamplesAll(agentID string) ([]Sample, error) {
	rows, err := s.db.Query(
		`SELECT seq,ts_unix_us,probe_type,src_host,dst_host,
		        COALESCE(direction,''),COALESCE(rtt_us,0),COALESCE(jitter_us,0),lost
		 FROM agent_samples WHERE agent_id=? ORDER BY seq`, agentID)
	if err != nil {
		return nil, fmt.Errorf("agent samples all: %w", err)
	}
	defer rows.Close()
	var out []Sample
	for rows.Next() {
		var sm Sample
		var lostInt int
		if err := rows.Scan(&sm.Seq, &sm.TSUnixUS, &sm.ProbeType, &sm.SrcHost,
			&sm.DstHost, &sm.Direction, &sm.RTTus, &sm.JitterUS, &lostInt); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		sm.Lost = lostInt == 1
		out = append(out, sm)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Add the Offsets store**

Create `internal/mesh/offsets.go`:
```go
package mesh

import "sync"

// Offsets is a thread-safe map of agent id -> measured clock Offset.
type Offsets struct {
	mu sync.RWMutex
	m  map[string]Offset
}

// NewOffsets returns an empty Offsets store.
func NewOffsets() *Offsets { return &Offsets{m: make(map[string]Offset)} }

// Set records the offset for an agent.
func (o *Offsets) Set(id string, off Offset) {
	o.mu.Lock()
	o.m[id] = off
	o.mu.Unlock()
}

// Get returns the offset for an agent and whether it is present.
func (o *Offsets) Get(id string) (Offset, bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	off, ok := o.m[id]
	return off, ok
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/store/ ./internal/mesh/`
Expected: PASS

- [ ] **Step 6: Commit**

```powershell
git add -A
git commit -m @'
feat(store,mesh): read aggregated samples + thread-safe offset store

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
'@
```

---

### Task 3: `correlate.DetectEvents`

**Files:**
- Create: `internal/correlate/events.go`, `internal/correlate/events_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/correlate/events_test.go`:
```go
package correlate

import (
	"testing"

	"netlogger/internal/store"
)

func sample(seq, ts int64, dst string, lost bool) store.Sample {
	return store.Sample{Seq: seq, TSUnixUS: ts, ProbeType: "icmp", SrcHost: "ncase", DstHost: dst, Lost: lost}
}

func TestDetectEventsMergesConsecutiveLosses(t *testing.T) {
	samples := []store.Sample{
		sample(1, 100, "ryzen", false),
		sample(2, 200, "ryzen", true), // event A start
		sample(3, 300, "ryzen", true), // event A continues
		sample(4, 400, "ryzen", false),
		sample(5, 500, "ryzen", true), // event B (single)
		sample(6, 600, "ryzen", false),
	}
	ev := DetectEvents("ncase", samples)
	if len(ev) != 2 {
		t.Fatalf("want 2 events, got %d (%+v)", len(ev), ev)
	}
	if ev[0].StartUS != 200 || ev[0].EndUS != 300 || ev[0].DurationUS != 100 {
		t.Fatalf("event A bounds wrong: %+v", ev[0])
	}
	if ev[1].StartUS != 500 || ev[1].EndUS != 500 {
		t.Fatalf("event B bounds wrong: %+v", ev[1])
	}
}

func TestDetectEventsSeparatesPathsByDst(t *testing.T) {
	samples := []store.Sample{
		sample(1, 100, "ryzen", true),
		sample(2, 110, "nas", true),
	}
	ev := DetectEvents("ncase", samples)
	if len(ev) != 2 {
		t.Fatalf("want 2 events (one per dst), got %d", len(ev))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/correlate/`
Expected: FAIL — `undefined: DetectEvents`.

- [ ] **Step 3: Write the implementation**

Create `internal/correlate/events.go`:
```go
// Package correlate detects per-path failure events and correlates them across
// hosts by uncertainty-interval overlap (spec §6, §9).
package correlate

import (
	"sort"

	"netlogger/internal/store"
)

// Event is a maximal run of consecutive losses on one (agent, src->dst) path,
// in the agent's local clock.
type Event struct {
	AgentID    string `json:"agent_id"`
	Src        string `json:"src"`
	Dst        string `json:"dst"`
	StartUS    int64  `json:"start_us"`
	EndUS      int64  `json:"end_us"`
	DurationUS int64  `json:"duration_us"`
}

// DetectEvents finds failure events per destination in an agent's samples.
func DetectEvents(agentID string, samples []store.Sample) []Event {
	byDst := map[string][]store.Sample{}
	for _, s := range samples {
		byDst[s.DstHost] = append(byDst[s.DstHost], s)
	}
	var events []Event
	for dst, rows := range byDst {
		sort.Slice(rows, func(i, j int) bool { return rows[i].TSUnixUS < rows[j].TSUnixUS })
		var cur *Event
		for _, s := range rows {
			if s.Lost {
				if cur == nil {
					cur = &Event{AgentID: agentID, Src: s.SrcHost, Dst: dst, StartUS: s.TSUnixUS, EndUS: s.TSUnixUS}
				} else {
					cur.EndUS = s.TSUnixUS
				}
			} else if cur != nil {
				cur.DurationUS = cur.EndUS - cur.StartUS
				events = append(events, *cur)
				cur = nil
			}
		}
		if cur != nil {
			cur.DurationUS = cur.EndUS - cur.StartUS
			events = append(events, *cur)
		}
	}
	sort.Slice(events, func(i, j int) bool { return events[i].StartUS < events[j].StartUS })
	return events
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/correlate/`
Expected: PASS

- [ ] **Step 5: Commit**

```powershell
git add -A
git commit -m @'
feat(correlate): per-path failure event detection

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
'@
```

---

### Task 4: `correlate.Correlate` (interval-overlap grouping)

**Files:**
- Create: `internal/correlate/correlate.go`, `internal/correlate/correlate_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/correlate/correlate_test.go`:
```go
package correlate

import "testing"

// noOffset gives every agent a zero offset and a small fixed uncertainty.
func noOffset(string) (int64, int64) { return 0, 5 }

func TestCorrelateSimultaneousAcrossAgents(t *testing.T) {
	events := []Event{
		{AgentID: "ncase", Dst: "ryzen", StartUS: 1000, EndUS: 1100, DurationUS: 100},
		{AgentID: "nas", Dst: "ryzen", StartUS: 1050, EndUS: 1150, DurationUS: 100},
	}
	groups := Correlate(events, noOffset)
	if len(groups) != 1 {
		t.Fatalf("overlapping events should form 1 group, got %d", len(groups))
	}
	if !groups[0].Simultaneous {
		t.Fatal("group spanning 2 agents should be simultaneous (shared device)")
	}
}

func TestCorrelateIndependentWhenDisjoint(t *testing.T) {
	events := []Event{
		{AgentID: "ncase", Dst: "ryzen", StartUS: 1000, EndUS: 1100, DurationUS: 100},
		{AgentID: "nas", Dst: "ryzen", StartUS: 9000, EndUS: 9100, DurationUS: 100},
	}
	groups := Correlate(events, noOffset)
	if len(groups) != 2 {
		t.Fatalf("disjoint events should be 2 groups, got %d", len(groups))
	}
	for _, g := range groups {
		if g.Simultaneous {
			t.Fatal("single-agent group must not be simultaneous")
		}
	}
}

func TestCorrelateOffsetAlignsClocks(t *testing.T) {
	// nas clock is +2000us ahead; after correction the events line up.
	off := func(id string) (int64, int64) {
		if id == "nas" {
			return 2000, 50
		}
		return 0, 50
	}
	events := []Event{
		{AgentID: "ncase", Dst: "ryzen", StartUS: 1000, EndUS: 1100},
		{AgentID: "nas", Dst: "ryzen", StartUS: 3000, EndUS: 3100}, // local; -2000 => 1000
	}
	groups := Correlate(events, off)
	if len(groups) != 1 || !groups[0].Simultaneous {
		t.Fatalf("offset-corrected events should correlate: %+v", groups)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/correlate/ -run Correlate`
Expected: FAIL — `undefined: Correlate`.

- [ ] **Step 3: Write the implementation**

Create `internal/correlate/correlate.go`:
```go
package correlate

import "sort"

// OffsetFunc returns, for an agent id, its (offsetUS, halfUncUS): the clock
// offset (agent-coordinator) and the uncertainty half-width to widen intervals.
type OffsetFunc func(agentID string) (offsetUS, halfUncUS int64)

// Corrected is an event shifted into coordinator time with an uncertainty band.
type Corrected struct {
	Event
	LoUS int64 `json:"lo_us"`
	HiUS int64 `json:"hi_us"`
}

// Group is a set of events whose uncertainty intervals overlap.
type Group struct {
	Events       []Corrected `json:"events"`
	Simultaneous bool        `json:"simultaneous"` // spans >1 agent => shared device
}

// Correlate shifts events into coordinator time, widens them by clock
// uncertainty + duration, and groups those whose intervals overlap.
func Correlate(events []Event, off OffsetFunc) []Group {
	corr := make([]Corrected, 0, len(events))
	for _, e := range events {
		offset, half := off(e.AgentID)
		lo := (e.StartUS - offset) - half
		hi := (e.EndUS - offset) + half
		corr = append(corr, Corrected{Event: e, LoUS: lo, HiUS: hi})
	}
	sort.Slice(corr, func(i, j int) bool { return corr[i].LoUS < corr[j].LoUS })

	var groups []Group
	for _, c := range corr {
		if n := len(groups); n > 0 && c.LoUS <= groupHi(groups[n-1]) {
			groups[n-1].Events = append(groups[n-1].Events, c)
		} else {
			groups = append(groups, Group{Events: []Corrected{c}})
		}
	}
	for i := range groups {
		groups[i].Simultaneous = distinctAgents(groups[i].Events) > 1
	}
	return groups
}

func groupHi(g Group) int64 {
	max := g.Events[0].HiUS
	for _, e := range g.Events {
		if e.HiUS > max {
			max = e.HiUS
		}
	}
	return max
}

func distinctAgents(events []Corrected) int {
	seen := map[string]bool{}
	for _, e := range events {
		seen[e.AgentID] = true
	}
	return len(seen)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/correlate/`
Expected: PASS

- [ ] **Step 5: Commit**

```powershell
git add -A
git commit -m @'
feat(correlate): interval-overlap correlation (simultaneous vs independent)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
'@
```

---

### Task 5: `score` — topology paths + per-component health/coverage

**Files:**
- Create: `internal/score/score.go`, `internal/score/score_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/score/score_test.go`:
```go
package score

import (
	"testing"

	"netlogger/internal/config"
)

// chain: ryzen - switch2 - switch1 - ncase  (a small linear topology)
func chainConfig() *config.Config {
	return &config.Config{
		Nodes: []config.Node{
			{ID: "ryzen", Type: config.NodeEndpoint, Label: "Ryzen", Address: "127.0.0.1:1"},
			{ID: "switch2", Type: config.NodeSwitch, Label: "Switch 2"},
			{ID: "switch1", Type: config.NodeSwitch, Label: "Switch 1"},
			{ID: "ncase", Type: config.NodeEndpoint, Label: "NCASE", Address: "127.0.0.1:2"},
		},
		Links: [][]string{{"ryzen", "switch2"}, {"switch2", "switch1"}, {"switch1", "ncase"}},
	}
}

func find(cs []Component, id string) Component {
	for _, c := range cs {
		if c.ID == id {
			return c
		}
	}
	return Component{}
}

func TestPathBetweenEndpoints(t *testing.T) {
	g := buildGraph(chainConfig())
	path := g.path("ryzen", "ncase")
	want := []string{"ryzen", "switch2", "switch1", "ncase"}
	if len(path) != len(want) {
		t.Fatalf("path %v != %v", path, want)
	}
	for i := range want {
		if path[i] != want[i] {
			t.Fatalf("path %v != %v", path, want)
		}
	}
}

func TestScoreSharedSwitchPoorWhenPathFails(t *testing.T) {
	cfg := chainConfig()
	tested := map[string]bool{key("ryzen", "ncase"): true}
	failing := map[string]bool{key("ryzen", "ncase"): true}
	cs := Score(cfg, tested, failing)

	// Every component on the only tested+failing path is implicated.
	if h := find(cs, "switch1").Health; h != "poor" {
		t.Fatalf("switch1 should be poor, got %q", h)
	}
	if h := find(cs, "switch2").Health; h != "poor" {
		t.Fatalf("switch2 should be poor, got %q", h)
	}
}

func TestScoreCleanPathIsGoodAndUntestedIsUntested(t *testing.T) {
	cfg := chainConfig()
	tested := map[string]bool{key("ryzen", "ncase"): true}
	failing := map[string]bool{} // none failing
	cs := Score(cfg, tested, failing)

	if h := find(cs, "switch1").Health; h != "good" {
		t.Fatalf("switch1 on a clean tested path should be good, got %q", h)
	}
	// A node on no tested path is untested. Add an isolated node.
	cfg.Nodes = append(cfg.Nodes, config.Node{ID: "lonely", Type: config.NodeEndpoint})
	cs = Score(cfg, tested, failing)
	if h := find(cs, "lonely").Health; h != "untested" {
		t.Fatalf("lonely node should be untested, got %q", h)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/score/`
Expected: FAIL — `undefined: buildGraph`.

- [ ] **Step 3: Write the implementation**

Create `internal/score/score.go`:
```go
// Package score attributes tested/failing host-pairs to the components on their
// topology path and assigns per-component health + coverage (spec §9a).
package score

import (
	"sort"

	"netlogger/internal/config"
)

// Component is one scored network element.
type Component struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	Health       string `json:"health"`   // good|fair|poor|untested
	Coverage     string `json:"coverage"` // none|light|partial|thorough
	TestedPaths  int    `json:"tested_paths"`
	FailingPaths int    `json:"failing_paths"`
}

// key is the unordered host-pair key.
func key(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}

type graph struct {
	adj map[string][]string
}

func buildGraph(cfg *config.Config) graph {
	g := graph{adj: map[string][]string{}}
	for _, n := range cfg.Nodes {
		if _, ok := g.adj[n.ID]; !ok {
			g.adj[n.ID] = nil
		}
	}
	for _, l := range cfg.Links {
		g.adj[l[0]] = append(g.adj[l[0]], l[1])
		g.adj[l[1]] = append(g.adj[l[1]], l[0])
	}
	return g
}

// path returns the BFS shortest path of node ids from src to dst (inclusive),
// or nil if unreachable.
func (g graph) path(src, dst string) []string {
	if src == dst {
		return []string{src}
	}
	prev := map[string]string{src: ""}
	queue := []string{src}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		neigh := append([]string{}, g.adj[cur]...)
		sort.Strings(neigh) // deterministic
		for _, nx := range neigh {
			if _, seen := prev[nx]; seen {
				continue
			}
			prev[nx] = cur
			if nx == dst {
				return rebuild(prev, src, dst)
			}
			queue = append(queue, nx)
		}
	}
	return nil
}

func rebuild(prev map[string]string, src, dst string) []string {
	var rev []string
	for at := dst; at != ""; at = prev[at] {
		rev = append(rev, at)
		if at == src {
			break
		}
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

func coverageLabel(n int) string {
	switch {
	case n <= 0:
		return "none"
	case n == 1:
		return "light"
	case n <= 3:
		return "partial"
	default:
		return "thorough"
	}
}

// Score walks each addressed host-pair's topology path and attributes the
// tested/failing status to every component on it, then labels health+coverage.
func Score(cfg *config.Config, tested, failing map[string]bool) []Component {
	g := buildGraph(cfg)

	// addressed endpoints = the things that probe each other
	var endpoints []string
	for _, n := range cfg.Nodes {
		if n.Address != "" {
			endpoints = append(endpoints, n.ID)
		}
	}

	testedThrough := map[string]int{}
	failingThrough := map[string]int{}
	for i := 0; i < len(endpoints); i++ {
		for j := i + 1; j < len(endpoints); j++ {
			a, b := endpoints[i], endpoints[j]
			k := key(a, b)
			if !tested[k] {
				continue
			}
			for _, nodeID := range g.path(a, b) {
				testedThrough[nodeID]++
				if failing[k] {
					failingThrough[nodeID]++
				}
			}
		}
	}

	var out []Component
	for _, n := range cfg.Nodes {
		tp := testedThrough[n.ID]
		fp := failingThrough[n.ID]
		c := Component{ID: n.ID, Label: n.Label, TestedPaths: tp, FailingPaths: fp, Coverage: coverageLabel(tp)}
		switch {
		case tp == 0:
			c.Health = "untested"
		case fp == 0:
			c.Health = "good"
		case fp == tp:
			c.Health = "poor"
		default:
			c.Health = "fair"
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/score/`
Expected: PASS

- [ ] **Step 5: Commit**

```powershell
git add -A
git commit -m @'
feat(score): topology path-finding + per-component health/coverage

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
'@
```

---

### Task 6: Wire endpoints + offset loop + e2e

**Files:**
- Modify: `internal/coordinator/coordinator.go` (add `CorrelationHandler`, `ComponentsHandler`)
- Modify: `internal/web/web.go` (routes), `internal/web/static/index.html` (components table)
- Modify: `internal/agentsvc/agentsvc.go` (offset loop; wire handlers)
- Create: `internal/coordinator/correlation_test.go` (e2e)

- [ ] **Step 1: Write the failing coordinator/e2e test**

Create `internal/coordinator/correlation_test.go`:
```go
package coordinator

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"netlogger/internal/config"
	"netlogger/internal/score"
	"netlogger/internal/store"
)

func TestComponentsHandlerScoresFromAggregatedSamples(t *testing.T) {
	agg, err := store.Open(filepath.Join(t.TempDir(), "agg.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { agg.Close() })
	// ncase -> ryzen path, one failing sample.
	_ = agg.Upsert("ncase", store.Sample{Seq: 1, TSUnixUS: 100, ProbeType: "icmp", SrcHost: "ncase", DstHost: "ryzen", Lost: true})

	cfg := &config.Config{
		Nodes: []config.Node{
			{ID: "ncase", Type: config.NodeEndpoint, Label: "NCASE", Address: "127.0.0.1:1"},
			{ID: "switch1", Type: config.NodeSwitch, Label: "Switch 1"},
			{ID: "ryzen", Type: config.NodeEndpoint, Label: "Ryzen", Address: "127.0.0.1:2"},
		},
		Links: [][]string{{"ncase", "switch1"}, {"switch1", "ryzen"}},
	}

	h := ComponentsHandler(agg, cfg)
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/api/components", nil))
	var comps []score.Component
	if err := json.Unmarshal(rr.Body.Bytes(), &comps); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var sw score.Component
	for _, c := range comps {
		if c.ID == "switch1" {
			sw = c
		}
	}
	if sw.Health != "poor" {
		t.Fatalf("switch1 should be poor from the failing path, got %q (%+v)", sw.Health, comps)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/coordinator/ -run Components`
Expected: FAIL — `undefined: ComponentsHandler`.

- [ ] **Step 3: Add coordinator handlers**

Append to `internal/coordinator/coordinator.go` (add imports `"netlogger/internal/correlate"`, `"netlogger/internal/score"`, `"netlogger/internal/store"`):
```go
// CorrelationHandler detects events across all aggregated agents and correlates
// them, returning the groups as JSON.
func CorrelationHandler(agg *store.Store, agentIDs []string, offsets *mesh.Offsets) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var events []correlate.Event
		for _, id := range agentIDs {
			rows, err := agg.AgentSamplesAll(id)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			events = append(events, correlate.DetectEvents(id, rows)...)
		}
		groups := correlate.Correlate(events, func(id string) (int64, int64) {
			if off, ok := offsets.Get(id); ok && off.Reliable {
				return off.OffsetUS, off.HalfUncUS()
			}
			return 0, 1000 // unknown clock: 1ms floor
		})
		writeJSON(w, groups)
	}
}

// ComponentsHandler scores every component from the aggregated samples: a
// host-pair is "tested" if it has any sample and "failing" if it has any event.
func ComponentsHandler(agg *store.Store, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tested := map[string]bool{}
		failing := map[string]bool{}
		for _, n := range cfg.Nodes {
			if n.Address == "" {
				continue
			}
			rows, err := agg.AgentSamplesAll(n.ID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			for _, s := range rows {
				tested[score.Key(s.SrcHost, s.DstHost)] = true
			}
			for _, e := range correlate.DetectEvents(n.ID, rows) {
				failing[score.Key(e.Src, e.Dst)] = true
			}
		}
		writeJSON(w, score.Score(cfg, tested, failing))
	}
}
```

Then in `internal/score/score.go`, export the key helper by adding (keep the lowercase `key` too, or replace usages): add
```go
// Key is the unordered host-pair key (exported for callers building maps).
func Key(a, b string) string { return key(a, b) }
```

- [ ] **Step 4: Add web routes**

In `internal/web/web.go`, add to the `Server` struct two more optional fields and register them in `Handler`:
```go
	CorrelationHandler http.HandlerFunc // optional
	ComponentsHandler  http.HandlerFunc // optional
```
And in `Handler`, after the readiness route:
```go
	mux.HandleFunc("/api/correlation", orEmptyArray(s.CorrelationHandler))
	mux.HandleFunc("/api/components", orEmptyArray(s.ComponentsHandler))
```

- [ ] **Step 5: Render components in the page**

In `internal/web/static/index.html`, before the closing `</script>`, add a components fetch and a heading `<h2>Components</h2><div id="components"></div>` right after the Readiness section in the body:
```html
  <h2>Components</h2><table id="components"><thead><tr><th>component</th><th>health</th><th>coverage</th><th>tested</th><th>failing</th></tr></thead><tbody></tbody></table>
```
And the script block:
```html
fetch('/api/components').then(r=>r.json()).then(rows=>{
  const tb=document.querySelector('#components tbody');
  rows.forEach(c=>{const tr=document.createElement('tr');
    const cls=c.health==='good'?'ok':(c.health==='untested'?'':'bad');
    tr.innerHTML=`<td>${c.label||c.id}</td><td class="${cls}">${c.health}</td><td>${c.coverage}</td><td>${c.tested_paths}</td><td>${c.failing_paths}</td>`;
    tb.appendChild(tr);});
});
```

- [ ] **Step 6: Label probe samples by peer node id (correctness fix)**

Scoring keys host-pairs by **node id**, but `probeLoop` currently records `DstHost` as the peer's IP. Fix it to label by node id while still pinging the IP. In `internal/agentsvc/agentsvc.go`, replace the whole `probeLoop` method:
```go
func (p *Program) probeLoop(ctx context.Context, src string, peers []config.TargetRef) {
	hostByID := make(map[string]string, len(peers))
	targets := make([]string, 0, len(peers))
	for _, t := range peers {
		hostByID[t.ID] = t.ProbeHost()
		targets = append(targets, t.ID)
	}
	if len(targets) == 0 {
		hostByID["self"] = "127.0.0.1" // lone node: self-ping proof of life
		targets = []string{"self"}
	}
	// Ping resolves a node id to its host and pings it; the sample is labeled
	// with the node id (what scoring/correlation key off).
	ping := func(nodeID string, timeout time.Duration) (probe.Result, error) {
		return probe.PingICMP(hostByID[nodeID], timeout)
	}
	runner := &probe.Runner{
		Store:   p.store,
		Clock:   clock.System{},
		Src:     src,
		Targets: targets,
		Ping:    ping,
		Timeout: 2 * time.Second,
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			_ = runner.Tick()
		}
	}
}
```

- [ ] **Step 7: Wire offset loop + handlers into agentsvc**

In `internal/agentsvc/agentsvc.go`: add a `offsets *mesh.Offsets` field to `Program`. In `Start`, inside the `if self.Role == "coordinator"` block (where the puller is created), add:
```go
		p.offsets = mesh.NewOffsets()
		ids := agentIDs(cfg)
		ws.CorrelationHandler = coordinator.CorrelationHandler(st, ids, p.offsets)
		ws.ComponentsHandler = coordinator.ComponentsHandler(st, cfg)
```
And after `go p.pullLoop(...)` (still inside the coordinator branch at the end), add:
```go
		go p.offsetLoop(ctx, cfg.AddressedNodes())
```
Add these helpers at the end of the file:
```go
func agentIDs(cfg *config.Config) []string {
	var ids []string
	for _, t := range cfg.AddressedNodes() {
		ids = append(ids, t.ID)
	}
	return ids
}

func (p *Program) offsetLoop(ctx context.Context, nodes []config.TargetRef) {
	client := &http.Client{Timeout: 4 * time.Second}
	tick := time.NewTicker(60 * time.Second)
	defer tick.Stop()
	measure := func() {
		for _, n := range nodes {
			if off, err := mesh.MeasureOffset(client, n.BaseURL(), 8); err == nil {
				p.offsets.Set(n.ID, off)
			}
		}
	}
	measure() // once at startup
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			measure()
		}
	}
}
```

- [ ] **Step 8: Build, vet, full suite**

Run:
```powershell
go build -o bin/netlogger.exe ./cmd/netlogger
go vet ./...
go test ./...
```
Expected: builds; vet clean; all PASS (including the new components e2e test).

- [ ] **Step 9: Manual verification**

Run the two-node localhost pair as in M2b (fresh db names `m3_*`), wait ~12s, then:
```powershell
Invoke-RestMethod "http://127.0.0.1:8088/api/components" | ConvertTo-Json -Depth 4 -Compress
Invoke-RestMethod "http://127.0.0.1:8088/api/correlation" | ConvertTo-Json -Depth 6 -Compress
Get-Process netlogger -ErrorAction SilentlyContinue | Stop-Process -Force
```
Expected: `/api/components` lists each config node with a health + coverage; `/api/correlation` returns any detected groups (likely empty or a few single-agent groups on a healthy localhost). Open `http://127.0.0.1:8088/` to see the Components table.

- [ ] **Step 10: Commit + push**

```powershell
git add -A
git commit -m @'
feat: clock offset loop + correlation + components endpoints and GUI

Coordinator measures per-agent clock offset (min-delta), detects per-path
failure events, correlates them by uncertainty-interval overlap, and scores
each topology component health+coverage at /api/correlation and /api/components.

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
'@
git push
```

---

## Done criteria for M3

- `go test ./...` passes, including offset recovery/clamp, event detection, interval-overlap correlation (simultaneous/independent/offset-aligned), topology path-finding, component scoring, and the components e2e.
- A coordinator serves `/api/correlation` and `/api/components`; the page renders the components table.
- Pushed to `origin/main`.

**Next (M4):** wrap iperf3 for load tests (RRUL envelope), collect NIC counters, add the bufferbloat-vs-fault and LAN-vs-WAN classifiers, and the Tests view — the load-triggered half of the diagnosis.
