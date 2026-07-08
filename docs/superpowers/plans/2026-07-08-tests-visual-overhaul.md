# Tests Visual Overhaul + Critical Functional Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Implement every visual improvement from `docs/superpowers/specs/2026-07-08-tests-ux-research.md` plus the two most critical functional items: the directional matrix graded as % of link speed (P1+P2), and persisted stress-run results (S2). Ships as v1.3.0.

**Architecture:** UI-heavy. The matrix change is a pure render-time transform (run-keyed cells → flow-keyed cells) — the sweep engine is untouched. Two small additive wire changes: `LinkReport.LinkSpeedMbit` (peers beacon their NIC speed for %-of-link grading; old peers → 0 → absolute fallback) and a new `stress` kind in test_results. Everything else is `internal/ui`.

**Tech Stack:** Go 1.26 (cgo-free on Windows), Gio v0.10.0. Repo conventions: plain factual UI copy (docs/design-guide.md — no verdicts/editorial), pure logic in untagged files with unit tests, `go test ./...` green before every commit, `$env:CGO_ENABLED='0'` on Windows.

**Deferred (do NOT implement):** stress duration control, internet median-of-3, scheduled internet tests, shared shell refactor (X4), dashboard-tab chart scales.

---

## Design constants (used across tasks)

```go
// Validated series palette (dark surface #161d29; all 6 dataviz checks pass,
// protan dE 58 for the first three). Severity colors (colGood/colWatch/colBad)
// are RESERVED for state and never used as series colors again.
var seriesColors = []color.NRGBA{
	{R: 0x4f, G: 0x8f, B: 0xf7, A: 0xff}, // blue
	{R: 0xc6, G: 0x77, B: 0x18, A: 0xff}, // orange
	{R: 0x0b, G: 0xab, B: 0x9e, A: 0xff}, // teal
	{R: 0x9a, G: 0x7e, B: 0xf0, A: 0xff}, // violet (4th+: legend chips carry identity)
	{R: 0xd4, G: 0x69, B: 0x9e, A: 0xff}, // pink
}
```

%-of-link severity buckets: `>=0.85` good · `0.50–0.85` watch · `<0.50` bad. Asymmetry marker threshold: slower direction < 80% of the faster one.

---

### Task 1: Series palette + reserved severity (X3)

**Files:** Modify `internal/ui/tests.go` (replace `linkColors`), `internal/ui/tests_test.go`.

- [ ] Replace `linkColors` with `seriesColors` above (rename all uses: `stressLatencyChart`, `stressRateChart`, `legendRow`). Grep for `linkColors` to catch all.
- [ ] In `pairDetail`, chart series colors become `seriesColors[0]` (blue) for the first series and `seriesColors[1]` (orange) for the second — stop using `colAccent`/`upGreen` in charts. (`upGreen` remains for the internet metric card accent only.)
- [ ] Add a test asserting no series color equals `colBad`/`colWatch`/`colGood`:

```go
func TestSeriesColorsAvoidSeverity(t *testing.T) {
	for i, c := range seriesColors {
		if c == colBad || c == colWatch || c == colGood {
			t.Errorf("seriesColors[%d] reuses a severity color", i)
		}
	}
}
```

- [ ] `go test ./internal/ui/` → PASS. Commit: `feat(ui): validated series palette; severity colors reserved for state`

---

### Task 2: Scaled charts everywhere in Tests (X1)

**Files:** Modify `internal/ui/sparkline.go`, `internal/ui/tests.go`; test `internal/ui/sparkline_test.go`.

- [ ] Add a range-pinned multi-series sparkline and a pure tick formatter to `sparkline.go`:

```go
// multiSparklineRange draws serieses on ONE FIXED scale [min,max] in a w×h dp
// box. markFrac >= 0 draws a dashed vertical marker at that x fraction (used
// for a stress run's start). Unlike multiSparkline it never auto-normalizes,
// so the caller can pin a zero baseline and label the scale honestly.
func multiSparklineRange(gtx layout.Context, serieses [][]float64, cols []color.NRGBA, w, h int, min, max float64, markFrac float64) layout.Dimensions
```

Implementation mirrors `multiSparkline` but uses the provided min/max (guard `max <= min` → draw nothing but return the box); marker = 1dp dashed line (draw 3px dash segments in a loop) in `colBorder`.

- [ ] Add the pure scale-bounds helper + test:

```go
// chartBounds returns [0,max] rounded up to a clean ceiling for zero-based
// charts: max is the data max (or floorMax if larger), padded ~5%.
func chartBounds(serieses [][]float64, floorMax float64) (float64, float64) {
	max := floorMax
	for _, s := range serieses {
		for _, v := range s {
			if v > max {
				max = v
			}
		}
	}
	if max <= 0 {
		return 0, 1
	}
	return 0, max * 1.05
}
```

Test: empty → (0,1); data 180 w/ floor 200 → (0,210); data 950 w/ floor 200 → (0,~997.5).

- [ ] Add a `scaledChart` layout helper in `tests.go`: a horizontal flex of a 52dp right-aligned y-axis column (two 10sp `colTextMut` captions: formatted max on top, "0" at bottom) + the `multiSparklineRange` filling the rest. Signature:

```go
// scaledChart wraps multiSparklineRange with a labeled y-axis (max over 0).
// fmtMax renders the top label (e.g. fmtRate or "80 ms").
func scaledChart(gtx layout.Context, th *material.Theme, serieses [][]float64, cols []color.NRGBA, hDp int, min, max float64, fmtMax string, markFrac float64) layout.Dimensions
```

- [ ] Apply it: `pairDetail` (bounds via `chartBounds(series, 0)`, fmtMax `fmtRate(max)`), `stressRateChart` (floorMax = float64(cap), fmtMax `fmtRate`), `stressLatencyChart` (floorMax 0, fmtMax `fmt.Sprintf("%.0f ms", max)`). Keep the existing captions but they must not claim more than the data (see Task 3 for stress labels).
- [ ] `go test ./internal/ui/` + `go build ./...` → PASS. Commit: `feat(ui): scale labels on every Tests chart — no more normalized squiggles`

---

### Task 3: Stress — RRUL alignment + quiet idle (S1, S4, cap-chip dedupe)

**Files:** Modify `internal/ui/tests.go` (`layoutStress`, `stressRateChart`, `stressLatencyChart`, `configChips` call).

- [ ] **Quiet idle:** in `layoutStress`, render `stressRateChart` and `stressLatencyChart` ONLY when `on || len(nodes) > 0` (i.e. a run is active or just finished polling). At idle render instead the stress history list from Task 6 (`historyList(gtx, th, st.stressHist)`); if empty, keep the existing "no active run" caption.
- [ ] **RRUL stack:** while running, render the two charts as one block: throughput chart (56dp) directly above latency chart (56dp), both via `scaledChart` with the SAME width (they already share the flex width), a single shared time caption row underneath (`"run start"` left when the run began <60s ago, `"last 60 s"` otherwise; `"now"` right — both 10sp `colTextMut`), and ONE `legendRow` for both charts (they share node/link identity). Chart captions: `"Throughput per link"` and `"Latency"` (drop `"· last minute"` and `"· Mb/s per second"` — the axis now says it). Pass `markFrac` = position of run start within the 60-sample window (`1 - elapsed/60` clamped to [0,1]; -1 when elapsed > 60s).
- [ ] **Cap-chip dedupe:** idle `configChips` drops the `Per-link cap · …` entry (the stepper beside Start already shows it). While running, KEEP the cap chip (the stepper is hidden then): `configChips(gtx, th, "Topology · full mesh", fmt.Sprintf("Cap · %s", fmtRate(float64(st.cap()))), "Protocol · TCP", "Probes · continuous")` — build the chip list conditionally on `on`.
- [ ] Windows build + `go test ./...` green. Commit: `feat(ui): RRUL-aligned stress charts; idle stress view goes quiet`

---

### Task 4: Internet — merged latency strip, honest fields, units (I1, I4, X2)

**Files:** Modify `internal/ui/internet.go`; test `internal/ui/tests_test.go` (pure helpers live in tests.go/internet.go).

- [ ] **Units:** metric cards use `Mb/s` (replace the `"Mbit/s"` sub-label strings). Rates in history/live views go through `fmtRate` where a number+unit is printed.
- [ ] **Merged latency strip:** in `internetResults`, replace the separate Idle/Loaded metric cards + grade card cluster with one row: `idle <N> ms` → `loaded <N> ms` → `+<Δ> ms` (each a small stat: 11sp muted label over 20sp value) then the grade badge (existing `gradeBadge`), then a factual scale caption `"A <30 · B <60 · C <100 · D <200 ms added"` in `colTextMut`. Keep the Download/Upload cards row above unchanged (except units).
- [ ] **Phantom zeros:** the grade sub-line renders `RPM <n>` always, but `jitter`/`loss` ONLY when `> 0` (they are unmeasured today; showing `0` claims a measurement). Pure helper + test:

```go
// gradeSubLine renders the grade context without claiming unmeasured zeros.
func gradeSubLine(rpm int, jitterMs, lossPct float64) string {
	s := fmt.Sprintf("RPM %d", rpm)
	if jitterMs > 0 {
		s += fmt.Sprintf(" · jitter %.0f ms", jitterMs)
	}
	if lossPct > 0 {
		s += fmt.Sprintf(" · loss %.1f%%", lossPct)
	}
	return s
}
```

Test: `gradeSubLine(1415, 0, 0) == "RPM 1415"`; with jitter 2 → contains `jitter 2 ms`.

- [ ] **Phase strip only while running:** render `phaseStrip` in `internetLive` only; remove it from `internetResults` (the all-checkmarks state carries no information).
- [ ] **Provenance names the node:** the `measured Jan 2 15:04` line becomes `measured Jan 2 15:04 · on <host> · <server>` where host = the node the test ran on (remote host when set, else `snap.SelfPeer.Host`) and server = the endpoint used (already known). Same format on history rows if absent.
- [ ] **Node picker selected state:** the picker chips use the same visual as `segControl`'s active segment (filled `colCardAlt` + `colTextPri` + border) so the selected node is unambiguous; `this device` is a chip like the others, labeled `<host> · this device`.
- [ ] Build + tests green. Commit: `feat(ui): internet — one latency story, honest fields, Mb/s units`

---

### Task 5: Config provenance on sweeps (P4)

**Files:** Modify `internal/ui/tests.go`, `internal/ui/ui.go`, `internal/appcore/test_history.go` (+test).

- [ ] Add a pure config formatter (ui or appcore — put it in appcore so history writing uses it):

```go
// SweepConfigLine renders a run's settings for provenance chips/history.
// e.g. "both · 10s · 4 streams".
func SweepConfigLine(req SpeedReq) string {
	d := req.Direction
	if d == "" {
		d = "both"
	}
	streams := req.Streams
	if streams <= 0 {
		streams = 1
	}
	return fmt.Sprintf("%s · %ds · %d streams", d, req.DurationS, streams)
}
```

Test the three shapes (defaults, bidir/30/8, down/10/1 → "1 streams" is fine — plain).

- [ ] `sweepSummary` (test_history.go) appends the config to the Detail: `"slowest … · both · 10s · 4 streams"`. It needs the `SpeedReq` — thread it: `SpeedSweep` already has `req`; pass into `sweepSummary(nodes, cells, req)`. Update its test.
- [ ] UI: store the running req in `testsState` (`lastReq appcore.SpeedReq`, set under `st.mu` where the sweep starts in ui.go); completed status becomes `"completed 15:04 · " + appcore.SweepConfigLine(req)`.
- [ ] `historyList` renders Detail as-is (config arrives via Detail — no widget change needed). Verify a fresh sweep's Recent row shows the chip text.
- [ ] Build + tests green. Commit: `feat(tests): sweep config provenance on status line and history`

---

### Task 6: FUNCTIONAL — stress runs persist (S2)

**Files:** Modify `internal/appcore/stress.go` or new `internal/appcore/stress_history.go`, `internal/ui/tests.go`, `internal/ui/ui.go`; tests in `internal/appcore/stress_test.go`, `internal/ui/tests_test.go`.

The orchestrator (the node that pressed Start) records one `test_results` row of kind `stress` when the run ends. Store schema already fits: `Label` = "6 links · 200 Mb/s cap · TCP", `Detail` = "10:00 · worst +38 ms on ryzen · 0 aborts", Down/Up = 0.

- [ ] **historyList zero-rate omission** (stress rows have no rates): in `historyList`, when `r.DownMbit == 0 && r.UpMbit == 0` skip the `↓ … ↑ …` label entirely. Pure enough to eyeball; add a note-test if a pure helper is extracted.
- [ ] **Baseline/max tracking in the UI poll loop** (ui.go already runs a 1s poll goroutine with a generation counter): at Start, snapshot per-peer idle RTT (`baseRTT map[hostID]float64` from `snap.Peers[i].RTTms`, plus self excluded); each poll tick, update `maxRTT[hostID]` from the current snapshot. Guard with `st.stressMu`. At run end (poll observes all nodes `Running == false` or the gen changes after natural end), compute worst = max over hosts of `maxRTT - baseRTT` (clamp ≥ 0), count aborts from the final `stressNodes` links, and call the new appcore recorder ONCE (guard with a `recorded bool` in the poll goroutine).
- [ ] **Recorder in appcore** (+ unit test with the in-memory store pattern used by existing history tests):

```go
// RecordStressRun persists the orchestrator's summary of a finished stress run.
func (a *App) RecordStressRun(durS, links, capMbit int, proto, worstHost string, worstAddMs float64, aborts int) {
	label := fmt.Sprintf("%d links · %s cap · %s", links, fmtCap(capMbit), strings.ToUpper(proto))
	detail := fmt.Sprintf("%s · worst +%.0f ms on %s · %d aborts", mmssStr(durS), worstAddMs, worstHost, aborts)
	if worstHost == "" {
		detail = fmt.Sprintf("%s · %d aborts", mmssStr(durS), aborts)
	}
	a.recordTestResult(store.TestResult{Kind: "stress", Label: label, Detail: detail})
}
```

(`fmtCap` = "200 Mb/s" / "1.5 Gb/s" — reuse the same thresholds as ui.fmtRate but appcore-local; `mmssStr` = "10:00". Both pure + tested. Match `recordTestResult`'s actual signature — read test_history.go first.)

- [ ] **History plumbing:** `TestHistory("stress", …)` already generic. `testsState.stressHist []store.TestResult`, loaded in the existing 5s history refresh alongside net/sweep. Render `Recent` under the stress view (running AND idle — idle shows it instead of charts, per Task 3). Export bundle: add `StressTests` (last 50) beside the existing two kinds in export.go (+test).
- [ ] Full suite green. Commit: `feat(stress): runs persist — per-run summary rows with worst added latency`

---

### Task 7: FUNCTIONAL — directional matrix + % of link (P1 + P2)

**Files:** Modify `internal/appcore/links.go` (LinkSpeedMbit beacon), `internal/appcore/appcore.go` (fill it), `internal/nicstat` (NO change — parse in appcore), `internal/ui/tests.go` (flow transform + rendering); tests in `internal/appcore/links_test.go`, `internal/ui/tests_test.go`.

**7a — Link-speed beacon (additive wire, mirrors the Version/Platform pattern):**

- [ ] `LinkReport` gains `LinkSpeedMbit int` `json:"link_speed_mbit,omitempty"`. Old peers → 0 → UI falls back to absolute grading.
- [ ] Pure parser + test (appcore):

```go
// parseLinkSpeedMbit converts nicstat's LinkSpeed vocabulary ("2.5 Gbps",
// "1 Gbps", "100 Mbps") to Mbit/s; 0 when unknown.
func parseLinkSpeedMbit(s string) int
```

Cases: "2.5 Gbps"→2500, "1 Gbps"→1000, "100 Mbps"→100, "10 Gbps"→10000, ""→0, "autoselect"→0. Split on space; parse float; unit suffix case-insensitive `gbps`→×1000, `mbps`→×1.

- [ ] appcore fills it in `linkReport()`: max `parseLinkSpeedMbit` over NICs with `Status == "Up"` (read the cached NIC slice the same way Snapshot does — under the right mutex; see `nicMu`). Snapshot: `SelfPeer` and `Peers[i]` gain `LinkSpeedMbit int` (peer value from pulled reports, same place Build/Platform are copied).
- [ ] Wire test: a `LinkReport` round-trip with the field; a peers-copy test mirroring the existing Platform copy test.

**7b — Flow transform (pure, UI-side; the sweep engine is untouched):**

- [ ] Types + transform in tests.go (or a new `internal/ui/flow.go`):

```go
// flowCell is one direction of data flow src→dst, assembled from run cells.
type flowCell struct {
	Mbit    float64 // primary measurement (see mapping below)
	Confirm float64 // the mirror run's measurement of the SAME flow (0 = none)
	Retr    int
	RTTms   float64
	Err     string
}

// flowCells re-keys run results (client\x00server) into flow results
// (src\x00dst). A run client=A server=B measures: up leg = flow A→B,
// down leg = flow B→A. When both ordered runs exist ("both" sweeps), the
// flow gets two measurements: primary = the leg whose CLIENT is the flow
// source (A's up leg for A→B), confirm = the other run's down leg.
func flowCells(cells map[string]appcore.SpeedResult) map[string]flowCell
```

Mapping rules (write exactly):
- run[A,B].UpMbit > 0 → flow[A,B].Mbit = UpMbit (primary); run[A,B].Retransmits/RTTms attach to it.
- run[B,A].DownMbit > 0 → flow[A,B]: if Mbit == 0 → Mbit = DownMbit (fallback primary), else Confirm = DownMbit.
- Errors: a run error sets Err on BOTH flows it would have measured, but never overwrites a real measurement from the mirror run.
- Live points: phase "up" → flow client→server; "down"/"bidir" → also map "bidir" up-number to client→server (bidir's interval sums track the sent direction).

Unit test with a 2-node "both" result set asserting: flow[A,B].Mbit == run[A,B].UpMbit, flow[A,B].Confirm == run[B,A].DownMbit, and the error-propagation case.

- [ ] Asymmetry (pure + test): `asymmetric(ab, ba float64) bool` → both > 0 && min < 0.80×max.
- [ ] %-of-link severity (pure + test):

```go
// linkPct returns rate ÷ min(endpoint link speeds), or -1 when unknown.
func linkPct(mbit float64, aMbit, bMbit int) float64
// pctBucket: >=0.85 good, >=0.50 watch, else bad. (-1 → fall back to
// matrixCellColor's absolute thresholds.)
```

- [ ] **Rendering** (`layoutSpeedMatrix`): header row label becomes `from ↓ · to →` in the corner cell; column headers lose the `↓ ` prefix (bare host names). Cells render `flowCell`s: main = `fmtRate(Mbit)` + ` ▲` suffix when this flow is the slower of an asymmetric mirror pair; sub-line = `"94% of link"` when linkPct ≥ 0 (else existing loss/slow/rtt logic). Live cells unchanged (Task 68 behavior) but keyed through the flow mapping. Footnote: `"cell = data flowing from row to column · ▲ slower than reverse"`. Legend when any pair has link speeds: `≥85% of link · 50–85% · <50%`; else the absolute legend.
- [ ] `pairDetail` for a flow: `A → B  942 Mb/s · 94% of 1 Gb link · retransmits 0 [· reverse direction 937 Mb/s]`; chart shows the primary run's per-second series solid + (when present) the confirm run's series dashed, both through `scaledChart`, legend `"━ this direction · ╌ reverse"`.
- [ ] Update existing matrix tests (`matrixCellText` etc. unchanged; add flow tests). Full suite green.
- [ ] Commit: `feat(speed): directional matrix — cell = flow row→column, graded as % of link`

---

### Task 8: Version + release

- [ ] `internal/version/version.go` → `1.3.0`; `cmd/netlogger-app/versioninfo.json` → 1.3.0 (FixedFileInfo + StringFileInfo).
- [ ] `$env:CGO_ENABLED='0'; go vet ./...; go test ./...` — all green. `gofmt` note: repo-wide `gofmt -l` lists everything due to CRLF; rely on vet/build.
- [ ] Commit: `chore: v1.3.0 — tests visual overhaul + directional matrix + stress history`. Do NOT push; do NOT run scripts/build-app.ps1 (the reviewer builds and pushes).

## Self-review checklist for the implementer

- Every chart in the Tests tab has a visible scale; nothing normalizes silently.
- No series drawn in colBad/colWatch/colGood.
- Zero-valued unmeasured fields are omitted, not printed.
- Copy stays plain: no verdicts, no exclamation, labels are nouns/data.
- Old-peer compatibility: LinkSpeedMbit 0 → absolute grading; mixed mesh must not crash or mis-grade.
- `go test ./...` green at every commit; Windows `go build ./...` green.
