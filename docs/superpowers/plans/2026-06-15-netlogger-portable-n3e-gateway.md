# NetLogger Portable — N3e: Gateway / Router Probing — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Widen coverage beyond the NetLogger peers: every instance auto-detects its **default gateway (router)** and probes it (ICMP), surfacing gateway RTT + loss in the window. This catches shared-router / uplink faults — the most likely cause of a LAN-side disconnect that the direct peer-to-peer link doesn't see — with **zero configuration**.

**Architecture:** A tiny `gateway` package wraps default-gateway discovery (the `jackpal/gateway` pure-Go library). `appcore` detects the gateway at startup and probes it inside the existing per-tick ICMP loop (reusing `peerStat`), exposing `GatewayIP/GatewayRTTms/GatewayLossPct` in `Snapshot`. The UI shows a gateway row. ICMP-only (the router doesn't run our app); 1 Hz is adequate because a disconnect-scale outage lasts seconds, unlike the sub-100 ms game-stutter the UDP probe targets.

**Tech Stack:** Go (cgo-free), `github.com/jackpal/gateway` (pure-Go default-gateway discovery), existing `internal/probe`, Gio.

Reference spec: `docs/superpowers/specs/2026-06-15-netlogger-portable-design.md` §3.2 (infrastructure coverage), §5 (per-component reachability).

---

## File Structure

| Path | Responsibility |
|---|---|
| `internal/gateway/gateway.go` | `Default()` — the machine's default gateway IP, or "". |
| `internal/gateway/gateway_test.go` | Test: returns a valid IP or empty (skip-friendly). |
| `internal/appcore/appcore.go` | Modify: detect gateway, probe it in the ICMP loop, `Snapshot` gateway fields. |
| `internal/appcore/appcore_test.go` | Add: injected-`Ping` test that `Snapshot` gateway fields populate. |
| `internal/ui/ui.go` | Modify: show a "Gateway" row. |

---

## Task 1: `internal/gateway` — default gateway discovery

**Files:** Create `internal/gateway/gateway.go`, `internal/gateway/gateway_test.go`. Add the `jackpal/gateway` dependency.

- [ ] **Step 1: Add the dependency**

Run:
```bash
cd "$HOME/.claude/netlogger"
go get github.com/jackpal/gateway@latest
```
Expected: `go.mod` gains `github.com/jackpal/gateway`.

- [ ] **Step 2: Write the test `internal/gateway/gateway_test.go`**

```go
package gateway

import (
	"net"
	"testing"
)

func TestDefaultReturnsIPOrEmpty(t *testing.T) {
	got := Default()
	if got == "" {
		t.Skip("no default gateway in this environment")
	}
	if net.ParseIP(got) == nil {
		t.Fatalf("Default() returned a non-IP: %q", got)
	}
}
```

- [ ] **Step 3: Run to verify it fails** — `go test ./internal/gateway/ -v` → FAIL (undefined: Default).

- [ ] **Step 4: Implement `internal/gateway/gateway.go`**

```go
// Package gateway discovers the machine's default gateway (router) IP, so the
// app can probe the shared-router path without any configuration.
package gateway

import "github.com/jackpal/gateway"

// Default returns the default gateway IP as a string, or "" if it can't be
// determined.
func Default() string {
	ip, err := gateway.DiscoverGateway()
	if err != nil || ip == nil {
		return ""
	}
	return ip.String()
}
```

- [ ] **Step 5: Run to verify pass** — `go test ./internal/gateway/ -v` → PASS (or Skip if no gateway). `gofmt -w internal/gateway/ && go vet ./internal/gateway/`.

- [ ] **Step 6: Commit** — `git add internal/gateway/ go.mod go.sum && git commit -m "feat(gateway): default-gateway discovery (N3e)"` (footer).

---

## Task 2: Probe the gateway in `appcore`

**Files:** Modify `internal/appcore/appcore.go`, add a test to `internal/appcore/appcore_test.go`.

- [ ] **Step 1: Write the failing test (append to `internal/appcore/appcore_test.go`)**

```go
func TestSnapshotShowsGateway(t *testing.T) {
	dir := t.TempDir()
	a := New(dir)
	a.Ping = func(addr string, _ time.Duration) (probe.Result, error) {
		return probe.Result{RTT: 3 * time.Millisecond}, nil
	}
	a.StartIperf = func(string) (func(), string) { return func() {}, "" }
	a.Discovery = fakeLister{} // no peers needed
	a.GatewayIP = "192.168.0.1" // inject so the test doesn't depend on the host
	a.tick = 5 * time.Millisecond
	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()
	deadline := time.Now().Add(2 * time.Second)
	for {
		s := a.Snapshot()
		if s.GatewayIP == "192.168.0.1" && s.GatewayRTTms > 0 {
			if s.GatewayLossPct != 0 {
				t.Fatalf("gateway loss = %v, want 0", s.GatewayLossPct)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("gateway not populated; got %+v", a.Snapshot())
		}
		time.Sleep(5 * time.Millisecond)
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/appcore/ -run TestSnapshotShowsGateway -v` → FAIL.

- [ ] **Step 3: Modify `internal/appcore/appcore.go`**

3a. Add the import: `"netlogger/internal/gateway"`.

3b. Add an exported field to `App` (near the other config fields, e.g., after `host`): 
```go
	// GatewayIP is the router to probe; auto-detected in Start when empty.
	GatewayIP string
```

3c. Add gateway fields to `Snapshot`:
```go
	GatewayIP      string
	GatewayRTTms   float64
	GatewayLossPct float64
```

3d. In `Start`, detect the gateway when not already set (do this on the real path or always; detection is cheap and harmless). Add right after `a.host, _ = os.Hostname()` (or wherever host is derived), under the same lock if convenient — simplest is before launching loops:
```go
	if a.GatewayIP == "" {
		a.GatewayIP = gateway.Default()
	}
```
(If you set it inside the locked startup block, read it consistently; since it's set once before the loops start and only read afterward, a plain assignment before `go a.peerLoop(ctx)` is fine.)

3e. In `peerLoop`, after the `for _, p := range disc.Peers()` loop body (still inside the `case <-t.C:`), probe the gateway too:
```go
			if a.GatewayIP != "" {
				res, err := a.Ping(a.GatewayIP, 2*time.Second)
				a.statFor("__gateway__").record(err == nil && !res.Lost,
					float64(res.RTT.Microseconds())/1000.0)
			}
```

3f. In `Snapshot`, after building the peer list and before returning, add the gateway fields. Read the gateway stat:
```go
	var gwRTT, gwLoss float64
	if a.GatewayIP != "" {
		gwRTT, gwLoss = a.statFor("__gateway__").read()
	}
```
and add to the returned `Snapshot{...}`:
```go
		GatewayIP: a.GatewayIP, GatewayRTTms: gwRTT, GatewayLossPct: gwLoss,
```
(`a.GatewayIP` is read here under `a.mu`; it is set once in Start before the loops/Snapshot run concurrently. For strict consistency you may read it into a local at the top of the `a.mu`-locked section, but a single assignment-before-use is race-free.)

- [ ] **Step 4: Run to verify pass** — `go test ./internal/appcore/ -v` → ALL PASS (incl new test). `go vet ./internal/appcore/ && gofmt -w internal/appcore/ && go build ./...`.

- [ ] **Step 5: Commit** — `git add internal/appcore/ && git commit -m "feat(appcore): auto-detect and probe the default gateway (N3e)"`.

---

## Task 3: Show the gateway in the UI

**Files:** Modify `internal/ui/ui.go` (manually verified).

- [ ] **Step 1: Add a gateway line** to `layoutStatus`, inserted between the self-probe row and the blank line before "Discovered peers". Find the `rows := []string{ ... "Self-probe: ..." , "", "Discovered peers (%d):" ... }` construction and add a gateway entry right after the self-probe line:

```go
		fmt.Sprintf("Self-probe:    %d samples, last RTT %.2f ms, loss %.1f%%", s.Samples, s.LastRTTms, s.LossPct),
		gatewayRow(s),
		"",
		fmt.Sprintf("Discovered peers (%d):", len(s.Peers)),
```

And add the helper:
```go
func gatewayRow(s appcore.Snapshot) string {
	if s.GatewayIP == "" {
		return "Gateway:       (not detected)"
	}
	return fmt.Sprintf("Gateway:       %s   RTT %.2f ms   loss %.1f%%", s.GatewayIP, s.GatewayRTTms, s.GatewayLossPct)
}
```

- [ ] **Step 2: Verify it compiles** — `go build ./internal/ui/ && gofmt -w internal/ui/ && go vet ./internal/ui/`.

- [ ] **Step 3: Commit** — `git add internal/ui/ && git commit -m "feat(ui): show default gateway RTT and loss (N3e)"`.

---

## Task 4: Build + manual verification

**Files:** none.

- [ ] **Step 1: Rebuild** — `powershell -ExecutionPolicy Bypass -File scripts/build-app.ps1` → `bin/NetLogger.exe`. Do NOT launch from automation.

- [ ] **Step 2: Manual (human runs):**
1. Close + relaunch `bin\NetLogger.exe` on each machine.
2. A **"Gateway: 192.168.0.x  RTT … ms  loss …%"** line appears, populated within a couple seconds.
3. Also launch NetLogger on the machine you RDP *from* so it joins the mesh (covers the RDP path at full UDP fidelity).
4. If a disconnect recurs, check whether **gateway loss** spikes (→ router/uplink fault) or whether a **specific peer's** jitter/drops spike (→ that machine's link/NIC).

- [ ] **Step 3:** On human confirmation, N3e is complete and ready to merge.

---

## Self-Review

**Spec coverage (N3e slice):**
- Auto-detect + probe the default gateway, zero-config → Task 1 + Task 2. ✓
- Gateway RTT/loss surfaced → Task 2 (`Snapshot`) + Task 3 (UI). ✓
- ICMP-only (router doesn't run our app), 1 Hz adequate for disconnect-scale events → noted in Architecture; reuses `peerStat` + `a.Ping`. ✓

Deferred (later): manual infra targets (modem/NAS by IP) + a GUI to add them; high-rate gateway probing; persisting gateway samples. N3e is the zero-config robust default; manual targets are the flexible follow-up.

**Placeholder scan:** none — complete code + exact commands.

**Type consistency:** `gateway.Default() string`; `App.GatewayIP string`; `Snapshot.GatewayIP string, GatewayRTTms, GatewayLossPct float64`; reuses `App.statFor(string) *peerStat` and `peerStat.read() (float64,float64)`. UI references `Snapshot.Gateway*` fields. The `"__gateway__"` key is namespaced so it can't collide with a peer UUID.
