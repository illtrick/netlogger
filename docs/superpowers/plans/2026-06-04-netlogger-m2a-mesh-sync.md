# NetLogger M2a — Config-Driven Mesh + Resilient Sync — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make probe targets come from the network config file, and let a designated **coordinator** node resiliently pull every agent's local samples into one aggregated store via a cursor-based, idempotent sync that survives drops and restarts.

**Architecture:** Every node runs the agent role: it probes its peers (resolved from config) into its local WAL store and serves them over HTTP (`/api/samples?since=N`). The coordinator additionally runs a **puller** that, per agent, fetches "everything since my cursor," upserts on `(agent_id, seq)` (so duplicates from retries/overlap are no-ops), and advances its cursor only after a successful upsert. The disposable HTTP request is the liveness check; the durable cursor is the resume mechanism (spec §7).

**Tech Stack:** Builds on M1 (Go, `modernc.org/sqlite`, `pro-bing`, `kardianos/service`). No new deps. New package `internal/mesh` (named `mesh`, not `sync`, to avoid shadowing stdlib `sync`).

**Spec reference:** `docs/superpowers/specs/2026-06-04-netlogger-design.md` — §7 (resilient sync), §8 (store), §3 (control plane star / data plane peer-to-peer), §2a (config file). This plan is M2a; readiness checks + Agents/Config GUI views are M2b.

---

### Task 1: Config — resolve this node + its peers

**Files:**
- Modify: `internal/config/config.go` (add `TargetRef`, `Resolve`, `AddressedNodes`)
- Test: `internal/config/resolve_test.go`

Convention: an endpoint node's `address` is its **control endpoint** `host:port` (e.g. `127.0.0.1:8088`). Peers are probed at the host part; the coordinator pulls from `http://<address>`.

- [ ] **Step 1: Write the failing test**

Create `internal/config/resolve_test.go`:
```go
package config

import "testing"

func sampleConfig() *Config {
	return &Config{
		Nodes: []Node{
			{ID: "ryzen", Type: NodeEndpoint, Label: "Ryzen", Address: "127.0.0.1:8088", Role: "coordinator"},
			{ID: "ncase", Type: NodeEndpoint, Label: "NCASE", Address: "127.0.0.1:8089"},
			{ID: "switch1", Type: NodeSwitch, Label: "Switch 1"}, // no address -> not a probe/pull target
		},
	}
}

func TestResolveReturnsSelfAndPeers(t *testing.T) {
	c := sampleConfig()
	self, peers, err := c.Resolve("ryzen")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if self.ID != "ryzen" {
		t.Fatalf("self wrong: %+v", self)
	}
	if len(peers) != 1 || peers[0].ID != "ncase" || peers[0].Address != "127.0.0.1:8089" {
		t.Fatalf("peers wrong: %+v", peers)
	}
}

func TestResolveUnknownNode(t *testing.T) {
	if _, _, err := sampleConfig().Resolve("ghost"); err == nil {
		t.Fatal("expected error for unknown node id")
	}
}

func TestTargetRefHelpers(t *testing.T) {
	tr := TargetRef{ID: "ncase", Address: "127.0.0.1:8089"}
	if tr.BaseURL() != "http://127.0.0.1:8089" {
		t.Fatalf("BaseURL wrong: %s", tr.BaseURL())
	}
	if tr.ProbeHost() != "127.0.0.1" {
		t.Fatalf("ProbeHost wrong: %s", tr.ProbeHost())
	}
}

func TestAddressedNodesIncludesAllWithAddress(t *testing.T) {
	got := sampleConfig().AddressedNodes()
	if len(got) != 2 {
		t.Fatalf("want 2 addressed nodes, got %d (%+v)", len(got), got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run "Resolve|TargetRef|Addressed"`
Expected: FAIL — `undefined: TargetRef` / `Resolve`.

- [ ] **Step 3: Write the implementation**

Append to `internal/config/config.go` (add `"net"` to the import block):
```go
// TargetRef is a config node that has a control address — a probe target and a
// node the coordinator can pull from.
type TargetRef struct {
	ID      string
	Address string // control endpoint host:port, e.g. "127.0.0.1:8088"
}

// BaseURL is the coordinator-facing HTTP base for this target.
func (t TargetRef) BaseURL() string { return "http://" + t.Address }

// ProbeHost is the host portion of the address (what ICMP/UDP probes target).
func (t TargetRef) ProbeHost() string {
	host, _, err := net.SplitHostPort(t.Address)
	if err != nil {
		return t.Address
	}
	return host
}

// AddressedNodes returns every node that has a control address (probe + pull set).
func (c *Config) AddressedNodes() []TargetRef {
	var out []TargetRef
	for _, n := range c.Nodes {
		if n.Address != "" {
			out = append(out, TargetRef{ID: n.ID, Address: n.Address})
		}
	}
	return out
}

// Resolve returns the node with id nodeID and the list of its peers to probe
// (all addressed nodes except itself).
func (c *Config) Resolve(nodeID string) (Node, []TargetRef, error) {
	self, ok := c.Node(nodeID)
	if !ok {
		return Node{}, nil, fmt.Errorf("node %q not found in config", nodeID)
	}
	var peers []TargetRef
	for _, t := range c.AddressedNodes() {
		if t.ID != self.ID {
			peers = append(peers, t)
		}
	}
	return self, peers, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/`
Expected: PASS

- [ ] **Step 5: Commit**

```powershell
git add -A
git commit -m @'
feat(config): resolve this node + peers from config (control addresses)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
'@
```

---

### Task 2: Store — aggregated table, idempotent Upsert, cursor

**Files:**
- Modify: `internal/store/store.go` (json tags on `Sample`; aggregated schema; `Upsert`, `Cursor`, `SetCursor`, `CountAgentSamples`)
- Test: `internal/store/aggregate_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/store/aggregate_test.go`:
```go
package store

import (
	"path/filepath"
	"testing"
)

func TestUpsertIsIdempotent(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "agg.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	sm := Sample{Seq: 7, TSUnixUS: 100, ProbeType: "icmp", SrcHost: "ncase", DstHost: "ryzen", Direction: "rtt", RTTus: 900}

	if err := s.Upsert("ncase", sm); err != nil {
		t.Fatalf("upsert 1: %v", err)
	}
	// Same (agent_id, seq) again -> must be a no-op, not an error or a dup.
	if err := s.Upsert("ncase", sm); err != nil {
		t.Fatalf("upsert 2: %v", err)
	}
	n, err := s.CountAgentSamples("ncase")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 row after duplicate upsert, got %d", n)
	}
}

func TestCursorRoundTrip(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "cur.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	// Unknown agent -> cursor 0.
	got, err := s.Cursor("ncase")
	if err != nil {
		t.Fatalf("cursor: %v", err)
	}
	if got != 0 {
		t.Fatalf("want 0 for new agent, got %d", got)
	}
	if err := s.SetCursor("ncase", 42); err != nil {
		t.Fatalf("set: %v", err)
	}
	if got, _ := s.Cursor("ncase"); got != 42 {
		t.Fatalf("want 42, got %d", got)
	}
	// Overwrites.
	if err := s.SetCursor("ncase", 99); err != nil {
		t.Fatalf("set2: %v", err)
	}
	if got, _ := s.Cursor("ncase"); got != 99 {
		t.Fatalf("want 99, got %d", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run "Upsert|Cursor"`
Expected: FAIL — `s.Upsert undefined`.

- [ ] **Step 3: Add json tags to Sample**

In `internal/store/store.go`, replace the `Sample` struct definition with the json-tagged version:
```go
// Sample is one probe measurement. A lost probe has Lost=true and RTTus=0
// (persisted as NULL — never a sentinel, per spec §8).
type Sample struct {
	Seq       int64  `json:"seq"`
	TSUnixUS  int64  `json:"ts_unix_us"`
	ProbeType string `json:"probe_type"`
	SrcHost   string `json:"src_host"`
	DstHost   string `json:"dst_host"`
	Direction string `json:"direction"`
	RTTus     int64  `json:"rtt_us"`
	JitterUS  int64  `json:"jitter_us"`
	Lost      bool   `json:"lost"`
}
```

- [ ] **Step 4: Add the aggregated schema + methods**

In `internal/store/store.go`, extend the `schema` const by appending these two tables (add inside the backtick block, after the existing indexes):
```sql
CREATE TABLE IF NOT EXISTS agent_samples (
  agent_id   TEXT NOT NULL,
  seq        INTEGER NOT NULL,
  ts_unix_us INTEGER NOT NULL,
  probe_type TEXT NOT NULL,
  src_host   TEXT NOT NULL,
  dst_host   TEXT NOT NULL,
  direction  TEXT,
  rtt_us     INTEGER,
  jitter_us  INTEGER,
  lost       INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (agent_id, seq)
);
CREATE INDEX IF NOT EXISTS idx_agent_ts ON agent_samples(agent_id, ts_unix_us);
CREATE TABLE IF NOT EXISTS sync_cursors (
  agent_id TEXT PRIMARY KEY,
  last_seq INTEGER NOT NULL
);
```

Then add these methods at the end of `internal/store/store.go`:
```go
// Upsert inserts an aggregated sample from agentID, keyed (agent_id, seq).
// A repeated (agent_id, seq) is ignored — idempotent, so retry/overlap is safe.
func (s *Store) Upsert(agentID string, sm Sample) error {
	var rtt, jitter any
	if sm.Lost {
		rtt = nil
	} else {
		rtt = sm.RTTus
	}
	if sm.JitterUS != 0 {
		jitter = sm.JitterUS
	}
	lost := 0
	if sm.Lost {
		lost = 1
	}
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO agent_samples
		   (agent_id,seq,ts_unix_us,probe_type,src_host,dst_host,direction,rtt_us,jitter_us,lost)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		agentID, sm.Seq, sm.TSUnixUS, sm.ProbeType, sm.SrcHost, sm.DstHost, sm.Direction, rtt, jitter, lost)
	if err != nil {
		return fmt.Errorf("upsert agent sample: %w", err)
	}
	return nil
}

// Cursor returns the last durably-synced seq for agentID (0 if none yet).
func (s *Store) Cursor(agentID string) (int64, error) {
	var seq int64
	err := s.db.QueryRow(`SELECT last_seq FROM sync_cursors WHERE agent_id=?`, agentID).Scan(&seq)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read cursor: %w", err)
	}
	return seq, nil
}

// SetCursor stores the last durably-synced seq for agentID.
func (s *Store) SetCursor(agentID string, seq int64) error {
	_, err := s.db.Exec(
		`INSERT INTO sync_cursors (agent_id,last_seq) VALUES (?,?)
		 ON CONFLICT(agent_id) DO UPDATE SET last_seq=excluded.last_seq`,
		agentID, seq)
	if err != nil {
		return fmt.Errorf("set cursor: %w", err)
	}
	return nil
}

// CountAgentSamples returns the number of aggregated rows for agentID.
func (s *Store) CountAgentSamples(agentID string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_samples WHERE agent_id=?`, agentID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count agent samples: %w", err)
	}
	return n, nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/store/`
Expected: PASS (M1 store tests still green too).

- [ ] **Step 6: Commit**

```powershell
git add -A
git commit -m @'
feat(store): aggregated agent_samples (idempotent Upsert) + sync cursors

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
'@
```

---

### Task 3: Agent sync API (`/api/info`, `/api/samples`)

**Files:**
- Create: `internal/mesh/agentapi.go`
- Test: `internal/mesh/agentapi_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/mesh/agentapi_test.go`:
```go
package mesh

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"netlogger/internal/store"
)

func newAgentWithSamples(t *testing.T) (*AgentAPI, *store.Store) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	for i := 0; i < 3; i++ {
		if _, err := s.Insert(store.Sample{TSUnixUS: int64(100 + i), ProbeType: "icmp", SrcHost: "ncase", DstHost: "ryzen", Direction: "rtt", RTTus: 900}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	return &AgentAPI{Store: s, NodeID: "ncase", Host: "ncase-host"}, s
}

func TestAgentInfo(t *testing.T) {
	api, _ := newAgentWithSamples(t)
	rr := httptest.NewRecorder()
	api.Info(rr, httptest.NewRequest(http.MethodGet, "/api/info", nil))
	if rr.Code != 200 {
		t.Fatalf("code %d", rr.Code)
	}
	var info Info
	if err := json.Unmarshal(rr.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if info.NodeID != "ncase" || info.Host != "ncase-host" || info.TimeUnixUS == 0 {
		t.Fatalf("bad info: %+v", info)
	}
}

func TestAgentSamplesSince(t *testing.T) {
	api, _ := newAgentWithSamples(t)

	// since=0 -> all 3
	rr := httptest.NewRecorder()
	api.Samples(rr, httptest.NewRequest(http.MethodGet, "/api/samples?since=0&limit=100", nil))
	var all []store.Sample
	if err := json.Unmarshal(rr.Body.Bytes(), &all); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("want 3, got %d", len(all))
	}

	// since=2 -> only seq 3
	rr2 := httptest.NewRecorder()
	api.Samples(rr2, httptest.NewRequest(http.MethodGet, "/api/samples?since=2&limit=100", nil))
	var rest []store.Sample
	if err := json.Unmarshal(rr2.Body.Bytes(), &rest); err != nil {
		t.Fatalf("decode2: %v", err)
	}
	if len(rest) != 1 || rest[0].Seq != 3 {
		t.Fatalf("want only seq 3, got %+v", rest)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mesh/`
Expected: FAIL — `undefined: AgentAPI`.

- [ ] **Step 3: Write the implementation**

Create `internal/mesh/agentapi.go`:
```go
// Package mesh implements the agent sync API and the coordinator-side puller.
// Named "mesh" (not "sync") to avoid shadowing the standard library.
package mesh

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"netlogger/internal/store"
	"netlogger/internal/version"
)

// Info is the agent identity/health payload at /api/info.
type Info struct {
	NodeID     string `json:"node_id"`
	Host       string `json:"host"`
	Version    string `json:"version"`
	TimeUnixUS int64  `json:"time_unix_us"`
}

// AgentAPI serves an agent's local samples and identity to the coordinator.
type AgentAPI struct {
	Store  *store.Store
	NodeID string
	Host   string
}

// Info handles GET /api/info.
func (a *AgentAPI) Info(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(Info{
		NodeID:     a.NodeID,
		Host:       a.Host,
		Version:    version.Version,
		TimeUnixUS: time.Now().UTC().UnixMicro(),
	})
}

// Samples handles GET /api/samples?since=N&limit=M (defaults: since=0, limit=500).
func (a *AgentAPI) Samples(w http.ResponseWriter, r *http.Request) {
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	rows, err := a.Store.Since(since, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if rows == nil {
		rows = []store.Sample{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rows)
}

// Register mounts the agent API routes on mux.
func (a *AgentAPI) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/info", a.Info)
	mux.HandleFunc("/api/samples", a.Samples)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/mesh/`
Expected: PASS

- [ ] **Step 5: Commit**

```powershell
git add -A
git commit -m @'
feat(mesh): agent sync API — /api/info and /api/samples?since=

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
'@
```

---

### Task 4: Coordinator puller (cursor-based, idempotent, liveness)

**Files:**
- Create: `internal/mesh/puller.go`
- Test: `internal/mesh/puller_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/mesh/puller_test.go`:
```go
package mesh

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"netlogger/internal/store"
)

// liveAgent spins up a real agent HTTP server backed by a store with n samples.
func liveAgent(t *testing.T, n int) *httptest.Server {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "live.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	for i := 0; i < n; i++ {
		if _, err := s.Insert(store.Sample{TSUnixUS: int64(i), ProbeType: "icmp", SrcHost: "ncase", DstHost: "ryzen", Direction: "rtt", RTTus: 800}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	api := &AgentAPI{Store: s, NodeID: "ncase", Host: "ncase"}
	mux := http.NewServeMux()
	api.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newAgg(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "agg.db"))
	if err != nil {
		t.Fatalf("open agg: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPullOnceAggregatesAndAdvancesCursor(t *testing.T) {
	srv := liveAgent(t, 3)
	agg := newAgg(t)
	p := NewPuller(agg)

	ref := AgentRef{ID: "ncase", BaseURL: srv.URL}
	got, err := p.PullOnce(ref)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if got != 3 {
		t.Fatalf("want 3 pulled, got %d", got)
	}
	if n, _ := agg.CountAgentSamples("ncase"); n != 3 {
		t.Fatalf("want 3 aggregated rows, got %d", n)
	}
	if c, _ := agg.Cursor("ncase"); c != 3 {
		t.Fatalf("want cursor 3, got %d", c)
	}
	if st := p.State("ncase"); !st.Online {
		t.Fatalf("agent should be online after a good pull")
	}
}

func TestPullIsIdempotentAcrossRepeatsAndOverlap(t *testing.T) {
	srv := liveAgent(t, 3)
	agg := newAgg(t)
	p := NewPuller(agg)
	ref := AgentRef{ID: "ncase", BaseURL: srv.URL}

	if _, err := p.PullOnce(ref); err != nil {
		t.Fatalf("pull1: %v", err)
	}
	// Second pull: cursor is 3, nothing new.
	if got, _ := p.PullOnce(ref); got != 0 {
		t.Fatalf("second pull should fetch 0, got %d", got)
	}
	// Force re-read of already-seen rows (simulate a backfill overlap).
	if err := agg.SetCursor("ncase", 1); err != nil {
		t.Fatalf("rewind cursor: %v", err)
	}
	if _, err := p.PullOnce(ref); err != nil {
		t.Fatalf("overlap pull: %v", err)
	}
	// Idempotent: still exactly 3 rows, no duplicates.
	if n, _ := agg.CountAgentSamples("ncase"); n != 3 {
		t.Fatalf("want 3 rows after overlap, got %d", n)
	}
}

func TestPullMarksOfflineOnError(t *testing.T) {
	agg := newAgg(t)
	p := NewPuller(agg)
	// Unreachable URL.
	if _, err := p.PullOnce(AgentRef{ID: "dead", BaseURL: "http://127.0.0.1:1"}); err == nil {
		t.Fatal("expected error pulling from unreachable agent")
	}
	if st := p.State("dead"); st.Online {
		t.Fatal("agent should be marked offline after a failed pull")
	}
	if st := p.State("dead"); st.LastErr == "" {
		t.Fatal("offline state should carry LastErr")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mesh/ -run Pull`
Expected: FAIL — `undefined: NewPuller`.

- [ ] **Step 3: Write the implementation**

Create `internal/mesh/puller.go`:
```go
package mesh

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"netlogger/internal/store"
)

// AgentRef identifies an agent to pull from.
type AgentRef struct {
	ID      string
	BaseURL string // e.g. "http://127.0.0.1:8089"
}

// AgentState is the coordinator's view of one agent's liveness.
type AgentState struct {
	Online   bool
	LastSeen time.Time
	LastErr  string
}

// Puller pulls samples from agents into the aggregated store, resiliently.
type Puller struct {
	agg    *store.Store
	client *http.Client

	mu    sync.Mutex
	state map[string]AgentState
}

// NewPuller builds a Puller writing into agg.
func NewPuller(agg *store.Store) *Puller {
	return &Puller{
		agg:    agg,
		client: &http.Client{Timeout: 5 * time.Second},
		state:  make(map[string]AgentState),
	}
}

// State returns the last-known state for agentID.
func (p *Puller) State(id string) AgentState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state[id]
}

func (p *Puller) setState(id string, st AgentState) {
	p.mu.Lock()
	p.state[id] = st
	p.mu.Unlock()
}

// PullOnce fetches everything since the stored cursor for a, upserts it
// idempotently, and advances the cursor only after successful upserts.
func (p *Puller) PullOnce(a AgentRef) (int, error) {
	cursor, err := p.agg.Cursor(a.ID)
	if err != nil {
		return 0, err
	}
	url := a.BaseURL + "/api/samples?since=" + strconv.FormatInt(cursor, 10) + "&limit=500"
	resp, err := p.client.Get(url)
	if err != nil {
		p.setState(a.ID, AgentState{Online: false, LastErr: err.Error()})
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		e := fmt.Errorf("agent %s returned %d", a.ID, resp.StatusCode)
		p.setState(a.ID, AgentState{Online: false, LastErr: e.Error()})
		return 0, e
	}

	var samples []store.Sample
	if err := json.NewDecoder(resp.Body).Decode(&samples); err != nil {
		p.setState(a.ID, AgentState{Online: false, LastErr: err.Error()})
		return 0, err
	}

	maxSeq := cursor
	for _, sm := range samples {
		if err := p.agg.Upsert(a.ID, sm); err != nil {
			// Partial progress already persisted; report error without advancing cursor.
			return 0, err
		}
		if sm.Seq > maxSeq {
			maxSeq = sm.Seq
		}
	}
	if maxSeq > cursor {
		if err := p.agg.SetCursor(a.ID, maxSeq); err != nil {
			return len(samples), err
		}
	}
	p.setState(a.ID, AgentState{Online: true, LastSeen: time.Now()})
	return len(samples), nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/mesh/`
Expected: PASS

- [ ] **Step 5: Commit**

```powershell
git add -A
git commit -m @'
feat(mesh): coordinator puller — cursor-based idempotent sync + liveness

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
'@
```

---

### Task 5: Wire config-driven agent + coordinator pull loop + flags

**Files:**
- Modify: `internal/agentsvc/agentsvc.go` (config-driven targets; mount agent API; coordinator pull loop)
- Modify: `cmd/netlogger/main.go` (add `--config`, `--node`, `--listen` flags)
- Create (manual fixtures): `examples/two-node-localhost.yaml`

- [ ] **Step 1: Rewrite agentsvc to be config-driven**

Replace `internal/agentsvc/agentsvc.go` entirely:
```go
// Package agentsvc wires probes + sync API + (optional) coordinator pull loop
// into a kardianos service, driven by the network config file.
package agentsvc

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/kardianos/service"

	"netlogger/internal/clock"
	"netlogger/internal/config"
	"netlogger/internal/mesh"
	"netlogger/internal/probe"
	"netlogger/internal/store"
	"netlogger/internal/web"
)

// Program is the long-running node: probe loop + sync API + optional puller.
type Program struct {
	ConfigPath string
	NodeID     string
	DBPath     string
	Listen     string // host:port for this node's control server

	store  *store.Store
	srv    *http.Server
	puller *mesh.Puller
	cancel context.CancelFunc
}

// Start is called by the service manager; it must not block.
func (p *Program) Start(s service.Service) error {
	cfg, err := config.Load(p.ConfigPath)
	if err != nil {
		return err
	}
	self, peers, err := cfg.Resolve(p.NodeID)
	if err != nil {
		return err
	}

	st, err := store.Open(p.DBPath)
	if err != nil {
		return err
	}
	p.store = st

	host, _ := os.Hostname()
	api := &mesh.AgentAPI{Store: st, NodeID: self.ID, Host: host}
	ws := &web.Server{Host: host, ServiceState: "running"}

	root := http.NewServeMux()
	api.Register(root)         // /api/info, /api/samples
	root.Handle("/", ws.Handler())
	p.srv = &http.Server{Addr: p.Listen, Handler: root}

	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel

	go p.srv.ListenAndServe()
	go p.probeLoop(ctx, self.ID, peers)

	if self.Role == "coordinator" {
		p.puller = mesh.NewPuller(st)
		go p.pullLoop(ctx, cfg.AddressedNodes())
	}
	return nil
}

func (p *Program) probeLoop(ctx context.Context, src string, peers []config.TargetRef) {
	targets := make([]string, 0, len(peers))
	for _, t := range peers {
		targets = append(targets, t.ProbeHost())
	}
	if len(targets) == 0 {
		targets = []string{"127.0.0.1"} // lone node: self-ping proof of life
	}
	runner := &probe.Runner{
		Store:   p.store,
		Clock:   clock.System{},
		Src:     src,
		Targets: targets,
		Ping:    probe.PingICMP,
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

func (p *Program) pullLoop(ctx context.Context, nodes []config.TargetRef) {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			for _, n := range nodes {
				_, _ = p.puller.PullOnce(mesh.AgentRef{ID: n.ID, BaseURL: n.BaseURL()})
			}
		}
	}
}

// Stop is called by the service manager on shutdown.
func (p *Program) Stop(s service.Service) error {
	if p.cancel != nil {
		p.cancel()
	}
	if p.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = p.srv.Shutdown(ctx)
	}
	if p.store != nil {
		_ = p.store.Close()
	}
	return nil
}
```

- [ ] **Step 2: Add flags to main.go**

Replace `cmd/netlogger/main.go` entirely:
```go
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kardianos/service"

	"netlogger/internal/agentsvc"
	"netlogger/internal/version"
)

func dataDir() string {
	if pd := os.Getenv("ProgramData"); pd != "" {
		return filepath.Join(pd, "NetLogger")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".netlogger")
}

func main() {
	cfgPath := flag.String("config", filepath.Join(dataDir(), "network.yaml"), "path to network config file")
	nodeID := flag.String("node", "", "this machine's node id in the config (defaults to hostname)")
	listen := flag.String("listen", "127.0.0.1:8088", "control server host:port")
	dbName := flag.String("db", "netlogger.db", "sqlite db filename under the data dir")
	flag.Parse()

	args := flag.Args()
	if len(args) >= 1 && args[0] == "version" {
		fmt.Println("netlogger", version.Version)
		return
	}

	node := *nodeID
	if node == "" {
		node, _ = os.Hostname()
	}

	dir := dataDir()
	_ = os.MkdirAll(dir, 0o755)

	prog := &agentsvc.Program{
		ConfigPath: *cfgPath,
		NodeID:     node,
		DBPath:     filepath.Join(dir, *dbName),
		Listen:     *listen,
	}
	svcConfig := &service.Config{
		Name:        "NetLogger",
		DisplayName: "NetLogger Agent",
		Description: "NetLogger network diagnostic agent.",
		Arguments:   []string{"--config", *cfgPath, "--node", node, "--listen", *listen, "--db", *dbName},
	}
	s, err := service.New(prog, svcConfig)
	if err != nil {
		fmt.Fprintln(os.Stderr, "service init:", err)
		os.Exit(1)
	}

	if len(args) >= 1 {
		switch args[0] {
		case "install", "uninstall", "start", "stop":
			if err := service.Control(s, args[0]); err != nil {
				fmt.Fprintln(os.Stderr, args[0], "failed:", err)
				os.Exit(1)
			}
			fmt.Println("netlogger:", args[0], "ok")
			return
		case "run":
			// foreground
		default:
			fmt.Fprintln(os.Stderr, "usage: netlogger [flags] [version|install|uninstall|start|stop|run]")
			os.Exit(2)
		}
	}

	if err := s.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "run:", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 3: Build and run the full suite**

Run:
```powershell
go build -o bin/netlogger.exe ./cmd/netlogger
go test ./...
```
Expected: builds; all packages PASS.

- [ ] **Step 4: Create a two-node localhost config for manual verification**

Create `examples/two-node-localhost.yaml`:
```yaml
nodes:
  - id: coord
    type: endpoint
    label: "Coordinator"
    address: "127.0.0.1:8088"
    role: coordinator
  - id: agent1
    type: endpoint
    label: "Agent 1"
    address: "127.0.0.1:8089"
links:
  - [coord, agent1]
```

- [ ] **Step 5: Manual verification — two processes, resilient aggregation**

Open two shells (each prepends Go to PATH). Use distinct DB files and listen ports.

Shell A (agent1):
```powershell
$env:Path += ";C:\Program Files\Go\bin"
.\bin\netlogger.exe --config examples\two-node-localhost.yaml --node agent1 --listen 127.0.0.1:8089 --db agent1.db run
```
Shell B (coordinator):
```powershell
$env:Path += ";C:\Program Files\Go\bin"
.\bin\netlogger.exe --config examples\two-node-localhost.yaml --node coord --listen 127.0.0.1:8088 --db coord.db run
```
After ~10s, in a third shell confirm the coordinator aggregated agent1's samples:
```powershell
Invoke-RestMethod "http://127.0.0.1:8089/api/samples?since=0&limit=5"   # agent1 has its own samples
Invoke-RestMethod "http://127.0.0.1:8088/api/info"                       # coordinator identity
```
Expected: agent1 returns a non-empty sample array; both servers respond. (A direct count of `agent_samples` in `coord.db` can be confirmed by stopping the coordinator and re-reading the DB; the key proof is that the coordinator's pull loop ran without error and agent1 served samples.)

**Resilience check:** Ctrl+C Shell A (kill agent1) for ~10s — the coordinator's `PullOnce` errors are swallowed (agent marked offline). Restart Shell A; the coordinator resumes pulling from its stored cursor with no duplicates (guaranteed by `INSERT OR IGNORE` on `(agent_id, seq)`).

Stop both with Ctrl+C.

- [ ] **Step 6: Commit**

```powershell
git add -A
git commit -m @'
feat: config-driven agent targets + coordinator pull loop; CLI flags

Probe targets now come from the network config (peer ProbeHost).
A node with role=coordinator runs a pull loop aggregating every addressed
node via the resilient cursor-based sync. Adds --config/--node/--listen.

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
'@
git push
```

---

## Done criteria for M2a

- `go test ./...` passes (config resolve, idempotent Upsert/cursor, agent API, puller idempotency + liveness).
- Two local processes (coordinator + agent) run from one config; the coordinator aggregates the agent's samples over HTTP.
- Killing/restarting the agent does not duplicate rows and resumes from the cursor.
- Pushed to `origin/main`.

**Next (M2b):** readiness checks (reachable, rough clock offset, iperf3 presence, data-dir writable, role/targets) and the Agents + Config web views that surface agent liveness and config-readiness to the operator.
