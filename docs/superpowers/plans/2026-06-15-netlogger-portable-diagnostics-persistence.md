# NetLogger Portable — Diagnostics Persistence, Event Logging, Export & Synchronized Reset — Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Make captured diagnostics durable and shareable, and let any machine restart the whole mesh's logging session at once:
1. **Persist UDP bursts** (the drop episodes + jitter) to the store, so the high-fidelity data survives a close.
2. **Connectivity-event logging** — link degraded/recovered, peer joined/left, session boundaries — to both the log file and the `connectivity_events` table, so the log is a real timeline that distinguishes app restarts from network faults.
3. **One-click Export** — a self-contained JSON bundle (session + per-link stats + events + sample counts + NIC info) written beside the exe.
4. **Synchronized session reset** — a command served on the control port; a "Reset all" action POSTs it to every discovered peer + self, restarting every machine's logging session together.

**Architecture:** Backend in `appcore` (UDP store writes; a pure hysteresis `linkState` event detector wired to log + store; `ResetSession`/`ResetAll`; an `Export` builder). The existing `/api/links` mux gains an `/api/command` handler (httpauth-wrapped). New Gio buttons (`widget.Clickable`) for Reset-all and Export.

**Tech Stack:** Go (cgo-free), existing `store` (probe_samples, connectivity_events), `sysinfo` (NIC), `httpauth`, Gio.

Reference: spec §5–6; the earlier diagnostic finding (real intermittent ICMP loss ProjectorPC↔ryzen) motivates persisting UDP + events.

---

## File Structure

| Path | Responsibility |
|---|---|
| `internal/appcore/events.go` | `linkState` hysteresis detector (pure) + event recording helpers. |
| `internal/appcore/events_test.go` | Detector transition tests. |
| `internal/appcore/export.go` | `ExportBundle` type, `Export()` builder, `WriteExport`. |
| `internal/appcore/export_test.go` | Export builder + file-write tests. |
| `internal/appcore/command.go` | `commandHandler` + `postCommand` (reset gossip). |
| `internal/appcore/command_test.go` | Handler + client tests (httptest). |
| `internal/appcore/appcore.go` | UDP persistence, event wiring, `ResetSession`/`ResetAll`, serve `/api/command`. |
| `internal/appcore/appcore_test.go` | UDP-persist + reset snapshot tests. |
| `internal/ui/ui.go` | Reset-all + Export buttons (Clickable) + status line. |

---

## Task 1: Persist UDP bursts to the store

**Files:** Modify `internal/appcore/appcore.go`; add a test to `internal/appcore/appcore_test.go`.

**Context:** `udpLoop` currently records UDP bursts only in memory. Persist one summary row per burst as `probe_type="udp_iso"`, `Lost = (burst had any loss)`, `RTTus = AvgRTT`, `JitterUS = Jitter`. This durably records drop *episodes* + jitter + RTT (exact per-burst loss% is not stored — documented).

- [ ] **Step 1: Write the failing test (append to `internal/appcore/appcore_test.go`)**

```go
func TestUDPBurstsPersistedToStore(t *testing.T) {
	dir := t.TempDir()
	a := New(dir)
	a.Ping = func(string, time.Duration) (probe.Result, error) { return probe.Result{RTT: time.Millisecond}, nil }
	a.StartIperf = func(string) (func(), string) { return func() {}, "" }
	a.FetchLinks = func(string) (LinkReport, error) { return LinkReport{}, nil }
	a.ProbeUDP = func(string, int, time.Duration, time.Duration) (probe.UDPStats, error) {
		return probe.UDPStats{Sent: 200, Received: 190, LossPct: 5, AvgRTT: time.Millisecond, Jitter: 300 * time.Microsecond}, nil
	}
	a.Discovery = fakeLister{peers: []discovery.Peer{{ID: "p1", Host: "h1", Addr: "10.0.0.1:8088"}}}
	a.tick = 5 * time.Millisecond
	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// let a few bursts persist
	time.Sleep(120 * time.Millisecond)
	a.Stop()

	// reopen and count udp_iso rows with loss
	st, err := store.Open(a.dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st.Close()
	samples, err := st.Since(0, 100000)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	udpLost := 0
	for _, s := range samples {
		if s.ProbeType == "udp_iso" && s.DstHost == "p1" && s.Lost {
			udpLost++
		}
	}
	if udpLost == 0 {
		t.Fatalf("expected persisted udp_iso lost rows, got 0 (of %d samples)", len(samples))
	}
}
```
(Ensure `"netlogger/internal/store"` is imported in the test file.)

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/appcore/ -run TestUDPBurstsPersisted -v` → FAIL.

- [ ] **Step 3: In `udpLoop`, after `a.udpStatFor(p.ID).record(st)` and the history pushes, persist the burst:**

```go
				_, _ = a.store.Insert(store.Sample{
					TSUnixUS:  time.Now().UnixMicro(),
					ProbeType: "udp_iso",
					SrcHost:   a.nodeID,
					DstHost:   p.ID,
					Direction: "rtt",
					RTTus:     st.AvgRTT.Microseconds(),
					JitterUS:  st.Jitter.Microseconds(),
					Lost:      st.LossPct > 0,
				})
```
(`st` is the `probe.UDPStats` returned by `a.ProbeUDP`; ensure the variable name matches the loop. `a.nodeID` is read — it's set once in Start; safe.)

- [ ] **Step 4: Run to verify pass** — `go test ./internal/appcore/ -v` → ALL PASS. `go test -count=3 ./internal/appcore/` (no deadlock). `go vet`, `gofmt -w`, `go build ./...`.

- [ ] **Step 5: Commit** — `git add internal/appcore/ && git commit -m "feat(appcore): persist UDP bursts (drop episodes + jitter) to the store (diag)"`.

---

## Task 2: Connectivity-event detection + logging

**Files:** Create `internal/appcore/events.go`, `internal/appcore/events_test.go`; modify `appcore.go`.

- [ ] **Step 1: Write the failing test `internal/appcore/events_test.go`**

```go
package appcore

import "testing"

func TestLinkStateHysteresis(t *testing.T) {
	s := &linkState{}
	// one lossy sample: not yet degraded (needs 2 consecutive)
	if ch, _ := s.step(2.0); ch {
		t.Fatalf("should not flip on first lossy sample")
	}
	// second consecutive lossy: now degraded
	ch, deg := s.step(2.0)
	if !ch || !deg {
		t.Fatalf("expected degraded transition, ch=%v deg=%v", ch, deg)
	}
	// staying lossy: no further change
	if ch, _ := s.step(2.0); ch {
		t.Fatalf("no change while staying degraded")
	}
	// clean sample below exit threshold: recovers
	ch, deg = s.step(0.0)
	if !ch || deg {
		t.Fatalf("expected recovery, ch=%v deg=%v", ch, deg)
	}
}

func TestLinkStateIgnoresMinorLoss(t *testing.T) {
	s := &linkState{}
	if ch, _ := s.step(0.5); ch { // below enter threshold (1.0)
		t.Fatalf("0.5%% should not degrade")
	}
	if ch, _ := s.step(0.5); ch {
		t.Fatalf("still should not degrade")
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/appcore/ -run TestLinkState -v` → FAIL.

- [ ] **Step 3: Implement `internal/appcore/events.go`**

```go
package appcore

// Event hysteresis thresholds (UDP loss %).
const (
	degradeEnterPct = 1.0
	degradeExitPct  = 0.2
	degradeEnterN   = 2 // consecutive lossy samples to enter
)

// linkState tracks healthy/degraded for one link with hysteresis so a single
// stray lossy burst doesn't flap the event log.
type linkState struct {
	degraded bool
	hiCount  int
}

// step folds one loss% sample in and reports whether the state changed and the
// new degraded value.
func (s *linkState) step(lossPct float64) (changed bool, degraded bool) {
	if !s.degraded {
		if lossPct >= degradeEnterPct {
			s.hiCount++
			if s.hiCount >= degradeEnterN {
				s.degraded = true
				s.hiCount = 0
				return true, true
			}
		} else {
			s.hiCount = 0
		}
		return false, false
	}
	// currently degraded: recover when loss drops below the exit threshold
	if lossPct < degradeExitPct {
		s.degraded = false
		s.hiCount = 0
		return true, false
	}
	return false, true
}
```

- [ ] **Step 4: Run to verify pass** — `go test ./internal/appcore/ -run TestLinkState -v` → PASS.

- [ ] **Step 5: Wire events into `appcore.go`.** Add field `linkStates map[string]*linkState` to `App` (guard with `a.peerMu`); init in `New`. Add helper:
```go
func (a *App) recordEvent(online bool, detail string) {
	log.Printf("event: %s", detail)
	if a.store != nil {
		_ = a.store.InsertConnectivityEvent(time.Now().UnixMicro(), a.nodeID, online, detail)
	}
}
func (a *App) linkStateFor(id string) *linkState {
	a.peerMu.Lock()
	defer a.peerMu.Unlock()
	s := a.linkStates[id]
	if s == nil {
		s = &linkState{}
		a.linkStates[id] = s
	}
	return s
}
```
In `udpLoop`, after recording/persisting the burst, evaluate the event detector using the peer's hostname for readability (look up host from the discovered peer `p`):
```go
				if changed, degraded := a.linkStateFor(p.ID).step(st.LossPct); changed {
					if degraded {
						a.recordEvent(false, "link to "+peerLabel(p)+" degraded (loss "+strconv.FormatFloat(st.LossPct, 'f', 1, 64)+"%)")
					} else {
						a.recordEvent(true, "link to "+peerLabel(p)+" recovered")
					}
				}
```
Add a tiny helper (in events.go):
```go
import "netlogger/internal/discovery"

func peerLabel(p discovery.Peer) string {
	if p.Host != "" {
		return p.Host
	}
	return p.ID
}
```
Also record **peer joined** in `markSeen` when it creates a new entry — but `markSeen` holds `a.peerMu`; calling `recordEvent` (which touches the store, not peerMu) inside is OK, but to avoid holding the lock during the store write, have `markSeen` return whether it was new and let the caller log. Change `markSeen` to `(t time.Time, isNew bool)` and in `udpLoop` (where markSeen is already called) do:
```go
				if _, isNew := a.markSeen(p.ID); isNew {
					a.recordEvent(true, "peer "+peerLabel(p)+" joined")
				}
```
(Update the existing `markSeen` callers — in `udpLoop` and `Snapshot` — to the new 2-return signature; in `Snapshot` ignore `isNew`.)

- [ ] **Step 6: Verify** — `go test ./internal/appcore/ -v` → ALL PASS, `-count=3` clean, `go vet`, `gofmt -w`, `go build ./...`.

- [ ] **Step 7: Commit** — `git add internal/appcore/ && git commit -m "feat(appcore): connectivity-event detection (degrade/recover/join) to log + store (diag)"`.

---

## Task 3: `ResetSession` (local)

**Files:** Modify `appcore.go`; add a test to `appcore_test.go`.

- [ ] **Step 1: Write the failing test (append to `appcore_test.go`)**

```go
func TestResetSessionClearsState(t *testing.T) {
	dir := t.TempDir()
	a := New(dir)
	a.Ping = func(string, time.Duration) (probe.Result, error) { return probe.Result{RTT: time.Millisecond}, nil }
	a.StartIperf = func(string) (func(), string) { return func() {}, "" }
	a.FetchLinks = func(string) (LinkReport, error) { return LinkReport{}, nil }
	a.ProbeUDP = func(string, int, time.Duration, time.Duration) (probe.UDPStats, error) {
		return probe.UDPStats{Sent: 200, Received: 200, AvgRTT: time.Millisecond, Jitter: 100 * time.Microsecond}, nil
	}
	a.Discovery = fakeLister{peers: []discovery.Peer{{ID: "p1", Host: "h1", Addr: "10.0.0.1:8088"}}}
	a.tick = 5 * time.Millisecond
	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()
	// accumulate some samples
	deadline := time.Now().Add(time.Second)
	for a.Snapshot().Samples < 5 {
		if time.Now().After(deadline) {
			t.Fatal("no samples accumulated")
		}
		time.Sleep(5 * time.Millisecond)
	}
	a.ResetSession()
	s := a.Snapshot()
	if s.Samples > 3 { // should be reset to ~0 (a tick or two may have re-added)
		t.Fatalf("expected samples reset near 0, got %d", s.Samples)
	}
	if s.SessionUptimeSec > 2 {
		t.Fatalf("expected uptime reset, got %d", s.SessionUptimeSec)
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/appcore/ -run TestResetSession -v` → FAIL.

- [ ] **Step 3: Implement `ResetSession` in `appcore.go`**

```go
// ResetSession clears all in-memory diagnostics and restarts the session clock,
// so the UI/charts begin fresh. Persisted samples remain on disk (timestamped).
func (a *App) ResetSession() {
	a.peerMu.Lock()
	a.peerStats = make(map[string]*peerStat)
	a.udpStats = make(map[string]*udpStat)
	a.rttHist = make(map[string]*histRing)
	a.lossHist = make(map[string]*histRing)
	a.firstSeen = make(map[string]time.Time)
	a.linkStates = make(map[string]*linkState)
	a.gwHist = newHistRing(histLen)
	a.netHist = newHistRing(histLen)
	a.peerMu.Unlock()

	a.mu.Lock()
	a.startedAt = time.Now()
	a.recent = nil
	a.samples = 0
	a.lastRTTms = 0
	a.mu.Unlock()

	a.reportMu.Lock()
	a.peerReports = make(map[string]LinkReport)
	a.reportMu.Unlock()

	a.recordEvent(true, "session reset")
}
```

- [ ] **Step 4: Verify** — `go test ./internal/appcore/ -v` → ALL PASS, `-count=3` clean, `go vet`, `gofmt -w`, `go build ./...`.

- [ ] **Step 5: Commit** — `git add internal/appcore/ && git commit -m "feat(appcore): ResetSession clears in-memory diagnostics + restarts clock (diag)"`.

---

## Task 4: Synchronized reset — `/api/command` + `ResetAll`

**Files:** Create `internal/appcore/command.go`, `internal/appcore/command_test.go`; modify `appcore.go`.

- [ ] **Step 1: Write the failing test `internal/appcore/command_test.go`**

```go
package appcore

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCommandHandlerInvokesReset(t *testing.T) {
	called := 0
	mux := http.NewServeMux()
	mux.Handle("/api/command", commandHandler(func() { called++ }))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if err := postCommand(http.DefaultClient, srv.URL, "reset"); err != nil {
		t.Fatalf("postCommand: %v", err)
	}
	if called != 1 {
		t.Fatalf("reset called %d times, want 1", called)
	}
}

func TestCommandHandlerIgnoresUnknown(t *testing.T) {
	called := 0
	mux := http.NewServeMux()
	mux.Handle("/api/command", commandHandler(func() { called++ }))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	_ = postCommand(http.DefaultClient, srv.URL, "frobnicate")
	if called != 0 {
		t.Fatalf("unknown command should not reset")
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/appcore/ -run TestCommandHandler -v` → FAIL.

- [ ] **Step 3: Implement `internal/appcore/command.go`**

```go
package appcore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

type command struct {
	Cmd string `json:"cmd"`
}

// commandHandler accepts POST {"cmd":"reset"} and invokes reset.
func commandHandler(reset func()) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var c command
		_ = json.NewDecoder(r.Body).Decode(&c)
		if c.Cmd == "reset" {
			reset()
		}
		w.WriteHeader(http.StatusOK)
	}
}

// postCommand POSTs a command to a peer's /api/command.
func postCommand(client *http.Client, baseURL, cmd string) error {
	body, _ := json.Marshal(command{Cmd: cmd})
	resp, err := client.Post(baseURL+"/api/command", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("command: status %d", resp.StatusCode)
	}
	return nil
}
```

- [ ] **Step 4: Run to verify pass** — `go test ./internal/appcore/ -run TestCommandHandler -v` → PASS.

- [ ] **Step 5: Wire into `appcore.go`.** In `Start`, where the `/api/links` mux is built (the `if a.disc != nil { mux := ...; mux.Handle("/api/links", ...) ...}` block), also register the command route:
```go
		mux.Handle("/api/command", commandHandler(a.ResetSession))
```
Add the `ResetAll` method:
```go
// ResetAll restarts the logging session on every discovered peer and on this
// machine, so the whole mesh begins a fresh session together.
func (a *App) ResetAll() {
	a.mu.Lock()
	disc := a.Discovery
	a.mu.Unlock()
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	if disc != nil {
		for _, p := range disc.Peers() {
			_ = postCommand(client, "http://"+p.Addr, "reset")
		}
	}
	a.ResetSession()
}
```

- [ ] **Step 6: Verify** — `go test ./internal/appcore/ -v` → ALL PASS, `-count=3` clean, `go vet`, `gofmt -w`, `go build ./...`.

- [ ] **Step 7: Commit** — `git add internal/appcore/ && git commit -m "feat(appcore): /api/command + ResetAll for synchronized mesh-wide session reset (diag)"`.

---

## Task 5: One-click Export bundle

**Files:** Create `internal/appcore/export.go`, `internal/appcore/export_test.go`.

- [ ] **Step 1: Write the failing test `internal/appcore/export_test.go`**

```go
package appcore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestExportBundleAndWrite(t *testing.T) {
	b := ExportBundle{
		GeneratedUnix:    100,
		NodeID:           "n1",
		Host:             "hostA",
		SessionUptimeSec: 42,
		Peers: []PeerInfo{
			{ID: "p1", Host: "h1", UDPLossPct: 1.0, DropEpisodes: 5},
		},
	}
	path, err := WriteExport(t.TempDir(), b)
	if err != nil {
		t.Fatalf("WriteExport: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var got ExportBundle
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.NodeID != "n1" || len(got.Peers) != 1 || got.Peers[0].DropEpisodes != 5 {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
	if filepath.Ext(path) != ".json" {
		t.Fatalf("expected .json file, got %s", path)
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/appcore/ -run TestExportBundle -v` → FAIL.

- [ ] **Step 3: Implement `internal/appcore/export.go`**

```go
package appcore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"netlogger/internal/store"
	"netlogger/internal/sysinfo"
)

// ExportBundle is a self-contained diagnostic snapshot for off-box analysis.
type ExportBundle struct {
	GeneratedUnix    int64             `json:"generated_unix"`
	NodeID           string            `json:"node_id"`
	Host             string            `json:"host"`
	SessionUptimeSec int64             `json:"session_uptime_sec"`
	GatewayIP        string            `json:"gateway_ip"`
	InternetIP       string            `json:"internet_ip"`
	Peers            []PeerInfo        `json:"peers"`
	Matrix           []MatrixCell      `json:"matrix"`
	Events           []store.ConnEvent `json:"events"`
	NICs             []sysinfo.NIC     `json:"nics"`
	SampleCount      int               `json:"sample_count"`
}

// Export builds a bundle from the current snapshot + store. unixNow is injected
// (the app passes time.Now().Unix()) so tests are deterministic.
func (a *App) Export(unixNow int64) ExportBundle {
	snap := a.Snapshot()
	var cells []MatrixCell
	for _, src := range snap.Matrix.Nodes {
		for _, dst := range snap.Matrix.Nodes {
			if c, ok := snap.Matrix.Cell(src.ID, dst.ID); ok {
				cells = append(cells, c)
			}
		}
	}
	var events []store.ConnEvent
	var count int
	if a.store != nil {
		events, _ = a.store.ConnectivityEvents(a.NodeID())
		if ss, err := a.store.Since(0, 1000000); err == nil {
			count = len(ss)
		}
	}
	return ExportBundle{
		GeneratedUnix:    unixNow,
		NodeID:           a.NodeID(),
		Host:             snap.GatewayIP, // placeholder overwritten below
		SessionUptimeSec: snap.SessionUptimeSec,
		GatewayIP:        snap.GatewayIP,
		InternetIP:       snap.InternetIP,
		Peers:            snap.Peers,
		Matrix:           cells,
		Events:           events,
		NICs:             sysinfo.NICCounters(),
		SampleCount:      count,
	}
}

// WriteExport writes b as indented JSON to dir, returning the file path.
func WriteExport(dir string, b ExportBundle) (string, error) {
	name := fmt.Sprintf("netlogger-export-%d.json", b.GeneratedUnix)
	path := filepath.Join(dir, name)
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

var _ = time.Now // keep time import if unused after edits
```
Fix the `Host` field: set `Host` from the snapshot host. Since `Snapshot` doesn't currently expose the host string directly, add the node host to the bundle via `a.host` under lock — simplest: add a `Host()` accessor like `NodeID()`:
```go
func (a *App) hostName() string { a.mu.Lock(); defer a.mu.Unlock(); return a.host }
```
and set `Host: a.hostName(),` in `Export` (remove the placeholder line and the stray `time` keep-alive if `time` ends up unused — only keep imports actually used).

- [ ] **Step 4: Run to verify pass** — `go test ./internal/appcore/ -run TestExportBundle -v` → PASS. `go vet`, `gofmt -w`, `go build ./...`.

- [ ] **Step 5: Commit** — `git add internal/appcore/ && git commit -m "feat(appcore): one-click analysis Export bundle (JSON) (diag)"`.

---

## Task 6: UI — Reset-all + Export buttons

**Files:** Modify `internal/ui/ui.go`. Manually verified.

- [ ] **Step 1: Add two `widget.Clickable` buttons to the header** (Reset all, Export). In `Run`, declare persistent state before the loop:
```go
	var resetBtn, exportBtn widget.Clickable
	var statusMsg string
```
In the `app.FrameEvent` handler, before laying out, check clicks and act:
```go
			if resetBtn.Clicked(gtx) {
				go a.ResetAll()
				statusMsg = "session reset across the mesh"
			}
			if exportBtn.Clicked(gtx) {
				if exe, err := os.Executable(); err == nil {
					if p, werr := appcore.WriteExport(filepath.Dir(exe), a.Export(time.Now().Unix())); werr == nil {
						statusMsg = "exported " + filepath.Base(p)
					} else {
						statusMsg = "export failed: " + werr.Error()
					}
				}
			}
```
Render the two buttons in the header row with `material.Button(th, &resetBtn, "Reset all").Layout(gtx)` and `material.Button(th, &exportBtn, "Export").Layout(gtx)`, and show `statusMsg` as a small caption. Add imports: `"os"`, `"path/filepath"`, `"time"`, `"gioui.org/widget"`, and ensure `"netlogger/internal/appcore"` is present.

- [ ] **Step 2: Verify** — `go build ./...`, `go vet ./internal/ui/`, `gofmt -w internal/ui/`, `go test ./internal/ui/`.

- [ ] **Step 3: Commit** — `git add internal/ui/ && git commit -m "feat(ui): Reset-all + Export buttons (diag)"`.

---

## Task 7: Build + manual verification

- [ ] **Step 1: Rebuild** — `powershell -ExecutionPolicy Bypass -File scripts/build-app.ps1`.
- [ ] **Step 2: Manual (human):** On the mesh — click **Reset all** on one machine → every machine's uptime + charts + drop counts reset together (and `event: session reset` appears in each log). Reproduce a fault → the log now shows `event: link to <host> degraded (loss X%)` then `recovered`. Click **Export** → a `netlogger-export-<ts>.json` appears beside the exe; send that one file for analysis. Confirm UDP drops now persist (the export's matrix + the db's `udp_iso` rows carry them).

---

## Self-Review

**Coverage of recommendations + the new request:**
- Persist UDP bursts (drop episodes + jitter) → Task 1. ✓
- Connectivity-event logging (degrade/recover/join + session reset) to log + store → Task 2 + Task 3. ✓ (distinguishes app-restart from real drops via explicit events)
- One-click Export bundle → Task 5 + Task 6. ✓
- Synchronized mesh-wide session reset from any app → Task 4 (`/api/command` + `ResetAll`) + Task 6 (button). ✓ — feasible and built.

**Tests added:** UDP persistence (store query), `linkState` hysteresis, `ResetSession`, command handler/client, Export build+write. UI/Gio button wiring is manual (the actions it calls are tested in appcore).

**Placeholder scan:** Backend tasks fully specified + tested. Task 6 (Gio buttons) is the implementer's to assemble; the actions are tested. One note: in Export, ensure imports match actual use (drop the `time` keep-alive line; set `Host` via `hostName()`).

**Type consistency:** `linkState.step(float64)(bool,bool)`; `App.ResetSession()`, `App.ResetAll()`, `App.Export(int64) ExportBundle`, `App.NodeID()/hostName() string`; `commandHandler(func()) http.HandlerFunc`, `postCommand(*http.Client,string,string) error`; `ExportBundle`/`WriteExport(string,ExportBundle)(string,error)`; store `Sample{ProbeType:"udp_iso",...}`, `ConnectivityEvents/InsertConnectivityEvent`, `sysinfo.NICCounters() []sysinfo.NIC`. Consistent across tasks/tests.
