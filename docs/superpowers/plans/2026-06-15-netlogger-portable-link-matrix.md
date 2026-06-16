# NetLogger Portable — Link Matrix (live link-stat exchange + N×N grid) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Make every window show the **whole mesh**, not just its own outbound links. Each instance serves its current per-link stats over the control port (`/api/links`) and pulls every peer's, assembling an **N×N Link Matrix** (rows = source, columns = destination) rendered as a color-coded Gio grid. This is the diagnostic centerpiece: a bad link is one hot cell; a bad shared device is a hot row/column. (Spec §5–6.)

**Architecture:** A lightweight **link-stat gossip** layer in `appcore`: a `/api/links` HTTP handler returns this node's `LinkReport` (built from the existing in-memory UDP per-peer stats), wrapped in the kept `httpauth` middleware and served on the control port (`8088`). A pull loop fetches each discovered peer's `/api/links`. A pure `assembleMatrix` combines this node's report + all peers' reports into a `Matrix` exposed on `Snapshot`. A new Gio matrix view renders it. No raw-sample aggregation, no store queries — just current-state gossip, which is what a live matrix needs.

**Tech Stack:** Go (cgo-free), `net/http` + kept `internal/httpauth`, existing `appcore` UDP stats, Gio (custom grid drawing).

Reference spec: `docs/superpowers/specs/2026-06-15-netlogger-portable-design.md` §5 (Link Matrix, CVD-safe color §5.1), §6 (screens).

Severity bands (spec §5.1, Wong CVD-safe, colored by UDP loss %): Good `#009E73` (<0.1%), Warn `#E69F00` (0.1–1%), Bad `#D55E00` (≥1%), No-data `#999999`. Diagonal (A→A) is hatched/grey.

---

## File Structure

| Path | Responsibility |
|---|---|
| `internal/appcore/links.go` | `LinkStat`/`LinkReport`/`Matrix` types, `linkReport()` builder, `assembleMatrix()` (pure), the `/api/links` handler, and the `fetchLinks` client. |
| `internal/appcore/links_test.go` | Tests for `assembleMatrix`, the handler, and `fetchLinks` (httptest). |
| `internal/appcore/appcore.go` | Modify: serve `/api/links` on the control port, run a link-pull loop, expose `Snapshot.Matrix`. |
| `internal/appcore/appcore_test.go` | Add: injected-peer-report test that `Snapshot.Matrix` assembles all directed links. |
| `internal/ui/matrix.go` | Gio rendering of the color-coded N×N matrix + a severity legend. |
| `internal/ui/matrix_test.go` | Tests for the pure helpers (`sevColor`, cell formatting). |
| `internal/ui/ui.go` | Modify: render the matrix below the status panel (or as the primary view). |

---

## Task 1: Link types + report builder + matrix assembly (pure)

**Files:** Create `internal/appcore/links.go`, `internal/appcore/links_test.go`.

**Context:** This node already keeps per-peer UDP stats in `a.udpStats map[string]*udpStat` (each `udpStat.read()` → `rttms, jitterms, lossPct float64, episodes int`) and per-peer ICMP stats in `a.peerStats`. `a.nodeID`/`a.host` identify this node. The matrix is assembled purely from each node's `LinkReport`.

- [ ] **Step 1: Write the failing test `internal/appcore/links_test.go`**

```go
package appcore

import "testing"

func TestAssembleMatrixCombinesAllReports(t *testing.T) {
	own := LinkReport{NodeID: "a", Host: "hostA", Links: []LinkStat{
		{PeerID: "b", RTTms: 1.0, JitterMs: 0.2, LossPct: 0, Drops: 0},
	}}
	peer := LinkReport{NodeID: "b", Host: "hostB", Links: []LinkStat{
		{PeerID: "a", RTTms: 1.1, JitterMs: 0.3, LossPct: 2.0, Drops: 5},
	}}
	m := assembleMatrix(own, map[string]LinkReport{"b": peer})

	if len(m.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d: %+v", len(m.Nodes), m.Nodes)
	}
	ab, ok := m.Cell("a", "b")
	if !ok || ab.LossPct != 0 || ab.RTTms != 1.0 {
		t.Fatalf("a->b cell wrong: %+v ok=%v", ab, ok)
	}
	ba, ok := m.Cell("b", "a")
	if !ok || ba.LossPct != 2.0 || ba.Drops != 5 {
		t.Fatalf("b->a cell wrong: %+v ok=%v", ba, ok)
	}
	if _, ok := m.Cell("a", "a"); ok {
		t.Fatalf("diagonal a->a should have no cell")
	}
}

func TestAssembleMatrixNodesSortedByHost(t *testing.T) {
	own := LinkReport{NodeID: "z", Host: "zebra"}
	peers := map[string]LinkReport{
		"m": {NodeID: "m", Host: "alpha"},
		"n": {NodeID: "n", Host: "mike"},
	}
	m := assembleMatrix(own, peers)
	if m.Nodes[0].Host != "alpha" || m.Nodes[1].Host != "mike" || m.Nodes[2].Host != "zebra" {
		t.Fatalf("nodes not sorted by host: %+v", m.Nodes)
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/appcore/ -run TestAssembleMatrix -v` → FAIL (undefined types).

- [ ] **Step 3: Implement `internal/appcore/links.go`**

```go
package appcore

import "sort"

// LinkStat is one directed link's current quality, as measured by the source node.
type LinkStat struct {
	PeerID   string  `json:"peer_id"`
	RTTms    float64 `json:"rtt_ms"`
	JitterMs float64 `json:"jitter_ms"`
	LossPct  float64 `json:"loss_pct"`
	Drops    int     `json:"drops"`
}

// LinkReport is a node's view of its own outbound links (what it serves at /api/links).
type LinkReport struct {
	NodeID string     `json:"node_id"`
	Host   string     `json:"host"`
	Links  []LinkStat `json:"links"`
}

// MatrixNode is one node (a row and a column of the matrix).
type MatrixNode struct {
	ID   string
	Host string
}

// MatrixCell is one directed link src->dst in the matrix.
type MatrixCell struct {
	SrcID    string
	DstID    string
	RTTms    float64
	JitterMs float64
	LossPct  float64
	Drops    int
}

// Matrix is the assembled N×N view of all directed links.
type Matrix struct {
	Nodes []MatrixNode
	cells map[string]MatrixCell // key src+"\x00"+dst
}

func cellKey(src, dst string) string { return src + "\x00" + dst }

// Cell returns the directed link src->dst, if measured.
func (m Matrix) Cell(src, dst string) (MatrixCell, bool) {
	c, ok := m.cells[cellKey(src, dst)]
	return c, ok
}

// assembleMatrix combines this node's report with every peer's report into the
// full directed-link matrix. Nodes are sorted by host (then id) for a stable,
// glanceable layout.
func assembleMatrix(own LinkReport, peers map[string]LinkReport) Matrix {
	nodes := map[string]MatrixNode{own.NodeID: {ID: own.NodeID, Host: own.Host}}
	cells := make(map[string]MatrixCell)
	add := func(r LinkReport) {
		if r.NodeID == "" {
			return
		}
		nodes[r.NodeID] = MatrixNode{ID: r.NodeID, Host: r.Host}
		for _, l := range r.Links {
			if l.PeerID == "" || l.PeerID == r.NodeID {
				continue
			}
			cells[cellKey(r.NodeID, l.PeerID)] = MatrixCell{
				SrcID: r.NodeID, DstID: l.PeerID,
				RTTms: l.RTTms, JitterMs: l.JitterMs, LossPct: l.LossPct, Drops: l.Drops,
			}
		}
	}
	add(own)
	for _, r := range peers {
		add(r)
	}
	out := make([]MatrixNode, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Host != out[j].Host {
			return out[i].Host < out[j].Host
		}
		return out[i].ID < out[j].ID
	})
	return Matrix{Nodes: out, cells: cells}
}
```

- [ ] **Step 4: Run to verify pass** — `go test ./internal/appcore/ -run TestAssembleMatrix -v` → PASS. `gofmt -w internal/appcore/ && go vet ./internal/appcore/`.

- [ ] **Step 5: Commit** — `git add internal/appcore/links.go internal/appcore/links_test.go && git commit -m "feat(appcore): link-stat types + pure matrix assembly (link-matrix)"` (footer).

---

## Task 2: `/api/links` handler + `fetchLinks` client

**Files:** Modify `internal/appcore/links.go`, add tests to `internal/appcore/links_test.go`.

- [ ] **Step 1: Write the failing tests (append to `internal/appcore/links_test.go`)**

```go
import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLinksHandlerServesReport(t *testing.T) {
	rep := LinkReport{NodeID: "a", Host: "hostA", Links: []LinkStat{{PeerID: "b", RTTms: 1}}}
	srv := httptest.NewServer(linksHandler(func() LinkReport { return rep }))
	defer srv.Close()
	got, err := fetchLinks(http.DefaultClient, srv.URL)
	if err != nil {
		t.Fatalf("fetchLinks: %v", err)
	}
	if got.NodeID != "a" || len(got.Links) != 1 || got.Links[0].PeerID != "b" {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
}

func TestFetchLinksErrorsOnBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()
	if _, err := fetchLinks(http.DefaultClient, srv.URL); err == nil {
		t.Fatalf("expected error on 500")
	}
}
```

(Merge the new `import` block into the existing one at the top of `links_test.go`.)

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/appcore/ -run 'TestLinksHandler|TestFetchLinks' -v` → FAIL.

- [ ] **Step 3: Add to `internal/appcore/links.go`**

```go
import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
)
```
(Replace the existing single `import "sort"` with this block.)

```go
// linksHandler serves this node's current LinkReport as JSON. report is a
// callback so the handler always reflects live stats.
func linksHandler(report func() LinkReport) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(report())
	}
}

// fetchLinks GETs a peer's /api/links and decodes the LinkReport.
func fetchLinks(client *http.Client, baseURL string) (LinkReport, error) {
	resp, err := client.Get(baseURL + "/api/links")
	if err != nil {
		return LinkReport{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return LinkReport{}, fmt.Errorf("links: status %d", resp.StatusCode)
	}
	var rep LinkReport
	if err := json.NewDecoder(resp.Body).Decode(&rep); err != nil {
		return LinkReport{}, fmt.Errorf("links decode: %w", err)
	}
	return rep, nil
}
```

Note: the test calls `linksHandler(...)` whose URL is the httptest base (no `/api/links` suffix on the handler itself — `fetchLinks` appends `/api/links`, but httptest serves the handler at `/`). To make the test's `fetchLinks(srv.URL)` reach the handler, the handler must respond on `/api/links`. Two clean options — pick the second:
  - (a) In `TestLinksHandlerServesReport`, wrap with a mux: `mux := http.NewServeMux(); mux.Handle("/api/links", linksHandler(...))`.
  Update Step 1's test to:
```go
	mux := http.NewServeMux()
	mux.Handle("/api/links", linksHandler(func() LinkReport { return rep }))
	srv := httptest.NewServer(mux)
```

- [ ] **Step 4: Run to verify pass** — `go test ./internal/appcore/ -run 'TestLinksHandler|TestFetchLinks' -v` → PASS. `gofmt -w`, `go vet`.

- [ ] **Step 5: Commit** — `git add internal/appcore/ && git commit -m "feat(appcore): /api/links handler + fetchLinks client (link-matrix)"`.

---

## Task 3: Serve links, pull peers, expose `Snapshot.Matrix`

**Files:** Modify `internal/appcore/appcore.go`, add a test to `internal/appcore/appcore_test.go`.

- [ ] **Step 1: Write the failing test (append to `internal/appcore/appcore_test.go`)**

```go
func TestSnapshotAssemblesMatrixFromPeerReports(t *testing.T) {
	dir := t.TempDir()
	a := New(dir)
	a.Ping = func(string, time.Duration) (probe.Result, error) { return probe.Result{RTT: time.Millisecond}, nil }
	a.StartIperf = func(string) (func(), string) { return func() {}, "" }
	a.ProbeUDP = func(string, int, time.Duration, time.Duration) (probe.UDPStats, error) {
		return probe.UDPStats{Sent: 200, Received: 200, AvgRTT: time.Millisecond, Jitter: 200 * time.Microsecond}, nil
	}
	a.Discovery = fakeLister{peers: []discovery.Peer{{ID: "b", Host: "hostB", Addr: "10.0.0.2:8088"}}}
	// Inject a peer's link report directly (skip real HTTP).
	a.FetchLinks = func(addr string) (LinkReport, error) {
		return LinkReport{NodeID: "b", Host: "hostB", Links: []LinkStat{{PeerID: a.NodeID(), RTTms: 1.2, LossPct: 3.0, Drops: 4}}}, nil
	}
	a.tick = 5 * time.Millisecond
	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()
	deadline := time.Now().Add(2 * time.Second)
	for {
		m := a.Snapshot().Matrix
		if c, ok := m.Cell("b", a.NodeID()); ok && c.LossPct == 3.0 {
			if len(m.Nodes) < 2 {
				t.Fatalf("expected >=2 nodes, got %d", len(m.Nodes))
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("matrix not assembled; got %+v", a.Snapshot().Matrix)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/appcore/ -run TestSnapshotAssemblesMatrix -v` → FAIL (no NodeID()/FetchLinks/Matrix).

- [ ] **Step 3: Modify `internal/appcore/appcore.go`**

3a. Add imports: `"context"` (already present), `"net/http"`, `"time"` (present). Also `"netlogger/internal/httpauth"`.

3b. Add a `NodeID()` accessor (the test and matrix need the self id):
```go
// NodeID returns this node's stable id (valid after Start).
func (a *App) NodeID() string { a.mu.Lock(); defer a.mu.Unlock(); return a.nodeID }
```

3c. Add fields to `App`:
```go
	// FetchLinks fetches a peer's link report; defaults to an HTTP client call.
	FetchLinks  func(baseURL string) (LinkReport, error)
	linksSrv    *http.Server
	reportMu    sync.Mutex
	peerReports map[string]LinkReport
```

3d. In `New`, init the map + default `FetchLinks`:
```go
		peerReports: make(map[string]LinkReport),
		FetchLinks: func(baseURL string) (LinkReport, error) {
			return fetchLinks(&http.Client{Timeout: 3 * time.Second}, baseURL)
		},
```

3e. Add `linkReport()` — this node's current outbound links from the UDP stats:
```go
// linkReport builds this node's current outbound link report from its UDP stats.
func (a *App) linkReport() LinkReport {
	a.mu.Lock()
	id, host := a.nodeID, a.host
	a.mu.Unlock()
	var links []LinkStat
	if a.Discovery != nil {
		for _, p := range a.Discovery.Peers() {
			rtt, jitter, loss, eps := a.udpStatFor(p.ID).read()
			links = append(links, LinkStat{PeerID: p.ID, RTTms: rtt, JitterMs: jitter, LossPct: loss, Drops: eps})
		}
	}
	return LinkReport{NodeID: id, Host: host, Links: links}
}
```

3f. In `Start` (real path, not the injected-Discovery test path is fine too — the server bind is harmless in tests but to avoid port clashes only start it when not already injected... simplest: always start it; tests use t.TempDir and the same fixed port 8088 which could clash across parallel tests — so guard it): start the links HTTP server only when `a.linksSrv == nil` AND we created real discovery. Concretely, inside the existing `if a.Discovery == nil { ... real discovery ... }` block is the "real" path; but the server should run whenever the app runs for real. To keep tests from binding 8088, gate the server on a new unexported flag set on the real path. Simplest robust approach: start the server in a goroutine and ignore bind errors (a test that doesn't need it won't assert on it), but DO skip it when `a.FetchLinks` is overridden in a test. Use this rule — start the server unless a test injected `Discovery` (the same gate already used for real discovery):

Add, right after the real-discovery `svc.Start()` success branch (or just after the discovery `if` block), guarded by whether we created real discovery:
```go
	if a.disc != nil { // real run (not an injected-Discovery test)
		mux := http.NewServeMux()
		mux.Handle("/api/links", linksHandler(a.linkReport))
		a.linksSrv = &http.Server{Addr: "0.0.0.0:" + strconv.Itoa(controlPort), Handler: httpauth.Middleware("")(mux)}
		go func() { _ = a.linksSrv.ListenAndServe() }()
	}
```

3g. Change `a.wg.Add(3)` to `a.wg.Add(4)` and add `go a.linkPullLoop(ctx)` with the others.

3h. Add the pull loop:
```go
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
				rep, err := a.FetchLinks("http://" + p.Addr)
				if err == nil && rep.NodeID != "" {
					a.reportMu.Lock()
					a.peerReports[rep.NodeID] = rep
					a.reportMu.Unlock()
				}
			}
		}
	}
}
```

3i. Add `Matrix` to the `Snapshot` struct:
```go
	Matrix Matrix
```

3j. In `Snapshot`, build the matrix. After the peer list is built (and still safe to call other-lock methods), add — but assemble OUTSIDE `a.mu` to avoid holding it during the report copy. Simplest: compute the matrix before taking `a.mu`, or take `a.reportMu` separately. Add near the end of `Snapshot`, before constructing the return value, reading reports under `a.reportMu`:
```go
	a.reportMu.Lock()
	reps := make(map[string]LinkReport, len(a.peerReports))
	for k, v := range a.peerReports {
		reps[k] = v
	}
	a.reportMu.Unlock()
	matrix := assembleMatrix(a.linkReport(), reps)
```
and add `Matrix: matrix,` to the returned `Snapshot{...}`.
Note: `a.linkReport()` takes `a.mu` internally — so this block MUST run OUTSIDE the `a.mu`-locked section of `Snapshot`. If `Snapshot` currently does all its work under one `a.mu.Lock()`/`defer Unlock()`, refactor so the matrix assembly happens after `a.mu` is released (capture the other Snapshot fields into locals under the lock, release, then assemble the matrix, then return). Ensure no double-lock of `a.mu`.

3k. In `Stop` (inside `stopOnce.Do`), shut the links server down (before closing the store):
```go
		if a.linksSrv != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = a.linksSrv.Shutdown(ctx)
			cancel()
		}
```

- [ ] **Step 4: Run to verify pass** — `go test ./internal/appcore/ -v` → ALL PASS (incl new). Run `go test -count=3 ./internal/appcore/` to shake out lock issues (`mu`, `peerMu`, `reportMu`). `go vet`, `gofmt -w`, `go build ./...`.

- [ ] **Step 5: Commit** — `git add internal/appcore/ && git commit -m "feat(appcore): serve + pull link reports, assemble Snapshot.Matrix (link-matrix)"`.

---

## Task 4: Gio matrix grid view

**Files:** Create `internal/ui/matrix.go`, `internal/ui/matrix_test.go`; modify `internal/ui/ui.go`.

- [ ] **Step 1: Write the failing test `internal/ui/matrix_test.go`**

```go
package ui

import (
	"image/color"
	"testing"

	"netlogger/internal/appcore"
)

func TestSevColorBands(t *testing.T) {
	good := sevColor(0.05, true)
	warn := sevColor(0.5, true)
	bad := sevColor(5.0, true)
	none := sevColor(0, false)
	if good == warn || warn == bad || good == bad {
		t.Fatalf("severity colors should differ: %v %v %v", good, warn, bad)
	}
	if none != (color.NRGBA{R: 0x99, G: 0x99, B: 0x99, A: 0xff}) {
		t.Fatalf("no-data color wrong: %v", none)
	}
}

func TestCellLabel(t *testing.T) {
	if got := cellLabel(appcore.MatrixCell{LossPct: 1.5}, true); got != "1.5%" {
		t.Fatalf("label = %q, want 1.5%%", got)
	}
	if got := cellLabel(appcore.MatrixCell{}, false); got != "–" {
		t.Fatalf("no-data label = %q, want dash", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/ui/ -run 'TestSevColor|TestCellLabel' -v` → FAIL.

- [ ] **Step 3: Implement `internal/ui/matrix.go`**

```go
package ui

import (
	"fmt"
	"image"
	"image/color"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"netlogger/internal/appcore"
)

// sevColor maps a loss percent to a CVD-safe severity color (Wong palette).
func sevColor(lossPct float64, hasData bool) color.NRGBA {
	if !hasData {
		return color.NRGBA{R: 0x99, G: 0x99, B: 0x99, A: 0xff}
	}
	switch {
	case lossPct < 0.1:
		return color.NRGBA{R: 0x00, G: 0x9E, B: 0x73, A: 0xff} // good
	case lossPct < 1.0:
		return color.NRGBA{R: 0xE6, G: 0x9F, B: 0x00, A: 0xff} // warn
	default:
		return color.NRGBA{R: 0xD5, G: 0x5E, B: 0x00, A: 0xff} // bad
	}
}

func cellLabel(c appcore.MatrixCell, hasData bool) string {
	if !hasData {
		return "–"
	}
	return fmt.Sprintf("%.1f%%", c.LossPct)
}

// layoutMatrix draws the N×N link matrix: rows = source, columns = destination,
// each cell colored by loss severity with the loss% as the label. The diagonal
// is greyed.
func layoutMatrix(gtx layout.Context, th *material.Theme, m appcore.Matrix) layout.Dimensions {
	if len(m.Nodes) == 0 {
		return material.Body1(th, "Link matrix: waiting for peers…").Layout(gtx)
	}
	const cell = 92
	const hdr = 96
	label := func(s string) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			lb := material.Caption(th, s)
			lb.Alignment = text.Middle
			return lb.Layout(gtx)
		}
	}
	// header row: corner + destination names
	rows := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			children := []layout.FlexChild{layout.Rigid(sizedBox(hdr, 28, label("src \\ dst")))}
			for _, n := range m.Nodes {
				children = append(children, layout.Rigid(sizedBox(cell, 28, label(n.Host))))
			}
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, children...)
		}),
	}
	for _, src := range m.Nodes {
		src := src
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			children := []layout.FlexChild{layout.Rigid(sizedBox(hdr, cell, label(src.Host)))}
			for _, dst := range m.Nodes {
				dst := dst
				children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if src.ID == dst.ID {
						return coloredCell(gtx, th, color.NRGBA{R: 0x33, G: 0x3a, B: 0x40, A: 0xff}, "")
					}
					c, ok := m.Cell(src.ID, dst.ID)
					return coloredCell(gtx, th, sevColor(c.LossPct, ok), cellLabel(c, ok))
				}))
			}
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, children...)
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}

func sizedBox(w, h int, inner layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min = image.Pt(gtx.Dp(unit.Dp(w)), gtx.Dp(unit.Dp(h)))
		gtx.Constraints.Max = gtx.Constraints.Min
		return layout.Center.Layout(gtx, inner)
	}
}

func coloredCell(gtx layout.Context, th *material.Theme, bg color.NRGBA, lbl string) layout.Dimensions {
	const cw, ch = 92, 56
	sz := image.Pt(gtx.Dp(unit.Dp(cw)), gtx.Dp(unit.Dp(ch)))
	r := image.Rectangle{Max: sz}
	defer clip.Rect{Min: image.Pt(2, 2), Max: image.Pt(sz.X - 2, sz.Y - 2)}.Push(gtx.Ops).Pop()
	paint.ColorOp{Color: bg}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	if lbl != "" {
		gtx.Constraints.Min = sz
		gtx.Constraints.Max = sz
		layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			t := material.Body2(th, lbl)
			t.Color = color.NRGBA{R: 0x06, G: 0x12, B: 0x1f, A: 0xff}
			return t.Layout(gtx)
		})
	}
	_ = op.Ops{}
	return layout.Dimensions{Size: r.Max}
}
```

> Note for the implementer: Gio v0.10 API specifics (clip/paint/text alignment) may need minor adjustment to compile — keep the structure (sized colored cells in a Flex grid, severity color by loss, diagonal greyed) and adapt calls to whatever the installed Gio exposes. The pure helpers `sevColor`/`cellLabel` (which the tests cover) must keep their signatures.

- [ ] **Step 4: Wire it into `ui.go`** — below the status panel, add the matrix. In `Run`'s frame handler, lay out status then matrix vertically. Replace the `layoutStatus(...)` call in the `FrameEvent` case with a vertical split:

```go
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			snap := a.Snapshot()
			layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layoutStatus(gtx, th, snap) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.UniformInset(unit.Dp(20)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layoutMatrix(gtx, th, snap.Matrix)
					})
				}),
			)
			e.Frame(gtx.Ops)
```

- [ ] **Step 5: Run to verify pass** — `go test ./internal/ui/ -v` (the pure-helper tests pass). `go build ./internal/ui/ && go vet ./internal/ui/ && gofmt -w internal/ui/`.

- [ ] **Step 6: Commit** — `git add internal/ui/ && git commit -m "feat(ui): color-coded N×N link matrix view (link-matrix)"`.

---

## Task 5: Build + two-machine manual verification

**Files:** none.

- [ ] **Step 1: Rebuild** — `powershell -ExecutionPolicy Bypass -File scripts/build-app.ps1` → `bin/NetLogger.exe`. Do NOT launch from automation.

- [ ] **Step 2: Manual (human runs):**
1. Close + relaunch `bin\NetLogger.exe` on both (ideally all) machines.
2. Below the status panel, a **matrix grid** appears: rows = source host, columns = destination host. Each off-diagonal cell is colored (green/orange/vermillion by loss) and shows the loss %; the diagonal is grey.
3. With 2 machines it's a 2×2 (the two off-diagonal cells = the two directions). With 3+, a full grid — a single window now shows **every link in the mesh**.
4. Generate load / reproduce the fault and watch a cell turn orange/red. A whole row/column lighting up = that source/dest node; a single cell = that one directed link.

- [ ] **Step 3:** On human confirmation, the Link Matrix milestone is complete and ready to merge.

---

## Self-Review

**Spec coverage (§5–6 Link Matrix):**
- Full N×N directed-link matrix from cross-node link-stat exchange → Tasks 1–3. ✓
- Rows=source / cols=dest, A→B and B→A separate, diagonal greyed → Task 1 (assemble) + Task 4 (render). ✓
- CVD-safe discrete severity bands colored by loss, value label → Task 4 (`sevColor`/`cellLabel`, spec §5.1 Wong palette). ✓
- Stable node order (by host) for glanceability → Task 1 (`assembleMatrix` sort). ✓
- Served over the control port with the kept `httpauth` (Host-allowlist) → Task 3. ✓

Deferred: Top-Suspects ranking, the timeline/correlation view, jitter-toggle, and shared-device inference (those wire in the kept `correlate`/`score` engine — a following milestone). This milestone delivers the live matrix; correlation/ranking builds on it.

**Placeholder scan:** none — complete code + commands. The one adaptivity note (Task 4 Gio API) is bounded to keeping the tested pure-helper signatures.

**Type consistency:** `LinkStat{PeerID,RTTms,JitterMs,LossPct,Drops}`, `LinkReport{NodeID,Host,Links}`, `Matrix{Nodes []MatrixNode}` + `Cell(src,dst)(MatrixCell,bool)`, `assembleMatrix(LinkReport, map[string]LinkReport) Matrix`, `linksHandler(func() LinkReport) http.HandlerFunc`, `fetchLinks(*http.Client,string)(LinkReport,error)`, `App.FetchLinks func(string)(LinkReport,error)`, `App.NodeID() string`, `Snapshot.Matrix Matrix`. UI `sevColor(float64,bool) color.NRGBA`, `cellLabel(MatrixCell,bool) string`, `layoutMatrix(...)`. All referenced consistently across tasks/tests.
