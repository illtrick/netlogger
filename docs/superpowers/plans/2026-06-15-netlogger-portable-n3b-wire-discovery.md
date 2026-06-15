# NetLogger Portable — N3b: Wire Discovery into the App — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Wire the proven `discovery` package into the running app so that launching `NetLogger.exe` on two+ machines makes each instance **show the others in a live peer list** — the "watch auto-discovery happen" moment. Includes a program-scoped inbound firewall rule (so multicast/sync traffic is allowed) and `Snapshot.Peers` feeding a UI peer panel.

**Architecture:** A new `internal/firewall` package adds a program-path inbound allow rule (Windows `netsh`, no-op elsewhere). `appcore` gains a `PeerLister` seam (the real `discovery.Service`, injectable for tests): on `Start` it derives the node UUID + hostname, opens the firewall, starts discovery announcing its control port, and exposes discovered peers via `Snapshot.Peers`. The Gio UI renders the peer list. Cross-machine link probing + the in-process sync API are explicitly deferred to N3c.

**Tech Stack:** Go (cgo-free), existing `internal/discovery`, `internal/identity`, `internal/version`, Gio.

Reference spec: `docs/superpowers/specs/2026-06-15-netlogger-portable-design.md` §3.1–3.2, §6 (Peers screen).

Design constants (define in `appcore`): control port `8088`, discovery group `239.255.74.76`, discovery port `48076` (must match the `discovery` package test values).

---

## File Structure

| Path | Responsibility |
|---|---|
| `internal/firewall/firewall_windows.go` | `AllowProgram(ruleName)` — idempotent inbound allow for this exe (covers all its dynamic ports). |
| `internal/firewall/firewall_other.go` | No-op `AllowProgram` for non-Windows. |
| `internal/firewall/firewall_windows_test.go` | Test that `AllowProgram` runs without error (best-effort; returns nil even unelevated). |
| `internal/appcore/appcore.go` | Modify: derive identity + host, open firewall, start/stop discovery, expose `Snapshot.Peers` via a `PeerLister` seam. |
| `internal/appcore/appcore_test.go` | Add: injected-`PeerLister` test that `Snapshot.Peers` reflects discovered peers. |
| `internal/ui/ui.go` | Modify: render the peer list below the status panel. |

---

## Task 1: `internal/firewall` — program-path inbound allow

**Files:** Create `internal/firewall/firewall_windows.go`, `internal/firewall/firewall_other.go`, `internal/firewall/firewall_windows_test.go`.

**Context:** The elevated app should allow its own inbound traffic (multicast discovery + the future sync API + iperf) with one program-scoped rule, per the research recommendation. `netsh` is delete-then-add for idempotency, child window hidden, best-effort (returns nil even when unelevated so callers don't treat it as fatal).

- [ ] **Step 1: Write the failing test `internal/firewall/firewall_windows_test.go`**

```go
//go:build windows

package firewall

import "testing"

func TestAllowProgramBestEffort(t *testing.T) {
	// Best-effort: must not return an error even when not elevated (so callers
	// can ignore the result). It either adds the rule or silently fails.
	if err := AllowProgram("NetLoggerTestRule"); err != nil {
		t.Fatalf("AllowProgram should be best-effort nil, got %v", err)
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/firewall/ -v` → FAIL (undefined: AllowProgram).

- [ ] **Step 3: Implement `internal/firewall/firewall_windows.go`**

```go
//go:build windows

// Package firewall adds a program-scoped inbound Windows Firewall allow rule for
// the running executable, so its dynamic ports (discovery, sync API, iperf) are
// reachable. Best-effort: only effective when elevated.
package firewall

import (
	"os"
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000

func hidden(c *exec.Cmd) *exec.Cmd {
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	return c
}

// AllowProgram adds (delete-then-add, idempotent) an inbound allow rule for this
// executable. Returns nil regardless of netsh success so callers can ignore it.
func AllowProgram(ruleName string) error {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	_ = hidden(exec.Command("netsh", "advfirewall", "firewall", "delete", "rule", "name="+ruleName)).Run()
	_ = hidden(exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
		"name="+ruleName, "dir=in", "action=allow", "program="+exe, "enable=yes", "profile=any")).Run()
	return nil
}
```

- [ ] **Step 4: Implement `internal/firewall/firewall_other.go`**

```go
//go:build !windows

package firewall

// AllowProgram is a no-op on non-Windows builds.
func AllowProgram(ruleName string) error { return nil }
```

- [ ] **Step 5: Run to verify pass** — `go test ./internal/firewall/ -v` → PASS. `gofmt -w internal/firewall/ && go vet ./internal/firewall/`.

- [ ] **Step 6: Commit** — `git add internal/firewall/ && git commit -m "feat(firewall): program-scoped inbound allow rule (N3b)"` (with footer).

---

## Task 2: Wire discovery into `appcore` + `Snapshot.Peers`

**Files:** Modify `internal/appcore/appcore.go`, add a test to `internal/appcore/appcore_test.go`.

- [ ] **Step 1: Write the failing test (append to `internal/appcore/appcore_test.go`)**

```go
import "netlogger/internal/discovery" // add to the existing import block

type fakeLister struct{ peers []discovery.Peer }

func (f fakeLister) Peers() []discovery.Peer { return f.peers }

func TestSnapshotExposesDiscoveredPeers(t *testing.T) {
	dir := t.TempDir()
	a := New(dir)
	a.Ping = func(string, time.Duration) (probe.Result, error) {
		return probe.Result{RTT: time.Millisecond}, nil
	}
	a.StartIperf = func(string) (func(), string) { return func() {}, "" }
	a.Discovery = fakeLister{peers: []discovery.Peer{
		{ID: "p1", Host: "h1", Addr: "10.0.0.1:8088", Version: "v"},
	}}
	a.tick = 5 * time.Millisecond
	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()
	snap := a.Snapshot()
	if len(snap.Peers) != 1 || snap.Peers[0].ID != "p1" || snap.Peers[0].Addr != "10.0.0.1:8088" {
		t.Fatalf("expected discovered peer in snapshot, got %+v", snap.Peers)
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/appcore/ -run TestSnapshotExposesDiscoveredPeers -v` → FAIL (undefined: App.Discovery / Snapshot.Peers).

- [ ] **Step 3: Modify `internal/appcore/appcore.go`**

3a. Add imports (merge into the existing import block):
```go
	"log"
	"os"

	"netlogger/internal/discovery"
	"netlogger/internal/firewall"
	"netlogger/internal/identity"
	"netlogger/internal/version"
```
(`log` is already imported from the N1 review fix; keep one copy. `os` may be new.)

3b. Add constants after the existing `const recentWindow = 60`:
```go
const (
	controlPort    = 8088
	discoveryGroup = "239.255.74.76"
	discoveryPort  = 48076
)
```

3c. Add the `PeerLister` interface + `PeerInfo` type, and extend `Snapshot`:
```go
// PeerLister is the discovery source the UI reads peers through.
type PeerLister interface {
	Peers() []discovery.Peer
}

// PeerInfo is a discovered peer as exposed to the UI.
type PeerInfo struct {
	ID           string
	Host         string
	Addr         string
	Version      string
	LastSeenUnix int64
}
```
Add to `Snapshot`:
```go
	Peers []PeerInfo
```

3d. Add fields to `App` (after `startedAt time.Time`):
```go
	// Discovery is the peer source; if nil, Start creates a real discovery.Service.
	Discovery PeerLister
	disc      *discovery.Service
	nodeID    string
	host      string
```

3e. In `Start`, after the iperf/store setup block and before launching the probe loop, add discovery startup:
```go
	a.nodeID, _ = identity.NodeID(a.dataDir)
	a.host, _ = os.Hostname()
	if a.Discovery == nil {
		_ = firewall.AllowProgram("NetLogger")
		svc := discovery.New(discovery.Config{
			SelfID: a.nodeID, Host: a.host, ControlPort: controlPort, Version: version.Version,
			Group: discoveryGroup, Port: discoveryPort,
		})
		if err := svc.Start(); err != nil {
			log.Printf("discovery start: %v", err)
		} else {
			a.disc = svc
			a.Discovery = svc
		}
	}
```

3f. In `Snapshot`, build the peer list (read `a.Discovery`, which is set before the goroutine and only read here — safe; for strictness it can be read under `a.mu`, but it is assigned once in Start before any concurrent Snapshot). Add before the `return Snapshot{...}`:
```go
	var peers []PeerInfo
	if a.Discovery != nil {
		for _, p := range a.Discovery.Peers() {
			peers = append(peers, PeerInfo{
				ID: p.ID, Host: p.Host, Addr: p.Addr, Version: p.Version,
				LastSeenUnix: p.LastSeen.Unix(),
			})
		}
	}
```
And add `Peers: peers,` to the returned `Snapshot{...}` literal.

> Note: `a.Discovery` is read in `Snapshot` (UI goroutine) and assigned in `Start`. Assign it under `a.mu` in Start (extend the existing locked block that sets `a.store`/`a.iperfStop`) and read it under `a.mu` in Snapshot, OR — simplest and matching N1's resolved pattern — assign `a.Discovery`, `a.disc`, `a.nodeID`, `a.host` inside the same `a.mu.Lock()` block already used in Start for startup state, and read `a.Discovery` at the top of `Snapshot` under the lock into a local before iterating. Implement the locked version to stay race-free.

3g. In `Stop` (inside the `stopOnce.Do`), stop discovery before closing the store:
```go
		if a.disc != nil {
			_ = a.disc.Stop()
		}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/appcore/ -v`
Expected: all appcore tests PASS, including the new `TestSnapshotExposesDiscoveredPeers`. (The injected `fakeLister` means Start skips the real discovery service and the firewall call.)

- [ ] **Step 5: vet + fmt + full build**

Run: `go vet ./internal/appcore/ && gofmt -l internal/appcore/ && go build ./...`
Expected: vet clean, gofmt prints nothing (ignore CRLF-only noise), build OK.

- [ ] **Step 6: Commit** — `git add internal/appcore/ && git commit -m "feat(appcore): start discovery + expose discovered peers in Snapshot (N3b)"`.

---

## Task 3: Render the peer list in the UI

**Files:** Modify `internal/ui/ui.go` (manually verified — no unit test).

- [ ] **Step 1: Modify `layoutStatus` in `internal/ui/ui.go`** to append peer rows after the existing status rows. Replace the `rows := []string{...}` construction and its single Flex with a version that adds a "Peers" section:

```go
func layoutStatus(gtx layout.Context, th *material.Theme, s appcore.Snapshot) layout.Dimensions {
	rows := []string{
		"NetLogger — portable diagnostic agent",
		"",
		fmt.Sprintf("Data dir:      %s", s.DataDir),
		fmt.Sprintf("Database:      %s", s.DBPath),
		fmt.Sprintf("iperf3:        %s (server %s)", versionOr(s.Iperf3Version), upDown(s.Iperf3ServerUp)),
		fmt.Sprintf("Self-probe:    %d samples, last RTT %.2f ms, loss %.1f%%", s.Samples, s.LastRTTms, s.LossPct),
		"",
		fmt.Sprintf("Discovered peers (%d):", len(s.Peers)),
	}
	if len(s.Peers) == 0 {
		rows = append(rows, "   (none yet — launch NetLogger on another machine on this LAN)")
	}
	for _, p := range s.Peers {
		rows = append(rows, fmt.Sprintf("   • %s   %s   %s", peerName(p), p.Addr, versionOr(p.Version)))
	}
	return layout.UniformInset(unit.Dp(20)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, flexChildren(th, rows)...)
	})
}

func peerName(p appcore.PeerInfo) string {
	if p.Host != "" {
		return p.Host
	}
	return p.ID
}
```

- [ ] **Step 2: Verify it compiles** — `go build ./internal/ui/ && gofmt -w internal/ui/ && go vet ./internal/ui/`.

- [ ] **Step 3: Commit** — `git add internal/ui/ && git commit -m "feat(ui): show discovered peers list in the window (N3b)"`.

---

## Task 4: Build + manual two-machine verification (acceptance gate)

**Files:** none (build + manual).

- [ ] **Step 1: Rebuild the elevated app** — `powershell -ExecutionPolicy Bypass -File scripts/build-app.ps1` → `bin/NetLogger.exe` produced. Do NOT launch from automation.

- [ ] **Step 2: Manual verification (the human runs this):**
1. Copy/launch `bin\NetLogger.exe` on **two** machines on the same LAN (approve UAC + the Windows Firewall prompt if shown).
2. Within a few seconds, each window's **"Discovered peers"** count goes to ≥1 and lists the other machine (hostname + its `IP:8088` + version).
3. Close one app → after ~12 s (TTL) it drops off the other's peer list (or immediately on the graceful bye).
4. No console windows appear; both apps stay in the taskbar.

- [ ] **Step 3: Commit** (if any doc/notes) — otherwise N3b is complete and ready to merge once the human confirms discovery works across the two machines.

---

## Self-Review

**Spec coverage (N3b slice):**
- Discovery wired into the app, announcing the control port → Task 2. ✓
- Program-scoped firewall so inbound discovery is allowed when elevated → Task 1 + Task 2 (called on the real path). ✓
- `Snapshot.Peers` + UI peer list ("watch auto-discovery happen") → Task 2 + Task 3. ✓
- Persistent identity used for the announce → Task 2 (`identity.NodeID`). ✓
- Clean discovery shutdown on quit → Task 2 (Stop). ✓

Deferred to N3c (explicitly out of N3b): the in-process HTTP sync API server (`mesh.AgentAPI`), cross-machine probe/pull/clock-offset (so peers measure each other's links), `Snapshot` per-peer loss/RTT, manual add-by-IP, and the Link Matrix data. N3b only makes peers *visible*; N3c makes them *measured*.

**Placeholder scan:** none — every step shows complete code or an exact command.

**Type consistency:** `firewall.AllowProgram(string) error`; `appcore.PeerLister{ Peers() []discovery.Peer }`; `appcore.PeerInfo{ID,Host,Addr,Version string; LastSeenUnix int64}`; `Snapshot.Peers []PeerInfo`; `App.Discovery PeerLister`, `App.disc *discovery.Service`. `discovery.New`, `discovery.Config{SelfID,Host,ControlPort,Version,Group,Port}`, `discovery.Service.Start()/Peers()/Stop()`, `discovery.Peer{ID,Host,Addr,Version,LastSeen}`, `identity.NodeID(dir)`, `version.Version` all match the merged packages. UI references `appcore.PeerInfo` fields consistently.
