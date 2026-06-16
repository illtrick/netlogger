# NetLogger Portable — UX Overhaul (uptime, internet probe, history charts, app layout) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Turn the "glorified CLI" into a glanceable app: a header with **session uptime** + overall status, an **Infrastructure** section probing the **gateway and the internet (8.8.8.8)**, per-peer **"up for" duration**, **time-series sparkline charts** (RTT/loss history) for the key factors, a clear matrix legend, and logs written **beside the executable**.

**Architecture:** Backend additions to `appcore` (an internet ICMP probe alongside the gateway; per-peer first-seen tracking for uptime; rolling history ring buffers per peer + gateway + internet, exposed on `Snapshot`). A new pure Gio `sparkline` helper draws a `[]float64` as a line chart. `ui.go` is restructured into a header / infrastructure / peers / matrix layout. `main.go` logs beside the exe.

**Tech Stack:** Go (cgo-free), existing `appcore`/`probe`/`gateway`, Gio (custom line drawing via `clip`/`paint`).

Reference spec: `docs/superpowers/specs/2026-06-15-netlogger-portable-design.md` §6 (screens, glanceability), §5 (per-link metrics).

Design constants: internet probe target `8.8.8.8`; history length `120` points (≈2 min at 1 Hz).

---

## File Structure

| Path | Responsibility |
|---|---|
| `internal/appcore/history.go` | `histRing` — fixed-capacity rolling `[]float64` buffer. |
| `internal/appcore/history_test.go` | Tests for the ring. |
| `internal/appcore/appcore.go` | Modify: internet probe, per-peer first-seen, history capture, new `Snapshot` fields. |
| `internal/appcore/appcore_test.go` | Add: internet + uptime + history snapshot tests. |
| `cmd/netlogger-app/main.go` | Modify: log beside the executable. |
| `internal/ui/sparkline.go` | Pure scaling helpers + a Gio sparkline widget. |
| `internal/ui/sparkline_test.go` | Tests for the scaling helper. |
| `internal/ui/ui.go` | Restructure: header / infrastructure / peers (with sparklines) / matrix + legend. |

---

## Task 1: `histRing` rolling buffer + appcore wiring (internet probe, uptime, history)

**Files:** Create `internal/appcore/history.go`, `internal/appcore/history_test.go`; modify `internal/appcore/appcore.go`; add tests to `internal/appcore/appcore_test.go`.

- [ ] **Step 1: Write the failing `history_test.go`**

```go
package appcore

import "testing"

func TestHistRingCapsAndOrders(t *testing.T) {
	r := newHistRing(3)
	r.push(1)
	r.push(2)
	r.push(3)
	r.push(4) // evicts 1
	got := r.values()
	if len(got) != 3 || got[0] != 2 || got[2] != 4 {
		t.Fatalf("expected [2 3 4], got %v", got)
	}
}

func TestHistRingPartial(t *testing.T) {
	r := newHistRing(5)
	r.push(7)
	r.push(8)
	got := r.values()
	if len(got) != 2 || got[0] != 7 || got[1] != 8 {
		t.Fatalf("expected [7 8], got %v", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/appcore/ -run TestHistRing -v` → FAIL.

- [ ] **Step 3: Implement `internal/appcore/history.go`**

```go
package appcore

import "sync"

// histRing is a fixed-capacity rolling buffer of float64 samples (oldest first
// when read), used for per-metric sparklines.
type histRing struct {
	mu   sync.Mutex
	buf  []float64
	cap  int
}

func newHistRing(capacity int) *histRing { return &histRing{cap: capacity} }

func (r *histRing) push(v float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, v)
	if len(r.buf) > r.cap {
		r.buf = r.buf[len(r.buf)-r.cap:]
	}
}

// values returns a copy of the buffer, oldest first.
func (r *histRing) values() []float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]float64, len(r.buf))
	copy(out, r.buf)
	return out
}
```

- [ ] **Step 4: Run to verify pass** — `go test ./internal/appcore/ -run TestHistRing -v` → PASS.

- [ ] **Step 5: Write the appcore snapshot test (append to `internal/appcore/appcore_test.go`)**

```go
func TestSnapshotInternetUptimeAndHistory(t *testing.T) {
	dir := t.TempDir()
	a := New(dir)
	a.Ping = func(addr string, _ time.Duration) (probe.Result, error) {
		return probe.Result{RTT: 2 * time.Millisecond}, nil
	}
	a.StartIperf = func(string) (func(), string) { return func() {}, "" }
	a.FetchLinks = func(string) (LinkReport, error) { return LinkReport{}, nil }
	a.ProbeUDP = func(string, int, time.Duration, time.Duration) (probe.UDPStats, error) {
		return probe.UDPStats{Sent: 200, Received: 200, AvgRTT: time.Millisecond, Jitter: 100 * time.Microsecond}, nil
	}
	a.Discovery = fakeLister{peers: []discovery.Peer{{ID: "p1", Host: "h1", Addr: "10.0.0.1:8088"}}}
	a.GatewayIP = "192.168.0.1"
	a.InternetIP = "8.8.8.8"
	a.tick = 5 * time.Millisecond
	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()
	deadline := time.Now().Add(2 * time.Second)
	for {
		s := a.Snapshot()
		if s.InternetIP == "8.8.8.8" && s.InternetRTTms > 0 && len(s.Peers) == 1 && s.Peers[0].UpForSec >= 0 && len(s.Peers[0].RTTHist) > 0 {
			if s.SessionUptimeSec < 0 {
				t.Fatalf("bad uptime %d", s.SessionUptimeSec)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("internet/uptime/history not populated; got %+v", a.Snapshot())
		}
		time.Sleep(5 * time.Millisecond)
	}
}
```

- [ ] **Step 6: Modify `internal/appcore/appcore.go`**

6a. Add a constant in the existing `const (...)` block: `internetTarget = "8.8.8.8"` and `histLen = 120`.

6b. Add exported field to `App` (near `GatewayIP`): `InternetIP string`. In `New`, default it: `InternetIP: internetTarget,` in the `&App{...}` literal (only if you also make it overridable; keep the default).

6c. Add to `App`: per-peer first-seen + history maps (guard with the existing `a.peerMu`):
```go
	firstSeen map[string]time.Time
	rttHist   map[string]*histRing
	lossHist  map[string]*histRing
	gwHist    *histRing
	netHist   *histRing
```
In `New`, init: `firstSeen: make(map[string]time.Time), rttHist: make(map[string]*histRing), lossHist: make(map[string]*histRing), gwHist: newHistRing(histLen), netHist: newHistRing(histLen),`.

6d. Helpers (use `a.peerMu`):
```go
func (a *App) histFor(m map[string]*histRing, id string) *histRing {
	a.peerMu.Lock()
	defer a.peerMu.Unlock()
	r := m[id]
	if r == nil {
		r = newHistRing(histLen)
		m[id] = r
	}
	return r
}
func (a *App) markSeen(id string) time.Time {
	a.peerMu.Lock()
	defer a.peerMu.Unlock()
	if t, ok := a.firstSeen[id]; ok {
		return t
	}
	now := time.Now()
	a.firstSeen[id] = now
	return now
}
```

6e. In `peerLoop`, after the peers loop and the gateway probe, add the internet probe + push gateway/internet history. Where the gateway probe currently records to `statFor("__gateway__")`, also push to `a.gwHist`, and add an internet probe:
```go
			if a.GatewayIP != "" {
				res, err := a.Ping(a.GatewayIP, 2*time.Second)
				ok := err == nil && !res.Lost
				a.statFor("__gateway__").record(ok, float64(res.RTT.Microseconds())/1000.0)
				if ok {
					a.gwHist.push(float64(res.RTT.Microseconds()) / 1000.0)
				}
			}
			if a.InternetIP != "" {
				res, err := a.Ping(a.InternetIP, 2*time.Second)
				ok := err == nil && !res.Lost
				a.statFor("__internet__").record(ok, float64(res.RTT.Microseconds())/1000.0)
				if ok {
					a.netHist.push(float64(res.RTT.Microseconds()) / 1000.0)
				}
			}
```
(Replace the existing gateway-only block with the above.)

6f. In `udpLoop`, after `a.udpStatFor(p.ID).record(st)`, also push history + mark seen:
```go
				rtt, _, loss, _ := a.udpStatFor(p.ID).read()
				a.histFor(a.rttHist, p.ID).push(rtt)
				a.histFor(a.lossHist, p.ID).push(loss)
				a.markSeen(p.ID)
```

6g. Add fields to `Snapshot`:
```go
	SessionUptimeSec int64
	InternetIP       string
	InternetRTTms    float64
	InternetLossPct  float64
	GatewayHist      []float64
	InternetHist     []float64
```
and add to `PeerInfo`:
```go
	UpForSec int64
	RTTHist  []float64
	LossHist []float64
```

6h. In `Snapshot`, populate the new fields. Inside the peer-building loop add:
```go
			fs := a.markSeen(p.ID)
			peers[len(peers)-1].UpForSec = int64(time.Since(fs).Seconds())
			peers[len(peers)-1].RTTHist = a.histFor(a.rttHist, p.ID).values()
			peers[len(peers)-1].LossHist = a.histFor(a.lossHist, p.ID).values()
```
(Adjust to set the fields on the PeerInfo you just appended — restructure the append so you can set these, e.g. build the PeerInfo into a local `pi`, set hist fields, then append.)
After the peer loop, set internet + uptime fields under the lock:
```go
	var netRTT, netLoss float64
	if a.InternetIP != "" {
		netRTT, netLoss = a.statFor("__internet__").read()
	}
```
and in the returned `Snapshot{...}` add:
```go
		SessionUptimeSec: int64(time.Since(a.startedAt).Seconds()),
		InternetIP:       a.InternetIP, InternetRTTms: netRTT, InternetLossPct: netLoss,
		GatewayHist:  a.gwHist.values(),
		InternetHist: a.netHist.values(),
```
Note: `a.gwHist.values()`/`a.netHist.values()` take their own lock — fine to call while holding `a.mu`. `markSeen`/`histFor` take `a.peerMu` — also fine while holding `a.mu` (consistent a.mu→peerMu order). Keep the matrix assembly OUTSIDE `a.mu` as before.

- [ ] **Step 7: Run to verify pass** — `go test ./internal/appcore/ -v` → ALL PASS. `go test -count=3 ./internal/appcore/` (no deadlock). `go vet`, `gofmt -w`, `go build ./...`.

- [ ] **Step 8: Commit** — `git add internal/appcore/ && git commit -m "feat(appcore): internet probe, per-peer uptime, history rings in Snapshot (ux)"`.

---

## Task 2: Log beside the executable

**Files:** Modify `cmd/netlogger-app/main.go`.

- [ ] **Step 1: Change the log target** — instead of `applog.Init(dir)` (the data dir), log next to the exe. After `datadir.Resolve()`, compute the exe dir and prefer it for the log:

```go
	logDir := dir
	if exe, err := os.Executable(); err == nil {
		logDir = filepath.Dir(exe)
	}
	logFile, err := applog.Init(logDir)
	if err == nil {
		defer logFile.Close()
	}
```
Add `"path/filepath"` to the imports if not present.

- [ ] **Step 2: Verify** — `go build ./cmd/netlogger-app` compiles. `gofmt -w cmd/netlogger-app/`.

- [ ] **Step 3: Commit** — `git add cmd/netlogger-app/ && git commit -m "feat(app): write the log beside the executable (ux)"`.

---

## Task 3: Sparkline helper + Gio widget

**Files:** Create `internal/ui/sparkline.go`, `internal/ui/sparkline_test.go`.

- [ ] **Step 1: Write the failing test `internal/ui/sparkline_test.go`**

```go
package ui

import "testing"

func TestNormalizeScalesToUnit(t *testing.T) {
	pts := normalize([]float64{0, 5, 10})
	if len(pts) != 3 || pts[0] != 0 || pts[2] != 1 {
		t.Fatalf("expected 0..1, got %v", pts)
	}
	if pts[1] < 0.49 || pts[1] > 0.51 {
		t.Fatalf("midpoint = %v, want ~0.5", pts[1])
	}
}

func TestNormalizeFlatAndEmpty(t *testing.T) {
	if got := normalize(nil); got != nil {
		t.Fatalf("nil -> %v", got)
	}
	flat := normalize([]float64{3, 3, 3})
	for _, v := range flat {
		if v != 0.5 {
			t.Fatalf("flat series should map to 0.5, got %v", flat)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/ui/ -run TestNormalize -v` → FAIL.

- [ ] **Step 3: Implement `internal/ui/sparkline.go`**

```go
package ui

import (
	"image"
	"image/color"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// normalize maps a series to 0..1 by min/max. A flat or empty series maps every
// point to 0.5 (a flat mid-line). Returns nil for nil/empty input.
func normalize(v []float64) []float64 {
	if len(v) == 0 {
		return nil
	}
	min, max := v[0], v[0]
	for _, x := range v {
		if x < min {
			min = x
		}
		if x > max {
			max = x
		}
	}
	out := make([]float64, len(v))
	if max == min {
		for i := range out {
			out[i] = 0.5
		}
		return out
	}
	for i, x := range v {
		out[i] = (x - min) / (max - min)
	}
	return out
}

// sparkline draws series as a line chart filling a w×h dp box (line color col).
func sparkline(gtx layout.Context, series []float64, col color.NRGBA, w, h int) layout.Dimensions {
	sz := image.Pt(gtx.Dp(unit.Dp(w)), gtx.Dp(unit.Dp(h)))
	n := normalize(series)
	if len(n) >= 2 {
		var p clip.Path
		p.Begin(gtx.Ops)
		x := func(i int) float32 { return float32(i) / float32(len(n)-1) * float32(sz.X) }
		y := func(v float64) float32 { return float32(sz.Y) - float32(v)*float32(sz.Y) }
		p.MoveTo(f32pt(x(0), y(n[0])))
		for i := 1; i < len(n); i++ {
			p.LineTo(f32pt(x(i), y(n[i])))
		}
		spec := p.End()
		paint.FillShape(gtx.Ops, col, clip.Stroke{Path: spec, Width: float32(gtx.Dp(unit.Dp(1.5)))}.Op())
	}
	return layout.Dimensions{Size: sz}
}
```

Add a tiny float32-point helper (Gio uses `f32.Point`):
```go
import "gioui.org/f32"

func f32pt(x, y float32) f32.Point { return f32.Pt(x, y) }
```
(Merge imports.) Note for the implementer: if the Gio v0.10 `clip.Path`/`clip.Stroke`/`paint.FillShape` API differs, adapt the drawing calls — but keep `normalize`'s signature (tests depend on it).

- [ ] **Step 4: Run to verify pass** — `go test ./internal/ui/ -run TestNormalize -v` → PASS. `go build ./internal/ui/ && go vet ./internal/ui/ && gofmt -w internal/ui/`.

- [ ] **Step 5: Commit** — `git add internal/ui/sparkline.go internal/ui/sparkline_test.go && git commit -m "feat(ui): sparkline normalize helper + line widget (ux)"`.

---

## Task 4: Restructured app layout (header, infrastructure, peers w/ charts, matrix legend)

**Files:** Modify `internal/ui/ui.go`. Manually verified (visual).

- [ ] **Step 1: Replace the `Run` frame body** to compose, top to bottom: a header (title + overall-status chip + session uptime), an Infrastructure section (gateway + internet rows with RTT/loss and a sparkline each), a Peers section (per-peer: host, "up for", RTT/jitter/loss/drops, and RTT + loss sparklines), then the Link Matrix with a one-line legend. Use the existing `material.Theme`, the `sparkline` helper (Task 3), `layoutMatrix` (existing), and add helpers:

```go
func fmtDuration(sec int64) string {
	if sec < 60 {
		return fmt.Sprintf("%ds", sec)
	}
	if sec < 3600 {
		return fmt.Sprintf("%dm %ds", sec/60, sec%60)
	}
	return fmt.Sprintf("%dh %dm", sec/3600, (sec%3600)/60)
}

func overallStatus(s appcore.Snapshot) (string, color.NRGBA) {
	worst := 0.0
	for _, p := range s.Peers {
		if p.UDPLossPct > worst {
			worst = p.UDPLossPct
		}
	}
	if s.InternetLossPct > worst {
		worst = s.InternetLossPct
	}
	if s.GatewayLossPct > worst {
		worst = s.GatewayLossPct
	}
	switch {
	case worst >= 1.0:
		return "DEGRADED", color.NRGBA{R: 0xD5, G: 0x5E, B: 0x00, A: 0xff}
	case worst >= 0.1:
		return "WATCH", color.NRGBA{R: 0xE6, G: 0x9F, B: 0x00, A: 0xff}
	default:
		return "ALL HEALTHY", color.NRGBA{R: 0x00, G: 0x9E, B: 0x73, A: 0xff}
	}
}
```

Compose with vertical/horizontal `layout.Flex`, `layout.Rigid`, `layout.UniformInset`. The peers section draws, per peer: a label line (`fmt.Sprintf("%s  up %s   RTT %.2f ms  jitter %.2f ms  loss %.1f%%  drops %d", host, fmtDuration(p.UpForSec), p.RTTms, p.JitterMs, p.UDPLossPct, p.DropEpisodes)`) and below it two sparklines: `sparkline(gtx, p.RTTHist, blue, 220, 28)` and `sparkline(gtx, p.LossHist, vermillion, 220, 28)`. Infrastructure rows show gateway/internet IP + RTT + loss + a `sparkline` of `s.GatewayHist` / `s.InternetHist`.

Add a matrix legend line below `layoutMatrix`: a `material.Caption` reading `"loss: green <0.1%   orange <1%   red ≥1%   (rows=source, cols=destination)"`.

> The exact Gio composition is the implementer's to assemble; keep it readable (helpers per section). The pure helpers (`fmtDuration`, `overallStatus`, `normalize`) should have their behavior covered — add quick tests for `fmtDuration` and `overallStatus` to `internal/ui/`.

- [ ] **Step 2: Add tests** for the pure UI helpers (append to `internal/ui/sparkline_test.go` or a new `ui_helpers_test.go`):

```go
func TestFmtDuration(t *testing.T) {
	if fmtDuration(45) != "45s" || fmtDuration(125) != "2m 5s" || fmtDuration(3725) != "1h 2m" {
		t.Fatalf("durations: %q %q %q", fmtDuration(45), fmtDuration(125), fmtDuration(3725))
	}
}

func TestOverallStatus(t *testing.T) {
	if s, _ := overallStatus(appcore.Snapshot{}); s != "ALL HEALTHY" {
		t.Fatalf("empty = %q", s)
	}
	if s, _ := overallStatus(appcore.Snapshot{Peers: []appcore.PeerInfo{{UDPLossPct: 2}}}); s != "DEGRADED" {
		t.Fatalf("lossy = %q", s)
	}
}
```
(Add `"testing"` / `"netlogger/internal/appcore"` imports as needed.)

- [ ] **Step 3: Verify** — `go test ./internal/ui/ -v` (helper tests pass), `go build ./...`, `go vet ./internal/ui/`, `gofmt -w internal/ui/`.

- [ ] **Step 4: Commit** — `git add internal/ui/ && git commit -m "feat(ui): app layout — header/uptime, infrastructure, peer charts, matrix legend (ux)"`.

---

## Task 5: Build + manual visual verification

- [ ] **Step 1: Rebuild** — `powershell -ExecutionPolicy Bypass -File scripts/build-app.ps1` → `bin/NetLogger.exe`.
- [ ] **Step 2: Manual (human):** relaunch on the machines and confirm: header shows session uptime + a green ALL-HEALTHY chip; Infrastructure shows Gateway + Internet (8.8.8.8) with RTT/loss + sparklines; each peer shows "up for", metrics, and RTT/loss sparklines that fill in over ~30–60 s; the matrix has a legend; and `netlogger.log` is now **beside the .exe**. Report back with a screenshot for layout iteration.

---

## Self-Review

**Spec / request coverage:**
- Log beside the exe → Task 2. ✓
- Internet/WAN probe (8.8.8.8) → Task 1 (`InternetIP` probe) + Task 4 (Infrastructure section). ✓
- "How long connections have been up" → Task 1 (session uptime + per-peer first-seen) + Task 4 (header + per-peer "up for"). ✓
- Historic info / charts for key factors → Task 1 (history rings) + Task 3 (sparkline) + Task 4 (RTT/loss sparklines per peer + infra). ✓
- Clarify which connections are good/bad → Task 4 (overall-status chip + matrix legend; the colored matrix already encodes per-link health). ✓ (Full node-link system diagram intentionally deferred to a follow-up.)
- Less "glorified CLI" → Task 4 (structured header/sections/charts). ✓

**Placeholder scan:** Backend tasks and the pure helpers have complete code + tests. Task 4's Gio composition is intentionally left to the implementer to assemble from the specified pieces (helpers, sparkline, sections) — the testable logic (`fmtDuration`, `overallStatus`, `normalize`) is fully specified and tested.

**Type consistency:** `histRing` (`newHistRing`/`push`/`values`); `App.InternetIP`; `Snapshot.{SessionUptimeSec int64, InternetIP string, InternetRTTms/InternetLossPct float64, GatewayHist/InternetHist []float64}`; `PeerInfo.{UpForSec int64, RTTHist/LossHist []float64}`; `normalize([]float64) []float64`, `sparkline(...)`, `fmtDuration(int64) string`, `overallStatus(Snapshot) (string, color.NRGBA)`. Consistent across tasks/tests.
