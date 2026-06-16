# NetLogger Portable — NIC Diagnostics (errors/discards + link speed + EEE state) — Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Surface per-adapter NIC health so the EEE-dropout theory can be confirmed or killed: link speed, link status, **Energy-Efficient-Ethernet enabled?**, and the NIC's own **error/discard counters** (with a recent delta). A rising RX/TX discard counter on ryzen during a drop is direct NIC evidence; an "EEE: Enabled" flag on the suspect NIC is the actionable lead.

**Architecture:** New `internal/nicstat` package collects adapter info on Windows via PowerShell (`Get-NetAdapter` + `Get-NetAdapterStatistics` + `Get-NetAdapterAdvancedProperty`) emitted as JSON and parsed in Go (the parse is a pure, testable function); a no-op on other platforms. `appcore` polls it on a slow loop (~8 s; PowerShell startup is ~0.5 s), keeps the previous sample to compute deltas, and exposes `Snapshot.NICs`. The UI adds an "Adapters" section (EEE-enabled and nonzero discards highlighted). The Export bundle uses it.

**Tech Stack:** Go (cgo-free), Windows PowerShell (hidden-console exec, same pattern as the firewall code), Gio.

Reference: the gap analysis — NIC counters + EEE state is the highest-leverage missing feature for the user's specific (EEE-dropout) hypothesis.

Design constants: NIC poll interval `8s`; EEE-keyword match (case-insensitive) `Energy.?Efficient|Green Ethernet|EEE|Gigabit Lite`.

---

## File Structure

| Path | Responsibility |
|---|---|
| `internal/nicstat/nicstat.go` | `NIC` type + `parseNICs([]byte) ([]NIC, error)` (pure). |
| `internal/nicstat/nicstat_windows.go` | `Collect()` — run the PowerShell, return `parseNICs` result. |
| `internal/nicstat/nicstat_other.go` | `Collect()` no-op (nil). |
| `internal/nicstat/nicstat_test.go` | `parseNICs` tests (array + single-object JSON, EEE field). |
| `internal/appcore/appcore.go` | NIC poll loop, delta computation, `Snapshot.NICs`. |
| `internal/appcore/appcore_test.go` | injected-`CollectNICs` snapshot test. |
| `internal/appcore/export.go` | use `Snapshot.NICs` in the bundle. |
| `internal/ui/ui.go` | "Adapters" section. |

---

## Task 1: `internal/nicstat` package

**Files:** Create `internal/nicstat/nicstat.go`, `nicstat_windows.go`, `nicstat_other.go`, `nicstat_test.go`.

- [ ] **Step 1: Write the failing test `internal/nicstat/nicstat_test.go`**

```go
package nicstat

import "testing"

func TestParseNICsArray(t *testing.T) {
	data := []byte(`[
		{"Name":"Ethernet","Description":"Killer E3100G","LinkSpeed":"2.5 Gbps","Status":"Up","RxErrors":0,"RxDiscards":47,"TxErrors":0,"TxDiscards":0,"RxBytes":1000,"TxBytes":2000,"EEE":"Enabled"},
		{"Name":"Wi-Fi","Description":"AX1675x","LinkSpeed":"866 Mbps","Status":"Up","RxErrors":0,"RxDiscards":0,"TxErrors":0,"TxDiscards":0,"RxBytes":3,"TxBytes":4,"EEE":""}
	]`)
	nics, err := parseNICs(data)
	if err != nil {
		t.Fatalf("parseNICs: %v", err)
	}
	if len(nics) != 2 {
		t.Fatalf("expected 2 nics, got %d", len(nics))
	}
	if nics[0].Name != "Ethernet" || nics[0].RxDiscards != 47 || nics[0].EEE != "Enabled" {
		t.Fatalf("nic0 wrong: %+v", nics[0])
	}
}

func TestParseNICsSingleObject(t *testing.T) {
	// PowerShell ConvertTo-Json emits a bare object for a single adapter.
	data := []byte(`{"Name":"Ethernet","Description":"d","LinkSpeed":"2.5 Gbps","Status":"Up","RxDiscards":5,"EEE":"Disabled"}`)
	nics, err := parseNICs(data)
	if err != nil {
		t.Fatalf("parseNICs single: %v", err)
	}
	if len(nics) != 1 || nics[0].RxDiscards != 5 || nics[0].EEE != "Disabled" {
		t.Fatalf("single parse wrong: %+v", nics)
	}
}

func TestParseNICsEmpty(t *testing.T) {
	if nics, err := parseNICs([]byte("")); err != nil || nics != nil {
		t.Fatalf("empty should be nil,nil; got %v,%v", nics, err)
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/nicstat/ -v` → FAIL.

- [ ] **Step 3: Implement `internal/nicstat/nicstat.go`**

```go
// Package nicstat reports per-adapter NIC health (link speed/status, EEE state,
// and error/discard counters) to support NIC/EEE fault diagnosis.
package nicstat

import (
	"bytes"
	"encoding/json"
)

// NIC is one network adapter's current state + cumulative counters.
type NIC struct {
	Name        string `json:"Name"`
	Description string `json:"Description"`
	LinkSpeed   string `json:"LinkSpeed"`
	Status      string `json:"Status"`
	RxErrors    int64  `json:"RxErrors"`
	RxDiscards  int64  `json:"RxDiscards"`
	TxErrors    int64  `json:"TxErrors"`
	TxDiscards  int64  `json:"TxDiscards"`
	RxBytes     int64  `json:"RxBytes"`
	TxBytes     int64  `json:"TxBytes"`
	EEE         string `json:"EEE"` // "Enabled"/"Disabled"/"" (advanced-property value)
}

// parseNICs decodes the PowerShell JSON, tolerating both a JSON array (multiple
// adapters) and a bare object (a single adapter — ConvertTo-Json's quirk).
func parseNICs(data []byte) ([]NIC, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, nil
	}
	if data[0] == '[' {
		var nics []NIC
		if err := json.Unmarshal(data, &nics); err != nil {
			return nil, err
		}
		return nics, nil
	}
	var one NIC
	if err := json.Unmarshal(data, &one); err != nil {
		return nil, err
	}
	return []NIC{one}, nil
}
```

- [ ] **Step 4: Implement `internal/nicstat/nicstat_windows.go`**

```go
//go:build windows

package nicstat

import (
	"os/exec"
	"syscall"
)

const psScript = `$ErrorActionPreference='SilentlyContinue'
@(Get-NetAdapter -Physical | ForEach-Object {
  $a=$_
  $s=Get-NetAdapterStatistics -Name $a.Name
  $eee=(Get-NetAdapterAdvancedProperty -Name $a.Name | Where-Object { $_.DisplayName -match 'Energy.?Efficient|Green Ethernet|EEE|Gigabit Lite' } | Select-Object -First 1).DisplayValue
  [PSCustomObject]@{
    Name=$a.Name; Description=$a.InterfaceDescription; LinkSpeed=[string]$a.LinkSpeed; Status=[string]$a.Status
    RxErrors=[int64]$s.ReceivedPacketErrors; RxDiscards=[int64]$s.ReceivedDiscardedPackets
    TxErrors=[int64]$s.OutboundPacketErrors; TxDiscards=[int64]$s.OutboundDiscardedPackets
    RxBytes=[int64]$s.ReceivedBytes; TxBytes=[int64]$s.OutboundBytes
    EEE=[string]$eee
  }
}) | ConvertTo-Json -Compress`

// Collect runs the PowerShell probe and returns parsed adapter stats (nil on error).
func Collect() []NIC {
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", psScript)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil
	}
	nics, _ := parseNICs(out)
	return nics
}
```

- [ ] **Step 5: Implement `internal/nicstat/nicstat_other.go`**

```go
//go:build !windows

package nicstat

// Collect returns nil on non-Windows builds.
func Collect() []NIC { return nil }
```

- [ ] **Step 6: Run to verify pass** — `go test ./internal/nicstat/ -v` → 3 PASS. `go vet ./internal/nicstat/`, `gofmt -w internal/nicstat/`, `go build ./...`.

- [ ] **Step 7: Commit** — `git add internal/nicstat/ && git commit -m "feat(nicstat): per-adapter NIC stats + EEE state via PowerShell (nic)"`.

---

## Task 2: appcore NIC poll loop + `Snapshot.NICs`

**Files:** Modify `internal/appcore/appcore.go`; add a test to `appcore_test.go`.

- [ ] **Step 1: Write the failing test (append to `appcore_test.go`)**

```go
func TestSnapshotExposesNICsWithDelta(t *testing.T) {
	dir := t.TempDir()
	a := New(dir)
	a.Ping = func(string, time.Duration) (probe.Result, error) { return probe.Result{RTT: time.Millisecond}, nil }
	a.StartIperf = func(string) (func(), string) { return func() {}, "" }
	a.FetchLinks = func(string) (LinkReport, error) { return LinkReport{}, nil }
	a.Discovery = fakeLister{}
	calls := 0
	a.CollectNICs = func() []nicstat.NIC {
		calls++
		return []nicstat.NIC{{Name: "Ethernet", LinkSpeed: "2.5 Gbps", Status: "Up", RxDiscards: int64(40 + calls*5), EEE: "Enabled"}}
	}
	a.nicTick = 5 * time.Millisecond
	a.tick = 5 * time.Millisecond
	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()
	deadline := time.Now().Add(2 * time.Second)
	for {
		nics := a.Snapshot().NICs
		// after >=2 polls, a delta should be computed
		if len(nics) == 1 && nics[0].Name == "Ethernet" && nics[0].EEE == "Enabled" && nics[0].RecentRxDiscards > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("NIC delta not populated; got %+v", a.Snapshot().NICs)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
```
(Add `"netlogger/internal/nicstat"` to the test imports.)

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/appcore/ -run TestSnapshotExposesNICs -v` → FAIL.

- [ ] **Step 3: Modify `internal/appcore/appcore.go`**

3a. Add import `"netlogger/internal/nicstat"`. Add a const `nicPollInterval = 8 * time.Second` (in the const block).

3b. Add a `NICInfo` type (exported, for Snapshot):
```go
// NICInfo is one adapter's state plus the discard/error delta since the prior poll.
type NICInfo struct {
	Name             string
	Description      string
	LinkSpeed        string
	Status           string
	EEE              string
	RxErrors         int64
	RxDiscards       int64
	TxErrors         int64
	TxDiscards       int64
	RecentRxDiscards int64
	RecentTxDiscards int64
	RecentRxErrors   int64
	RecentTxErrors   int64
}
```

3c. Add `App` fields + seam:
```go
	CollectNICs func() []nicstat.NIC // default nicstat.Collect; injectable
	nicTick     time.Duration
	nicMu       sync.Mutex
	nics        []NICInfo
```
In `New`: `CollectNICs: nicstat.Collect,` and `nicTick: nicPollInterval,`.

3d. Launch a NIC loop. In `Start`, change `a.wg.Add(4)` to `a.wg.Add(5)` and add `go a.nicLoop(ctx)`.

3e. Add the loop (computes deltas against the previous poll, keyed by adapter name):
```go
func (a *App) nicLoop(ctx context.Context) {
	defer a.wg.Done()
	prev := map[string]nicstat.NIC{}
	poll := func() {
		raw := a.CollectNICs()
		out := make([]NICInfo, 0, len(raw))
		for _, n := range raw {
			p := prev[n.Name]
			out = append(out, NICInfo{
				Name: n.Name, Description: n.Description, LinkSpeed: n.LinkSpeed, Status: n.Status, EEE: n.EEE,
				RxErrors: n.RxErrors, RxDiscards: n.RxDiscards, TxErrors: n.TxErrors, TxDiscards: n.TxDiscards,
				RecentRxDiscards: nonNeg(n.RxDiscards - p.RxDiscards),
				RecentTxDiscards: nonNeg(n.TxDiscards - p.TxDiscards),
				RecentRxErrors:   nonNeg(n.RxErrors - p.RxErrors),
				RecentTxErrors:   nonNeg(n.TxErrors - p.TxErrors),
			})
			prev[n.Name] = n
		}
		a.nicMu.Lock()
		a.nics = out
		a.nicMu.Unlock()
	}
	poll() // once at startup (prev empty → deltas are the full counts on the 2nd poll)
	t := time.NewTicker(a.nicTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			poll()
		}
	}
}

func nonNeg(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}
```

3f. Add `NICs []NICInfo` to `Snapshot`. In `Snapshot`, read it under `a.nicMu` (leaf lock, outside `a.mu`):
```go
	a.nicMu.Lock()
	nics := a.nics
	a.nicMu.Unlock()
```
and add `NICs: nics,` to the returned struct.

3g. In `ResetSession`, also clear the prev/delta baseline so deltas restart — simplest: under `a.nicMu`, set `a.nics = nil` (the loop's local `prev` resets naturally only on restart; acceptable — the live counters are cumulative anyway). Add:
```go
	a.nicMu.Lock()
	a.nics = nil
	a.nicMu.Unlock()
```

- [ ] **Step 4: Verify** — `go test ./internal/appcore/ -v` → ALL PASS, `go test -count=2 ./internal/appcore/` (no deadlock), `go vet`, `gofmt -w`, `go build ./...`.

- [ ] **Step 5: Commit** — `git add internal/appcore/ && git commit -m "feat(appcore): poll NIC stats, expose Snapshot.NICs with discard deltas (nic)"`.

---

## Task 3: Export includes NICs

**Files:** Modify `internal/appcore/export.go`.

- [ ] **Step 1:** Change the `ExportBundle.NICs` field type from `[]sysinfo.NIC` to `[]NICInfo`, and in `Export` set `NICs: snap.NICs` (from the snapshot) instead of `sysinfo.NICCounters()`. Drop the now-unused `sysinfo` import. (The Windows app's `sysinfo.NICCounters()` was Linux-only/empty anyway.)

- [ ] **Step 2:** Update `export_test.go` only if it referenced the NIC field type (it doesn't set NICs, so likely no change). Run `go test ./internal/appcore/ -run TestExportBundle -v` → PASS.

- [ ] **Step 3: Verify + commit** — `go vet`, `gofmt -w`, `go build ./...`; `git add internal/appcore/ && git commit -m "feat(appcore): include live NIC stats in the export bundle (nic)"`.

---

## Task 4: UI "Adapters" section

**Files:** Modify `internal/ui/ui.go`. Manually verified.

- [ ] **Step 1:** Add a `layoutAdapters(gtx, th, snap)` section (rendered between Infrastructure and Peers, or just above the matrix — your call). For each `snap.NICs`, render a line:
```go
fmt.Sprintf("%s  %s  %s  EEE:%s  discards rx+%d tx+%d  errors rx+%d tx+%d",
	n.Name, n.LinkSpeed, n.Status, eeeText(n.EEE),
	n.RecentRxDiscards, n.RecentTxDiscards, n.RecentRxErrors, n.RecentTxErrors)
```
Highlight (color the row text) when it matters: **vermillion** if `n.RecentRxDiscards+n.RecentTxDiscards+n.RecentRxErrors+n.RecentTxErrors > 0`; **orange** if `n.EEE` is a non-empty "enabled-ish" value (EEE on a wired adapter is the suspect); otherwise default. Add small pure helpers + tests:
```go
func eeeText(v string) string {
	if v == "" {
		return "n/a"
	}
	return v
}
func eeeIsOn(v string) bool {
	return strings.EqualFold(v, "Enabled") || strings.EqualFold(v, "On")
}
```
Add a header line "Adapters:" and an empty-state ("Adapters: (none reported)") when `len(snap.NICs)==0`.

- [ ] **Step 2: Add tests** (`internal/ui/`): `eeeText("")=="n/a"`, `eeeText("Enabled")=="Enabled"`, `eeeIsOn("enabled")==true`, `eeeIsOn("Disabled")==false`. (Add `"strings"` import.)

- [ ] **Step 3: Verify + commit** — `go test ./internal/ui/`, `go build ./...`, `go vet ./internal/ui/`, `gofmt -w internal/ui/`; `git add internal/ui/ && git commit -m "feat(ui): Adapters section — link speed, EEE state, discard/error deltas (nic)"`.

---

## Task 5: Build + manual verification

- [ ] **Step 1:** `powershell -ExecutionPolicy Bypass -File scripts/build-app.ps1`.
- [ ] **Step 2: Manual (human):** relaunch; an **Adapters** section lists each NIC with link speed, status, **EEE state**, and discard/error deltas. On ryzen, confirm whether the Killer E3100G shows **EEE: Enabled** (the lead) and whether **discards tick up during a drop** (the proof). The export bundle now carries the same NIC data.

---

## Self-Review

**Coverage:** Per-adapter link speed/status + EEE state + error/discard counters with recent delta → Tasks 1-2; surfaced in UI (Task 4) + export (Task 3). Directly targets the EEE-dropout hypothesis (EEE-enabled flag + rising discards = the evidence).

**Tests:** `parseNICs` (array/single/empty), the appcore NIC-delta snapshot (injected `CollectNICs`), the UI `eeeText`/`eeeIsOn` helpers. The PowerShell `Collect()` itself is integration (verified in the manual gate).

**Placeholder scan:** Backend + pure helpers fully specified/tested. Task 4 Gio composition is the implementer's to assemble; its testable logic is covered.

**Type consistency:** `nicstat.NIC{Name,Description,LinkSpeed,Status,RxErrors,RxDiscards,TxErrors,TxDiscards,RxBytes,TxBytes,EEE}`, `parseNICs([]byte)([]NIC,error)`, `nicstat.Collect() []NIC`; `appcore.NICInfo{...,Recent*}`, `App.CollectNICs func() []nicstat.NIC`, `App.nicLoop`, `Snapshot.NICs []NICInfo`; `ExportBundle.NICs []NICInfo`; ui `eeeText/eeeIsOn`. Consistent across tasks/tests.
