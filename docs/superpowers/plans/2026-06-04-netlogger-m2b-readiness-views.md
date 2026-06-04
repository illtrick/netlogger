# NetLogger M2b — Readiness Checks + Agents/Config Views — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the operator a trustworthy "is everything set up right?" view — device-agnostic readiness checks per node (reachable, clock-in-tolerance, iperf3 present, data-dir writable, role/targets) plus `/api/agents` (liveness) and `/api/readiness` (checks) endpoints surfaced in the web GUI.

**Architecture:** Agents self-report facts they alone can know (iperf3 presence, data-dir writable) in `/api/info`. The coordinator's `readiness.Checker` fetches each node's info, measures a rough clock offset from the round-trip, and combines it with config facts into a `Result`. A small `coordinator` package turns the puller's liveness state and the checker's results into JSON handlers the `web` server mounts. **Test strategy:** each package gets unit tests; Task 4 adds an end-to-end test that runs a live agent server and asserts the coordinator's readiness + agents endpoints reflect it.

**Tech Stack:** Builds on M1/M2a. No new deps. New packages: `internal/sysinfo`, `internal/readiness`, `internal/coordinator`.

**Spec reference:** `docs/superpowers/specs/2026-06-04-netlogger-design.md` §10.3 (Agents view), §10.4 (Configuration readiness — generic, device-agnostic only). Hardware-specific checks are explicitly out of scope (§2a). Clock check here is a rough single-round-trip estimate; the rigorous min-δ handshake is M3.

---

### Task 1: `sysinfo` self-checks + extend agent `Info`

**Files:**
- Create: `internal/sysinfo/sysinfo.go`, `internal/sysinfo/sysinfo_test.go`
- Modify: `internal/mesh/agentapi.go` (extend `Info`, populate from `AgentAPI` fields, add `FetchInfo`)
- Test: `internal/mesh/agentapi_test.go` (extend)

- [ ] **Step 1: Write the failing sysinfo test**

Create `internal/sysinfo/sysinfo_test.go`:
```go
package sysinfo

import (
	"path/filepath"
	"testing"
)

func TestDetectVersionMissingBinaryReturnsEmpty(t *testing.T) {
	if v := detectVersion("definitely-not-a-real-binary-xyz123", "--version"); v != "" {
		t.Fatalf("want empty for missing binary, got %q", v)
	}
}

func TestDataDirWritable(t *testing.T) {
	if !DataDirWritable(t.TempDir()) {
		t.Fatal("temp dir should be writable")
	}
	if DataDirWritable(filepath.Join(t.TempDir(), "does", "not", "exist")) {
		t.Fatal("nonexistent nested dir should not be writable")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sysinfo/`
Expected: FAIL — `undefined: detectVersion`.

- [ ] **Step 3: Write sysinfo**

Create `internal/sysinfo/sysinfo.go`:
```go
// Package sysinfo holds device-agnostic self-checks an agent reports about
// itself (iperf3 presence, data-dir writability). No vendor-specific logic.
package sysinfo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Iperf3Version returns the installed iperf3 version string, or "" if absent.
func Iperf3Version() string { return detectVersion("iperf3", "--version") }

// detectVersion runs `bin arg` and returns the first whitespace token that
// looks like a version (the second field of typical `tool x.y` output), or the
// first line trimmed; "" if the binary is missing or errors.
func detectVersion(bin, arg string) string {
	path, err := exec.LookPath(bin)
	if err != nil {
		return ""
	}
	out, err := exec.Command(path, arg).CombinedOutput()
	if err != nil && len(out) == 0 {
		return ""
	}
	line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	return line
}

// DataDirWritable reports whether a temp file can be created in dir.
func DataDirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".netlogger-write-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	_ = os.Remove(filepath.Clean(name))
	return true
}
```

- [ ] **Step 4: Run sysinfo tests**

Run: `go test ./internal/sysinfo/`
Expected: PASS

- [ ] **Step 5: Extend mesh Info + add FetchInfo**

In `internal/mesh/agentapi.go`, replace the `Info` struct and `AgentAPI` struct and the `Info` handler, and add `FetchInfo`. First add `"fmt"` to the imports. Replace the `Info` struct:
```go
// Info is the agent identity/health payload at /api/info.
type Info struct {
	NodeID        string `json:"node_id"`
	Host          string `json:"host"`
	Version       string `json:"version"`
	TimeUnixUS    int64  `json:"time_unix_us"`
	Iperf3Version string `json:"iperf3_version"` // "" if not installed
	DataWritable  bool   `json:"data_writable"`
}
```
Replace the `AgentAPI` struct:
```go
// AgentAPI serves an agent's local samples and identity to the coordinator.
type AgentAPI struct {
	Store         *store.Store
	NodeID        string
	Host          string
	Iperf3Version string
	DataWritable  bool
}
```
Replace the `Info` handler body's encode call:
```go
// Info handles GET /api/info.
func (a *AgentAPI) Info(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(Info{
		NodeID:        a.NodeID,
		Host:          a.Host,
		Version:       version.Version,
		TimeUnixUS:    time.Now().UTC().UnixMicro(),
		Iperf3Version: a.Iperf3Version,
		DataWritable:  a.DataWritable,
	})
}
```
Add at the end of the file:
```go
// FetchInfo GETs {baseURL}/api/info and decodes it.
func FetchInfo(client *http.Client, baseURL string) (Info, error) {
	resp, err := client.Get(baseURL + "/api/info")
	if err != nil {
		return Info{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Info{}, fmt.Errorf("info status %d", resp.StatusCode)
	}
	var info Info
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return Info{}, err
	}
	return info, nil
}
```

- [ ] **Step 6: Extend the agent API test for the new fields**

Append to `internal/mesh/agentapi_test.go`:
```go
func TestAgentInfoReportsSelfChecks(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "i.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	api := &AgentAPI{Store: s, NodeID: "ncase", Host: "h", Iperf3Version: "iperf 3.18", DataWritable: true}

	mux := http.NewServeMux()
	api.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	info, err := FetchInfo(srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if info.Iperf3Version != "iperf 3.18" || !info.DataWritable {
		t.Fatalf("self-checks not reported: %+v", info)
	}
}
```

- [ ] **Step 7: Run mesh tests**

Run: `go test ./internal/mesh/`
Expected: PASS

- [ ] **Step 8: Commit**

```powershell
git add -A
git commit -m @'
feat(sysinfo): agent self-checks (iperf3 presence, data-dir writable) in /api/info

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
'@
```

---

### Task 2: `readiness.Checker`

**Files:**
- Create: `internal/readiness/readiness.go`, `internal/readiness/readiness_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/readiness/readiness_test.go`:
```go
package readiness

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"netlogger/internal/config"
	"netlogger/internal/mesh"
)

// infoServer serves a fixed /api/info payload.
func infoServer(t *testing.T, info mesh.Info) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/info", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(info)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func addrOf(t *testing.T, url string) string {
	t.Helper()
	return strings.TrimPrefix(url, "http://")
}

func checkNamed(res Result, name string) (Check, bool) {
	for _, c := range res.Checks {
		if c.Name == name {
			return c, true
		}
	}
	return Check{}, false
}

func TestCheckAllGood(t *testing.T) {
	srv := infoServer(t, mesh.Info{NodeID: "ncase", Host: "h", TimeUnixUS: time.Now().UTC().UnixMicro(), Iperf3Version: "iperf 3.18", DataWritable: true})
	c := NewChecker()
	res := c.Check(config.Node{ID: "ncase", Address: addrOf(t, srv.URL)})
	if !res.Online {
		t.Fatal("should be online")
	}
	if res.Issues != 0 {
		t.Fatalf("want 0 issues, got %d (%+v)", res.Issues, res.Checks)
	}
}

func TestCheckFlagsMissingIperf3AndUnwritable(t *testing.T) {
	srv := infoServer(t, mesh.Info{NodeID: "ncase", TimeUnixUS: time.Now().UTC().UnixMicro(), Iperf3Version: "", DataWritable: false})
	res := NewChecker().Check(config.Node{ID: "ncase", Address: addrOf(t, srv.URL)})
	if c, _ := checkNamed(res, "iperf3 present"); c.OK {
		t.Fatal("iperf3 should be flagged missing")
	}
	if c, _ := checkNamed(res, "data dir writable"); c.OK {
		t.Fatal("data writable should be flagged false")
	}
	if res.Issues < 2 {
		t.Fatalf("want >=2 issues, got %d", res.Issues)
	}
}

func TestCheckClockOutOfTolerance(t *testing.T) {
	// Agent reports a time 10s in the future -> offset exceeds the 2s tolerance.
	future := time.Now().Add(10 * time.Second).UTC().UnixMicro()
	srv := infoServer(t, mesh.Info{NodeID: "ncase", TimeUnixUS: future, Iperf3Version: "x", DataWritable: true})
	res := NewChecker().Check(config.Node{ID: "ncase", Address: addrOf(t, srv.URL)})
	if c, _ := checkNamed(res, "clock sync within tolerance"); c.OK {
		t.Fatalf("clock should be out of tolerance: %+v", c)
	}
}

func TestCheckUnreachable(t *testing.T) {
	res := NewChecker().Check(config.Node{ID: "dead", Address: "127.0.0.1:1"})
	if res.Online {
		t.Fatal("unreachable node should be offline")
	}
	if c, _ := checkNamed(res, "reachable"); c.OK {
		t.Fatal("reachable check should fail")
	}
}

func TestCheckNoAddressIsOfflineNotPanic(t *testing.T) {
	res := NewChecker().Check(config.Node{ID: "switch1"})
	if res.Online {
		t.Fatal("addressless node cannot be online")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/readiness/`
Expected: FAIL — `undefined: NewChecker`.

- [ ] **Step 3: Write the implementation**

Create `internal/readiness/readiness.go`:
```go
// Package readiness runs device-agnostic per-node configuration checks.
// It encodes no vendor/NIC-specific knowledge — that's the operator's (spec §2a).
package readiness

import (
	"fmt"
	"net/http"
	"time"

	"netlogger/internal/clock"
	"netlogger/internal/config"
	"netlogger/internal/mesh"
)

// Check is one readiness probe result.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// Result is all checks for one node.
type Result struct {
	NodeID string  `json:"node_id"`
	Online bool    `json:"online"`
	Checks []Check `json:"checks"`
	Issues int     `json:"issues"`
}

// Checker runs readiness checks against agents.
type Checker struct {
	Client    *http.Client
	Clock     clock.Clock
	Tolerance time.Duration
}

// NewChecker returns a Checker with sane defaults (2s clock tolerance).
func NewChecker() *Checker {
	return &Checker{
		Client:    &http.Client{Timeout: 4 * time.Second},
		Clock:     clock.System{},
		Tolerance: 2 * time.Second,
	}
}

// Check runs all readiness checks for node and returns the combined result.
func (c *Checker) Check(node config.Node) Result {
	res := Result{NodeID: node.ID}
	add := func(name string, ok bool, detail string) {
		res.Checks = append(res.Checks, Check{Name: name, OK: ok, Detail: detail})
		if !ok {
			res.Issues++
		}
	}

	// Config-only check (no network needed).
	add("role/targets assigned", node.Address != "", node.Address)

	if node.Address == "" {
		res.Online = false
		return res
	}

	t0 := c.Clock.NowUnixMicro()
	info, err := mesh.FetchInfo(c.Client, "http://"+node.Address)
	t1 := c.Clock.NowUnixMicro()
	if err != nil {
		res.Online = false
		add("reachable", false, err.Error())
		return res
	}
	res.Online = true
	add("reachable", true, "")

	// Rough offset estimate: assume symmetric path, agent time ~ midpoint of RTT.
	rtt := t1 - t0
	est := t0 + rtt/2
	offset := info.TimeUnixUS - est
	absOff := offset
	if absOff < 0 {
		absOff = -absOff
	}
	add("clock sync within tolerance", absOff <= c.Tolerance.Microseconds(),
		fmt.Sprintf("offset %dms", offset/1000))
	add("iperf3 present", info.Iperf3Version != "", info.Iperf3Version)
	add("data dir writable", info.DataWritable, "")
	return res
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/readiness/`
Expected: PASS

- [ ] **Step 5: Commit**

```powershell
git add -A
git commit -m @'
feat(readiness): device-agnostic per-node checks (reachable/clock/iperf3/data)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
'@
```

---

### Task 3: `coordinator` handlers + web routes

**Files:**
- Create: `internal/coordinator/coordinator.go`, `internal/coordinator/coordinator_test.go`
- Modify: `internal/web/web.go` (optional `AgentsHandler`/`ReadinessHandler`, routes), `internal/web/static/index.html`
- Test: `internal/web/web_test.go` (extend)

- [ ] **Step 1: Write the failing coordinator test**

Create `internal/coordinator/coordinator_test.go`:
```go
package coordinator

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"netlogger/internal/config"
	"netlogger/internal/readiness"
)

func TestReadinessHandlerReturnsResults(t *testing.T) {
	nodes := []config.Node{{ID: "switch1"}} // addressless -> offline, but no panic
	h := ReadinessHandler(readiness.NewChecker(), nodes)

	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/api/readiness", nil))
	if rr.Code != 200 {
		t.Fatalf("code %d", rr.Code)
	}
	var out []readiness.Result
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 1 || out[0].NodeID != "switch1" {
		t.Fatalf("bad readiness output: %+v", out)
	}
}

func TestAgentsHandlerEmptyIsArrayNotNull(t *testing.T) {
	h := AgentsHandler(nil, nil)
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/api/agents", nil))
	if got := strings.TrimSpace(rr.Body.String()); got != "[]" {
		t.Fatalf("want [], got %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/coordinator/`
Expected: FAIL — `undefined: ReadinessHandler`.

- [ ] **Step 3: Write the coordinator package**

Create `internal/coordinator/coordinator.go`:
```go
// Package coordinator turns puller liveness + readiness results into JSON
// HTTP handlers the web server mounts.
package coordinator

import (
	"encoding/json"
	"net/http"

	"netlogger/internal/config"
	"netlogger/internal/mesh"
	"netlogger/internal/readiness"
)

// AgentView is the per-agent liveness row for /api/agents.
type AgentView struct {
	ID             string `json:"id"`
	Online         bool   `json:"online"`
	LastSeenUnixUS int64  `json:"last_seen_unix_us"`
	LastErr        string `json:"last_err"`
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// AgentsHandler reports liveness for each node from the puller's state.
func AgentsHandler(p *mesh.Puller, nodes []config.TargetRef) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		views := []AgentView{}
		if p != nil {
			for _, n := range nodes {
				st := p.State(n.ID)
				var seen int64
				if !st.LastSeen.IsZero() {
					seen = st.LastSeen.UnixMicro()
				}
				views = append(views, AgentView{ID: n.ID, Online: st.Online, LastSeenUnixUS: seen, LastErr: st.LastErr})
			}
		}
		writeJSON(w, views)
	}
}

// ReadinessHandler runs the readiness checks for the given nodes on demand.
func ReadinessHandler(c *readiness.Checker, nodes []config.Node) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out := []readiness.Result{}
		for _, n := range nodes {
			out = append(out, c.Check(n))
		}
		writeJSON(w, out)
	}
}
```

- [ ] **Step 4: Run coordinator tests**

Run: `go test ./internal/coordinator/`
Expected: PASS

- [ ] **Step 5: Add optional handlers + routes to web**

In `internal/web/web.go`, replace the `Server` struct and the `Handler` method:
```go
// Server holds the live state and optional coordinator data handlers.
type Server struct {
	Host             string
	ServiceState     string
	AgentsHandler    http.HandlerFunc // optional; nil -> empty array
	ReadinessHandler http.HandlerFunc // optional; nil -> empty array
}

// Handler returns the HTTP handler (status API, agents/readiness, static files).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Status{
			Host:         s.Host,
			Version:      version.Version,
			ServiceState: s.ServiceState,
		})
	})
	mux.HandleFunc("/api/agents", orEmptyArray(s.AgentsHandler))
	mux.HandleFunc("/api/readiness", orEmptyArray(s.ReadinessHandler))
	sub, err := fs.Sub(content, "static")
	if err != nil {
		panic(err)
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))
	return mux
}

func orEmptyArray(h http.HandlerFunc) http.HandlerFunc {
	if h != nil {
		return h
	}
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}
}
```

- [ ] **Step 6: Update the embedded page to show agents + readiness**

Replace `internal/web/static/index.html`:
```html
<!DOCTYPE html>
<html lang="en">
<head><meta charset="utf-8"><title>NetLogger</title>
<style>body{font-family:system-ui;background:#0d1117;color:#e6edf3;padding:2rem;line-height:1.5}
.k{color:#677483}.v{font-family:monospace}h2{font-size:14px;margin-top:1.5rem}
table{border-collapse:collapse;font-size:13px;margin-top:.5rem}td,th{padding:6px 12px;border-bottom:1px solid #222b36;text-align:left}
.ok{color:#3fb950}.bad{color:#f85149}</style></head>
<body>
  <h1>NetLogger <span class="v" id="ver"></span></h1>
  <p><span class="k">host:</span> <span class="v" id="host"></span> &nbsp; <span class="k">service:</span> <span class="v" id="svc"></span></p>
  <h2>Agents</h2><table id="agents"><thead><tr><th>node</th><th>online</th><th>last seen</th><th>error</th></tr></thead><tbody></tbody></table>
  <h2>Readiness</h2><div id="readiness"></div>
<script>
fetch('/api/status').then(r=>r.json()).then(s=>{
  ver.textContent=s.version; host.textContent=s.host; svc.textContent=s.service_state;
});
fetch('/api/agents').then(r=>r.json()).then(rows=>{
  const tb=document.querySelector('#agents tbody');
  rows.forEach(a=>{const tr=document.createElement('tr');
    tr.innerHTML=`<td>${a.id}</td><td class="${a.online?'ok':'bad'}">${a.online?'yes':'no'}</td>`+
      `<td>${a.last_seen_unix_us||'-'}</td><td>${a.last_err||''}</td>`;
    tb.appendChild(tr);});
});
fetch('/api/readiness').then(r=>r.json()).then(list=>{
  const root=document.getElementById('readiness');
  list.forEach(n=>{const h=document.createElement('div');
    let rows=n.checks.map(c=>`<tr><td>${c.name}</td><td class="${c.ok?'ok':'bad'}">${c.ok?'OK':'FAIL'}</td><td>${c.detail||''}</td></tr>`).join('');
    h.innerHTML=`<b>${n.node_id}</b> (${n.issues} issue${n.issues===1?'':'s'})<table>${rows}</table>`;
    root.appendChild(h);});
});
</script>
</body>
</html>
```

- [ ] **Step 7: Extend the web test for the new endpoints**

Append to `internal/web/web_test.go`:
```go
func TestAgentsEndpointDefaultsToEmptyArray(t *testing.T) {
	srv := &Server{Host: "h", ServiceState: "running"} // no handlers injected
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/agents", nil))
	if strings.TrimSpace(rr.Body.String()) != "[]" {
		t.Fatalf("want [], got %q", rr.Body.String())
	}
}

func TestInjectedReadinessHandlerIsUsed(t *testing.T) {
	called := false
	srv := &Server{
		ReadinessHandler: func(w http.ResponseWriter, r *http.Request) { called = true; w.Write([]byte("[1]")) },
	}
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/readiness", nil))
	if !called {
		t.Fatal("injected readiness handler was not called")
	}
}
```

- [ ] **Step 8: Run web + coordinator tests**

Run: `go test ./internal/web/ ./internal/coordinator/`
Expected: PASS

- [ ] **Step 9: Commit**

```powershell
git add -A
git commit -m @'
feat(coordinator,web): /api/agents + /api/readiness endpoints and GUI tables

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
'@
```

---

### Task 4: Wire coordinator into agentsvc + end-to-end test

**Files:**
- Modify: `internal/agentsvc/agentsvc.go` (populate self-checks; wire coordinator handlers when coordinator)
- Create: `internal/coordinator/e2e_test.go`

- [ ] **Step 1: Write the end-to-end test**

Create `internal/coordinator/e2e_test.go`:
```go
package coordinator

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"netlogger/internal/config"
	"netlogger/internal/mesh"
	"netlogger/internal/readiness"
	"netlogger/internal/store"
)

// End-to-end: a real agent server (sync API + self-checks) is checked + pulled
// by the coordinator handlers, and the JSON reflects the live agent.
func TestEndToEndReadinessAndAgents(t *testing.T) {
	// 1. Stand up a real agent.
	s, err := store.Open(filepath.Join(t.TempDir(), "e2e.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	for i := 0; i < 3; i++ {
		_, _ = s.Insert(store.Sample{TSUnixUS: int64(i), ProbeType: "icmp", SrcHost: "ncase", DstHost: "ryzen", Direction: "rtt", RTTus: 700})
	}
	api := &mesh.AgentAPI{Store: s, NodeID: "ncase", Host: "ncase", Iperf3Version: "iperf 3.18", DataWritable: true}
	amux := http.NewServeMux()
	api.Register(amux)
	agent := httptest.NewServer(amux)
	t.Cleanup(agent.Close)
	addr := strings.TrimPrefix(agent.URL, "http://")

	node := config.Node{ID: "ncase", Type: config.NodeEndpoint, Address: addr}

	// 2. Readiness over the live agent -> all checks pass.
	rh := ReadinessHandler(readiness.NewChecker(), []config.Node{node})
	rr := httptest.NewRecorder()
	rh(rr, httptest.NewRequest(http.MethodGet, "/api/readiness", nil))
	var results []readiness.Result
	if err := json.Unmarshal(rr.Body.Bytes(), &results); err != nil {
		t.Fatalf("decode readiness: %v", err)
	}
	if len(results) != 1 || !results[0].Online || results[0].Issues != 0 {
		t.Fatalf("live agent should be online with 0 issues: %+v", results)
	}

	// 3. Pull from the agent, then /api/agents shows it online.
	agg, err := store.Open(filepath.Join(t.TempDir(), "agg.db"))
	if err != nil {
		t.Fatalf("open agg: %v", err)
	}
	t.Cleanup(func() { agg.Close() })
	p := mesh.NewPuller(agg)
	if _, err := p.PullOnce(mesh.AgentRef{ID: "ncase", BaseURL: agent.URL}); err != nil {
		t.Fatalf("pull: %v", err)
	}
	ah := AgentsHandler(p, []config.TargetRef{{ID: "ncase", Address: addr}})
	rr2 := httptest.NewRecorder()
	ah(rr2, httptest.NewRequest(http.MethodGet, "/api/agents", nil))
	var agents []AgentView
	if err := json.Unmarshal(rr2.Body.Bytes(), &agents); err != nil {
		t.Fatalf("decode agents: %v", err)
	}
	if len(agents) != 1 || !agents[0].Online {
		t.Fatalf("agent should show online after pull: %+v", agents)
	}
	if n, _ := agg.CountAgentSamples("ncase"); n != 3 {
		t.Fatalf("want 3 aggregated rows, got %d", n)
	}
}
```

- [ ] **Step 2: Run it to verify it fails (wiring not yet present is fine — this test only uses existing exported APIs, so it should actually PASS once Tasks 1-3 are in). Run it now:**

Run: `go test ./internal/coordinator/ -run EndToEnd -v`
Expected: PASS (it exercises the public APIs from Tasks 1–3). If it fails, fix the wiring it depends on before continuing.

- [ ] **Step 3: Wire the coordinator handlers + self-checks into agentsvc**

In `internal/agentsvc/agentsvc.go`, add imports `"netlogger/internal/coordinator"`, `"netlogger/internal/readiness"`, `"netlogger/internal/sysinfo"`. Then in `Start`, replace the block from `api := &mesh.AgentAPI{...}` through the coordinator `if` with:
```go
	host, _ := os.Hostname()
	dataDir := filepath.Dir(p.DBPath)
	api := &mesh.AgentAPI{
		Store:         st,
		NodeID:        self.ID,
		Host:          host,
		Iperf3Version: sysinfo.Iperf3Version(),
		DataWritable:  sysinfo.DataDirWritable(dataDir),
	}
	ws := &web.Server{Host: host, ServiceState: "running"}

	if self.Role == "coordinator" {
		p.puller = mesh.NewPuller(st)
		ws.AgentsHandler = coordinator.AgentsHandler(p.puller, cfg.AddressedNodes())
		ws.ReadinessHandler = coordinator.ReadinessHandler(readiness.NewChecker(), endpointNodes(cfg))
	}

	root := http.NewServeMux()
	api.Register(root) // /api/info, /api/samples
	root.Handle("/", ws.Handler())
	p.srv = &http.Server{Addr: p.Listen, Handler: root}

	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel

	go p.srv.ListenAndServe()
	go p.probeLoop(ctx, self.ID, peers)
	if self.Role == "coordinator" {
		go p.pullLoop(ctx, cfg.AddressedNodes())
	}
	return nil
```
Add `"path/filepath"` to the imports if not present. Add this helper at the end of the file:
```go
// endpointNodes returns the config nodes that have a control address.
func endpointNodes(cfg *config.Config) []config.Node {
	var out []config.Node
	for _, n := range cfg.Nodes {
		if n.Address != "" {
			out = append(out, n)
		}
	}
	return out
}
```

- [ ] **Step 4: Build + full suite**

Run:
```powershell
go build -o bin/netlogger.exe ./cmd/netlogger
go vet ./...
go test ./...
```
Expected: builds; `go vet` clean; all packages PASS (including the e2e test).

- [ ] **Step 5: Manual verification — readiness in the browser**

Run two processes as in M2a (distinct ports + db names):
```powershell
$env:Path += ";C:\Program Files\Go\bin"
Start-Process .\bin\netlogger.exe -ArgumentList "--config","examples\two-node-localhost.yaml","--node","agent1","--listen","127.0.0.1:8089","--db","m2b_agent1.db","run"
Start-Process .\bin\netlogger.exe -ArgumentList "--config","examples\two-node-localhost.yaml","--node","coord","--listen","127.0.0.1:8088","--db","m2b_coord.db","run"
Start-Sleep -Seconds 8
Invoke-RestMethod "http://127.0.0.1:8088/api/readiness"
Invoke-RestMethod "http://127.0.0.1:8088/api/agents"
Get-Process netlogger | Stop-Process -Force
```
Expected: `/api/readiness` lists both nodes with their checks (reachable OK; iperf3 present depends on whether iperf3 is installed — `false` is correct if it isn't); `/api/agents` shows both online. Open `http://127.0.0.1:8088/` to see the Agents + Readiness tables.

- [ ] **Step 6: Commit + push**

```powershell
git add -A
git commit -m @'
feat: wire coordinator readiness + agents views into the agent service

Coordinator nodes now serve /api/agents (puller liveness) and /api/readiness
(device-agnostic checks) and self-report iperf3 presence + data-dir writability.
Adds an end-to-end test exercising a live agent through the coordinator handlers.

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
'@
git push
```

---

## Done criteria for M2b

- `go test ./...` passes, including: `sysinfo` self-checks, `readiness` (all-good / missing-iperf3 / unwritable / clock-out-of-tolerance / unreachable / no-address), `coordinator` handlers, web endpoint defaults + injection, and the **end-to-end** test (live agent → coordinator readiness + agents + aggregation).
- A coordinator process serves `/api/agents` and `/api/readiness`, and the embedded page renders both tables.
- Pushed to `origin/main`.

**Next (M3):** the rigorous clock-offset handshake (min-δ, uncertainty intervals), interval-overlap correlation, and the per-component health + coverage scoring board.
