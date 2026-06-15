# NetLogger Portable — N3d: High-Fidelity UDP Probing (Jitter + Micro-Drops) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Catch the actual game-stream failure mode — **jitter and millisecond-scale micro-drops** that 1 Hz ICMP samples right past. Each instance runs a UDP echo responder and probes every discovered peer with the existing isochronous high-rate UDP probe (e.g. 200 Hz bursts), surfacing per-peer **jitter, UDP loss, and a cumulative micro-drop-episode count**. This is what makes the tool sensitive to the suspected I226-V/E3100G EEE dropout and switch microbursts under load.

**Architecture:** Reuse the engine's `probe.UDPEcho` (responder) and `probe.ProbeUDP` (isochronous probe → `UDPStats{LossPct, AvgRTT, Jitter}`). `appcore` starts an echo responder on a fixed UDP port, runs a high-rate UDP probe loop against each discovered peer (via an injectable `ProbeUDP` seam), aggregates results in a `udpStat`, and exposes jitter/UDP-loss/episodes per peer in `Snapshot`. The UI shows them. The program-scoped firewall rule already covers inbound UDP to our process (unlike ICMP), so no new firewall work.

**Tech Stack:** Go (cgo-free), existing `internal/probe` (`UDPEcho`, `ProbeUDP`, `UDPStats`), Gio.

Reference spec: `docs/superpowers/specs/2026-06-15-netlogger-portable-design.md` §4–5 (per-link metrics, jitter, micro-drop episodes).

Design constants (in `appcore`): UDP echo port `8089`; probe burst `count=200`, `interval=5ms` (≈200 Hz, ~1 s/burst), `timeout=200ms`.

Existing engine signatures (do not change):
- `probe.StartUDPEcho(addr string) (*probe.UDPEcho, error)`; `(*UDPEcho).Addr() string`; `(*UDPEcho).Close() error`.
- `probe.ProbeUDP(target string, count int, interval, timeout time.Duration) (probe.UDPStats, error)`.
- `probe.UDPStats{ Sent, Received int; LossPct float64; AvgRTT, Jitter time.Duration }`.

---

## File Structure

| Path | Responsibility |
|---|---|
| `internal/appcore/udpstat.go` | `udpStat` — per-peer latest jitter/RTT/loss + cumulative micro-drop episode count. |
| `internal/appcore/udpstat_test.go` | Unit tests for `udpStat`. |
| `internal/appcore/appcore.go` | Modify: start UDP echo responder, `ProbeUDP` seam, UDP probe loop, `Snapshot.Peers` UDP fields. |
| `internal/appcore/appcore_test.go` | Add: injected-`ProbeUDP` test that `Snapshot.Peers` shows jitter/UDP-loss/episodes. |
| `internal/ui/ui.go` | Modify: show jitter + UDP loss + episode count per peer. |

---

## Task 1: `udpStat` aggregator (pure)

**Files:** Create `internal/appcore/udpstat.go`, `internal/appcore/udpstat_test.go`.

- [ ] **Step 1: Write the failing test `internal/appcore/udpstat_test.go`**

```go
package appcore

import (
	"testing"
	"time"

	"netlogger/internal/probe"
)

func TestUDPStatRecordsLatestAndCountsEpisodes(t *testing.T) {
	s := &udpStat{}
	s.record(probe.UDPStats{Received: 200, Sent: 200, LossPct: 0, AvgRTT: 800 * time.Microsecond, Jitter: 120 * time.Microsecond})
	s.record(probe.UDPStats{Received: 197, Sent: 200, LossPct: 1.5, AvgRTT: 2 * time.Millisecond, Jitter: 600 * time.Microsecond})

	rtt, jitter, loss, episodes := s.read()
	if rtt != 2.0 { // latest burst AvgRTT in ms
		t.Fatalf("rtt = %v, want 2.0", rtt)
	}
	if jitter < 0.59 || jitter > 0.61 { // latest Jitter ~0.6 ms
		t.Fatalf("jitter = %v, want ~0.6", jitter)
	}
	if loss != 1.5 {
		t.Fatalf("loss = %v, want 1.5", loss)
	}
	if episodes != 1 { // only the second burst had loss
		t.Fatalf("episodes = %d, want 1", episodes)
	}
}

func TestUDPStatEmpty(t *testing.T) {
	s := &udpStat{}
	rtt, jitter, loss, episodes := s.read()
	if rtt != 0 || jitter != 0 || loss != 0 || episodes != 0 {
		t.Fatalf("expected zeroes, got %v/%v/%v/%d", rtt, jitter, loss, episodes)
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/appcore/ -run TestUDPStat -v` → FAIL (undefined: udpStat).

- [ ] **Step 3: Implement `internal/appcore/udpstat.go`**

```go
package appcore

import (
	"sync"

	"netlogger/internal/probe"
)

// udpStat aggregates high-rate UDP probe bursts for one peer: the latest burst's
// RTT/jitter/loss plus a cumulative count of bursts that saw any loss (micro-drop
// episodes — the signal that survives across a long session).
type udpStat struct {
	mu        sync.Mutex
	lastRTTms float64
	jitterMs  float64
	lossPct   float64
	episodes  int
	bursts    int
}

// record folds one probe burst into the stat.
func (s *udpStat) record(st probe.UDPStats) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastRTTms = float64(st.AvgRTT.Microseconds()) / 1000.0
	s.jitterMs = float64(st.Jitter.Microseconds()) / 1000.0
	s.lossPct = st.LossPct
	s.bursts++
	if st.LossPct > 0 {
		s.episodes++
	}
}

// read returns latest RTT (ms), jitter (ms), loss (%), and cumulative episode count.
func (s *udpStat) read() (rttms, jitterms, lossPct float64, episodes int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastRTTms, s.jitterMs, s.lossPct, s.episodes
}
```

- [ ] **Step 4: Run to verify pass** — `go test ./internal/appcore/ -run TestUDPStat -v` → PASS. `gofmt -w internal/appcore/ && go vet ./internal/appcore/`.

- [ ] **Step 5: Commit** — `git add internal/appcore/udpstat.go internal/appcore/udpstat_test.go && git commit -m "feat(appcore): UDP burst aggregator (jitter + micro-drop episodes) (N3d)"` (footer).

---

## Task 2: UDP echo responder + high-rate probe loop in `appcore`

**Files:** Modify `internal/appcore/appcore.go`, add a test to `internal/appcore/appcore_test.go`.

- [ ] **Step 1: Write the failing test (append to `internal/appcore/appcore_test.go`)**

```go
func TestSnapshotShowsUDPJitterAndLoss(t *testing.T) {
	dir := t.TempDir()
	a := New(dir)
	a.Ping = func(string, time.Duration) (probe.Result, error) {
		return probe.Result{RTT: time.Millisecond}, nil
	}
	a.StartIperf = func(string) (func(), string) { return func() {}, "" }
	// Deterministic UDP seam: report jitter + some loss without real sockets.
	a.ProbeUDP = func(string, int, time.Duration, time.Duration) (probe.UDPStats, error) {
		return probe.UDPStats{Sent: 200, Received: 198, LossPct: 1.0, AvgRTT: time.Millisecond, Jitter: 500 * time.Microsecond}, nil
	}
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
		if len(ps) == 1 && ps[0].JitterMs > 0 {
			if ps[0].UDPLossPct != 1.0 {
				t.Fatalf("UDP loss = %v, want 1.0", ps[0].UDPLossPct)
			}
			if ps[0].DropEpisodes < 1 {
				t.Fatalf("expected >=1 drop episode, got %d", ps[0].DropEpisodes)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("UDP jitter not populated; got %+v", a.Snapshot().Peers)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/appcore/ -run TestSnapshotShowsUDP -v` → FAIL.

- [ ] **Step 3: Modify `internal/appcore/appcore.go`**

3a. Add UDP constants to the existing `const (...)` block (next to `controlPort`):
```go
	udpEchoPort   = 8089
	udpProbeCount = 200
)

var (
	udpProbeInterval = 5 * time.Millisecond
	udpProbeTimeout  = 200 * time.Millisecond
)
```
(Place the `var` block right after the `const` block.)

3b. Add to `PeerInfo`:
```go
	JitterMs     float64
	UDPLossPct   float64
	DropEpisodes int
```

3c. Add the `ProbeUDP` seam to `App` (in the seam group near `Ping`/`StartIperf`):
```go
	ProbeUDP func(target string, count int, interval, timeout time.Duration) (probe.UDPStats, error)
```

3d. Add to `App` (near `peerStats`):
```go
	udpEcho  *probe.UDPEcho
	udpStats map[string]*udpStat
```

3e. In `New`, default the seam + init the map (add to the `&App{...}` literal):
```go
		ProbeUDP:   probe.ProbeUDP,
		udpStats:   make(map[string]*udpStat),
```

3f. Add a get-or-create helper:
```go
func (a *App) udpStatFor(id string) *udpStat {
	a.peerMu.Lock()
	defer a.peerMu.Unlock()
	s := a.udpStats[id]
	if s == nil {
		s = &udpStat{}
		a.udpStats[id] = s
	}
	return s
}
```

3g. In `Start`, after the discovery block and before launching the loops, start the echo responder (only on the real path — when not injected). Add:
```go
	if e, err := probe.StartUDPEcho("0.0.0.0:" + strconv.Itoa(udpEchoPort)); err != nil {
		log.Printf("udp echo start: %v", err)
	} else {
		a.udpEcho = e
	}
```
(`strconv` is already imported via other files? It is NOT necessarily imported in appcore.go — add `"strconv"` to the import block.)

3h. Launch the UDP loop. Change `a.wg.Add(2)` (currently for probeLoop + peerLoop) to `a.wg.Add(3)` and add `go a.udpLoop(ctx)`.

3i. Add the loop:
```go
// udpLoop runs a high-rate isochronous UDP burst against each discovered peer's
// echo responder, capturing jitter + micro-drop episodes that 1 Hz ICMP misses.
func (a *App) udpLoop(ctx context.Context) {
	defer a.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		a.mu.Lock()
		disc := a.Discovery
		a.mu.Unlock()
		if disc == nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(a.tick):
			}
			continue
		}
		peers := disc.Peers()
		if len(peers) == 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(a.tick):
			}
			continue
		}
		for _, p := range peers {
			if ctx.Err() != nil {
				return
			}
			host := p.Addr
			if h, _, err := net.SplitHostPort(p.Addr); err == nil {
				host = h
			}
			target := net.JoinHostPort(host, strconv.Itoa(udpEchoPort))
			st, err := a.ProbeUDP(target, udpProbeCount, udpProbeInterval, udpProbeTimeout)
			if err == nil {
				a.udpStatFor(p.ID).record(st)
			}
		}
	}
}
```
Note: the burst itself paces the loop (~1 s/peer), so no extra sleep is needed when peers exist. The injected test seam returns instantly, so the loop spins fast — that's fine for the test (it just needs the stat populated); in production `ProbeUDP` blocks ~1 s per burst.

3j. In `Snapshot`, enrich each peer with UDP stats. In the loop building `peers`, after reading the ICMP `rtt, loss`, also read UDP and include the fields:
```go
		for _, p := range a.Discovery.Peers() {
			rtt, loss := a.statFor(p.ID).read()
			urtt, jitter, uloss, episodes := a.udpStatFor(p.ID).read()
			_ = urtt // UDP RTT available if needed; ICMP RTT shown as primary
			peers = append(peers, PeerInfo{
				ID: p.ID, Host: p.Host, Addr: p.Addr, Version: p.Version,
				LastSeenUnix: p.LastSeen.Unix(), RTTms: rtt, LossPct: loss,
				JitterMs: jitter, UDPLossPct: uloss, DropEpisodes: episodes,
			})
		}
```

3k. In `Stop` (inside `stopOnce.Do`), close the echo responder before closing the store (after stopping discovery):
```go
		if a.udpEcho != nil {
			_ = a.udpEcho.Close()
		}
```

- [ ] **Step 4: Run to verify pass** — `go test ./internal/appcore/ -v` → ALL PASS (incl new test). `go vet ./internal/appcore/ && gofmt -w internal/appcore/ && go build ./...`.

- [ ] **Step 5: Commit** — `git add internal/appcore/ && git commit -m "feat(appcore): UDP echo responder + high-rate peer probe (jitter/micro-drops) (N3d)"`.

---

## Task 3: Show jitter + UDP loss + episodes in the UI

**Files:** Modify `internal/ui/ui.go` (manually verified).

- [ ] **Step 1: Update the peer-row format** in `layoutStatus` to include the high-fidelity metrics:

```go
	for _, p := range s.Peers {
		rows = append(rows, fmt.Sprintf("   - %-12s %-20s  RTT %.2f ms  jitter %.2f ms  loss %.1f%%  drops %d",
			peerName(p), p.Addr, p.RTTms, p.JitterMs, p.UDPLossPct, p.DropEpisodes))
	}
```

- [ ] **Step 2: Verify it compiles** — `go build ./internal/ui/ && gofmt -w internal/ui/ && go vet ./internal/ui/`.

- [ ] **Step 3: Commit** — `git add internal/ui/ && git commit -m "feat(ui): show UDP jitter, loss, and micro-drop episodes per peer (N3d)"`.

---

## Task 4: Build + two-machine stress verification (acceptance gate)

**Files:** none.

- [ ] **Step 1: Rebuild** — `powershell -ExecutionPolicy Bypass -File scripts/build-app.ps1` → `bin/NetLogger.exe`. Do NOT launch from automation.

- [ ] **Step 2: Manual (human runs):**
1. Close any running NetLogger (window X = clean shutdown), launch the new `bin\NetLogger.exe` on **both** machines.
2. Idle: each peer row shows low jitter (sub-ms), 0% UDP loss, `drops 0`.
3. **Reproduce the stutter** — start the Moonlight stream Ryzen→ProjectorPC (the real workload). Watch the affected peer row: **jitter climbing** and/or **`drops` incrementing** marks a caught micro-drop episode — exactly the events 1 Hz ICMP missed.
4. Note which direction/box shows the jitter/drops (asymmetry points at the culprit NIC vs the shared switch).

- [ ] **Step 3:** On human confirmation, N3d is complete and ready to merge.

---

## Self-Review

**Spec coverage (N3d slice):**
- High-rate isochronous UDP probing → Task 2 (`udpLoop` + `probe.ProbeUDP` at 200 Hz). ✓
- Peer-side responder → Task 2 (`probe.StartUDPEcho`). ✓
- Jitter + micro-drop-episode capture (the failure-mode signal) → Task 1 (`udpStat`) + Task 2 (`Snapshot`) + Task 3 (UI). ✓
- Firewall already covers inbound UDP-to-process (no new work) — noted in Architecture. ✓

Deferred (later milestones): persisting UDP samples to the store for offline analysis (the burst stats are in-memory for now), the sync API + sample pull (N-future), the Link Matrix UI (N2), and the synchronized full-mesh load round (N4). N3d makes the live per-link metrics *sensitive enough to catch the fault*; aggregation/visualization follows.

**Placeholder scan:** none — complete code + exact commands in every step.

**Type consistency:** `udpStat.record(probe.UDPStats)`, `udpStat.read() (float64,float64,float64,int)`, `App.ProbeUDP func(string,int,time.Duration,time.Duration)(probe.UDPStats,error)`, `App.udpStats map[string]*udpStat`, `App.udpStatFor(string)*udpStat`, `App.udpEcho *probe.UDPEcho`, `App.udpLoop(context.Context)`, `PeerInfo{...,JitterMs,UDPLossPct float64,DropEpisodes int}`. `probe.StartUDPEcho/ProbeUDP/UDPStats/UDPEcho` match the engine. UI references the new `PeerInfo` fields. `strconv`/`net` imports added to appcore.
