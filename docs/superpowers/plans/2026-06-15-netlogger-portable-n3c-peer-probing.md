# NetLogger Portable — N3c: Per-Peer Probing + Live Link Quality — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Each instance continuously **probes every discovered peer** (ICMP) and shows **live per-link RTT + loss** in the window — so launching on two machines gives a real, continuously-updating measurement of the link between them (the actual Ryzen↔ProjectorPC path). No HTTP server is required for this slice (ICMP needs no peer-side service); the cross-machine sync API + sample pull (for the full Link Matrix) is deferred to N3d.

**Architecture:** A small `peerStat` aggregator (pure, lock-guarded ring of recent successes + last RTT) tracks each peer. `appcore` gains a peer-probe loop that, each tick, pings every discovered peer's IP and records to its `peerStat`; `Snapshot.Peers[i]` is enriched with `RTTms`/`LossPct`. The Gio UI renders those per peer. The existing self-probe loop and all N3b behavior are unchanged.

**Tech Stack:** Go (cgo-free), existing `internal/probe`, `internal/discovery`, Gio.

Reference spec: `docs/superpowers/specs/2026-06-15-netlogger-portable-design.md` §3.1, §5 (per-link metrics).

---

## File Structure

| Path | Responsibility |
|---|---|
| `internal/appcore/peerstat.go` | `peerStat` — per-peer rolling RTT + loss aggregator (pure, lock-guarded). |
| `internal/appcore/peerstat_test.go` | Unit tests for record/read math. |
| `internal/appcore/appcore.go` | Modify: peer-probe loop, per-peer stats map, `Snapshot.Peers` RTT/loss enrichment. |
| `internal/appcore/appcore_test.go` | Add: injected-Ping + fake-discovery test that `Snapshot.Peers` shows RTT/loss. |
| `internal/ui/ui.go` | Modify: show `RTT / loss` on each peer row. |

---

## Task 1: `peerStat` aggregator (pure)

**Files:** Create `internal/appcore/peerstat.go`, `internal/appcore/peerstat_test.go`.

- [ ] **Step 1: Write the failing test `internal/appcore/peerstat_test.go`**

```go
package appcore

import "testing"

func TestPeerStatRTTandLoss(t *testing.T) {
	s := &peerStat{}
	s.record(true, 1.5)
	s.record(true, 2.5)
	s.record(false, 0) // a loss
	rtt, loss := s.read()
	if rtt != 2.5 {
		t.Fatalf("expected lastRTT 2.5 (last successful), got %v", rtt)
	}
	if loss < 33.0 || loss > 34.0 {
		t.Fatalf("expected ~33.3%% loss over 3 samples, got %v", loss)
	}
}

func TestPeerStatEmpty(t *testing.T) {
	s := &peerStat{}
	rtt, loss := s.read()
	if rtt != 0 || loss != 0 {
		t.Fatalf("expected zero rtt/loss when empty, got %v/%v", rtt, loss)
	}
}

func TestPeerStatWindowBounded(t *testing.T) {
	s := &peerStat{}
	for i := 0; i < recentWindow+50; i++ {
		s.record(true, 1.0)
	}
	if got := len(s.recent); got != recentWindow {
		t.Fatalf("expected recent capped at %d, got %d", recentWindow, got)
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/appcore/ -run TestPeerStat -v` → FAIL (undefined: peerStat).

- [ ] **Step 3: Implement `internal/appcore/peerstat.go`**

```go
package appcore

import "sync"

// peerStat is a per-peer rolling aggregator: last successful RTT (ms) and packet
// loss over the most recent recentWindow probes.
type peerStat struct {
	mu        sync.Mutex
	lastRTTms float64
	recent    []bool // success flags, newest appended
}

// record adds one probe result. rttms is ignored when ok is false.
func (s *peerStat) record(ok bool, rttms float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ok {
		s.lastRTTms = rttms
	}
	s.recent = append(s.recent, ok)
	if len(s.recent) > recentWindow {
		s.recent = s.recent[len(s.recent)-recentWindow:]
	}
}

// read returns the last successful RTT (ms) and loss percent over the window.
func (s *peerStat) read() (rttms, lossPct float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.recent)
	if n == 0 {
		return 0, 0
	}
	lost := 0
	for _, ok := range s.recent {
		if !ok {
			lost++
		}
	}
	return s.lastRTTms, float64(lost) / float64(n) * 100.0
}
```

- [ ] **Step 4: Run to verify pass** — `go test ./internal/appcore/ -run TestPeerStat -v` → PASS. `gofmt -w internal/appcore/ && go vet ./internal/appcore/`.

- [ ] **Step 5: Commit** — `git add internal/appcore/peerstat.go internal/appcore/peerstat_test.go && git commit -m "feat(appcore): per-peer RTT/loss aggregator (N3c)"` (footer).

---

## Task 2: Peer-probe loop + `Snapshot.Peers` enrichment

**Files:** Modify `internal/appcore/appcore.go`, add a test to `internal/appcore/appcore_test.go`.

- [ ] **Step 1: Write the failing test (append to `internal/appcore/appcore_test.go`)**

```go
func TestSnapshotShowsPerPeerRTTAndLoss(t *testing.T) {
	dir := t.TempDir()
	a := New(dir)
	a.Ping = func(addr string, _ time.Duration) (probe.Result, error) {
		return probe.Result{RTT: 2 * time.Millisecond}, nil // 2.0 ms, no loss
	}
	a.StartIperf = func(string) (func(), string) { return func() {}, "" }
	a.Discovery = fakeLister{peers: []discovery.Peer{
		{ID: "p1", Host: "h1", Addr: "10.0.0.1:8088"},
	}}
	a.tick = 5 * time.Millisecond
	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()
	deadline := time.Now().Add(2 * time.Second)
	for {
		ps := a.Snapshot().Peers
		if len(ps) == 1 && ps[0].RTTms > 0 {
			if ps[0].LossPct != 0 {
				t.Fatalf("expected 0%% loss, got %v", ps[0].LossPct)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("peer RTT not populated; got %+v", a.Snapshot().Peers)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/appcore/ -run TestSnapshotShowsPerPeer -v` → FAIL (PeerInfo has no RTTms/LossPct, no peer loop).

- [ ] **Step 3: Modify `internal/appcore/appcore.go`**

3a. Add `"net"` to the import block.

3b. Add two fields to `PeerInfo`:
```go
	RTTms   float64
	LossPct float64
```

3c. Add a per-peer stats map to `App` (after the `recent []bool` field):
```go
	peerMu    sync.Mutex
	peerStats map[string]*peerStat
```

3d. In `New`, initialize the map:
```go
		peerStats:  make(map[string]*peerStat),
```
(add this line to the `&App{...}` literal returned by `New`).

3e. Add a helper to get-or-create a peer's stat:
```go
func (a *App) statFor(id string) *peerStat {
	a.peerMu.Lock()
	defer a.peerMu.Unlock()
	s := a.peerStats[id]
	if s == nil {
		s = &peerStat{}
		a.peerStats[id] = s
	}
	return s
}
```

3f. Add the peer-probe loop:
```go
// peerLoop probes every discovered peer once per tick and records per-peer stats.
func (a *App) peerLoop(ctx context.Context) {
	defer a.wg.Done()
	t := time.NewTicker(a.tick)
	defer t.Stop()
	var seq int64
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
				host := p.Addr
				if h, _, err := net.SplitHostPort(p.Addr); err == nil {
					host = h
				}
				res, err := a.Ping(host, 2*time.Second)
				lost := err != nil || res.Lost
				rttms := float64(res.RTT.Microseconds()) / 1000.0
				a.statFor(p.ID).record(!lost, rttms)
				seq++
				_, _ = a.store.Insert(store.Sample{
					Seq: seq, TSUnixUS: time.Now().UnixMicro(), ProbeType: "icmp",
					SrcHost: a.nodeID, DstHost: p.ID, Direction: "rtt",
					RTTus: res.RTT.Microseconds(), Lost: lost,
				})
			}
		}
	}
}
```

3g. In `Start`, launch the peer loop alongside the existing probe loop. Where the code currently does `a.wg.Add(1); go a.probeLoop(ctx)`, change to:
```go
	a.wg.Add(2)
	go a.probeLoop(ctx)
	go a.peerLoop(ctx)
```

3h. In `Snapshot`, enrich each peer with its stats. In the loop that builds `peers` from `a.Discovery.Peers()`, set RTT/loss per peer:
```go
		for _, p := range a.Discovery.Peers() {
			rtt, loss := a.statFor(p.ID).read()
			peers = append(peers, PeerInfo{
				ID: p.ID, Host: p.Host, Addr: p.Addr, Version: p.Version,
				LastSeenUnix: p.LastSeen.Unix(), RTTms: rtt, LossPct: loss,
			})
		}
```
(Note: `statFor` takes `a.peerMu`, not `a.mu`, so calling it while holding `a.mu` in Snapshot is safe — no lock-ordering cycle, since `peerLoop` acquires `a.mu` and `a.peerMu` separately and never holds both at once. Verify peerLoop releases `a.mu` before calling `statFor` — in 3f it does: it copies `disc` under `a.mu`, unlocks, then calls `statFor`.)

- [ ] **Step 4: Run to verify pass** — `go test ./internal/appcore/ -v` → ALL PASS (incl new test + all prior). `go vet ./internal/appcore/ && gofmt -w internal/appcore/ && go build ./...`.

- [ ] **Step 5: Commit** — `git add internal/appcore/ && git commit -m "feat(appcore): probe discovered peers and report per-link RTT/loss (N3c)"`.

---

## Task 3: Show per-peer RTT/loss in the UI

**Files:** Modify `internal/ui/ui.go` (manually verified).

- [ ] **Step 1: Update the peer row format** in `layoutStatus`. Replace the peer-row append loop:

```go
	for _, p := range s.Peers {
		rows = append(rows, fmt.Sprintf("   - %-14s %-20s  RTT %.2f ms   loss %.1f%%",
			peerName(p), p.Addr, p.RTTms, p.LossPct))
	}
```

- [ ] **Step 2: Verify it compiles** — `go build ./internal/ui/ && gofmt -w internal/ui/ && go vet ./internal/ui/`.

- [ ] **Step 3: Commit** — `git add internal/ui/ && git commit -m "feat(ui): show per-peer RTT and loss (N3c)"`.

---

## Task 4: Build + two-machine manual verification (acceptance gate)

**Files:** none.

- [ ] **Step 1: Rebuild** — `powershell -ExecutionPolicy Bypass -File scripts/build-app.ps1` → `bin/NetLogger.exe`. Do NOT launch from automation.

- [ ] **Step 2: Manual (human runs):**
1. Launch the rebuilt `bin\NetLogger.exe` on **both** machines (approve UAC).
2. Each "Discovered peers" row now shows a live **RTT (ms)** and **loss %** to the other machine, updating ~1/sec.
3. Generate load (e.g., start the Moonlight stream, or a large file copy) → watch the RTT rise / loss appear on the affected link.
4. Numbers are plausible for a LAN (sub-millisecond to low-single-digit ms RTT, ~0% loss at idle).

- [ ] **Step 3:** On human confirmation, N3c is complete and ready to merge.

---

## Self-Review

**Spec coverage (N3c slice):**
- Probe each discovered peer + per-link RTT/loss → Task 1 (`peerStat`) + Task 2 (`peerLoop`). ✓
- Per-link metrics surfaced (the Link Matrix's raw input, A→B direction) → Task 2 (`Snapshot.Peers` RTT/loss) + Task 3 (UI). ✓
- Samples persisted labeled by peer → Task 2 (`store.Insert` DstHost=peer ID). ✓

Deferred to N3d (out of N3c): the in-process HTTP sync API (`mesh.AgentAPI`), pulling peers' samples (so each box has *both* directions / the full mesh), and clock-offset measurement. N3c measures only the local→peer (A→B) direction; N3d adds the rest for the full Link Matrix.

**Placeholder scan:** none — complete code + exact commands in every step.

**Type consistency:** `peerStat.record(bool,float64)`, `peerStat.read() (float64,float64)`, `App.peerStats map[string]*peerStat`, `App.statFor(string) *peerStat`, `App.peerLoop(context.Context)`, `PeerInfo{...,RTTms,LossPct float64}`. `discovery.Peer{ID,Host,Addr,Version,LastSeen}`, `store.Sample{Seq,TSUnixUS,ProbeType,SrcHost,DstHost,Direction,RTTus,Lost}`, `probe.Result{RTT,Lost}` all match the merged packages. UI references `PeerInfo.RTTms`/`LossPct`. `recentWindow` reused from `appcore.go`.
