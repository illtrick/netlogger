# macOS Parity — Windows→Mac Sync Channel

This is the standing hand-off from the Windows dev line to the macOS build.
**After every successful Windows build, a new dated entry is appended to the
[Change log](#change-log) below** describing what changed and what — if
anything — the Mac side must do to stay at parity. The macOS port itself is
complete and documented in [docs/MACOS.md](MACOS.md); this file is only the
running delta between the two platforms.

## How to use this (on the Mac)

1. `git pull` and read the **newest** Change-log entry (top of the list).
2. For each change, apply its **class** (below) — most are "rebuild + verify."
3. Run the [verification bar](#verification-bar). It must be green.
4. Tick the entry's checkboxes, commit any Mac-side code the entry required,
   and push. If a change needed a new `_darwin.go` seam, that code is the
   Mac's to write — the Windows side only ships the stub + the contract.

If several Windows builds landed since you last synced, walk the entries
oldest→newest; each is self-contained.

## Parity model — every change is one of three classes

| Class | What it means | Mac action |
|---|---|---|
| **Portable** | Shared, untagged Go (engine, UI logic, `version`, `store`, pure parsers). Compiles and behaves the same on every OS. | **Rebuild + verify.** No Mac code. |
| **Platform seam** | Windows added or changed a build-tagged value (`*_windows.go` + `*_other.go`, or a new `const`/`func` seam) that darwin must satisfy. | **Implement the `*_darwin.go` counterpart** to the contract the entry states, then verify. |
| **Spec / behavioral** | A cross-platform behavior, UX rule, or protocol change that the Mac must *match* even though the code is portable (e.g. a new wire field, a copy rule, a warning's semantics). | **Verify the behavior on the Mac**, live, against a Windows node where relevant. |

The guiding pattern the whole codebase follows: **small build-tagged files
define per-platform values; untagged code consumes them; pure logic stays
untagged so it unit-tests on any OS.** When you add a seam, keep that shape.

## Standing invariants (the spec guidance that never changes)

These are the cross-platform contracts every change is judged against. A
Windows change that would violate one of these on the Mac is a parity bug —
call it out in the entry. Full rationale for each lives in
[docs/MACOS.md](MACOS.md); the short version:

1. **Compatibility is `version.Version` (semver), not the git build.** Same
   Version ⇒ nodes interoperate regardless of OS. `version.Platform()`
   (`GOOS/GOARCH`) exists so a Mac↔PC binary difference is never mistaken for
   a mismatch. Only bump `Version` when the wire protocol changes; when you do,
   say so in the entry (every node must upgrade together).
2. **No elevation on macOS, ever.** Unprivileged ICMP (`privilegedICMP=false`).
   Nothing may introduce a code path that needs root on darwin.
3. **The app binary is built on a Mac (cgo + Apple SDK).** The engine stays
   pure Go and cross-compiles from anywhere; UI/`nicstat` darwin need cgo.
4. **Nothing is ever written inside the `.app` bundle** (breaks the codesign
   seal): DB, log, and exports go to `~/Library/Application Support/NetLogger`
   via `datadir`/`SidecarDir`. A new file the app writes must route through
   those, not `os.Executable()`'s dir.
5. **Native window chrome on macOS** (`app.Decorated(false)` + re-shown traffic
   lights). `customChrome` (hand-drawn caption buttons) is Windows-only. Don't
   mutate the NSWindow style mask behind Gio's back.
6. **iperf3 is resolved, not bundled, on macOS** (`brew install iperf3`);
   monitoring must degrade cleanly when it's absent.
7. **A platform may lack a signal.** macOS has no rootless RX-drop or EEE
   source; the UI shows whatever the platform provides. Don't make a Windows
   metric a hard requirement in shared code.
8. **UI copy stays plain and factual** — no editorial/verdict language, on
   either platform.

## Verification bar

A macOS sync is "successful" only when:

- `./scripts/test-mac.sh` is green (6 layers: fmt/vet, `go test ./...`,
  `-race`, cross-compile Windows+Linux engine, live `nicstat.Collect()`,
  `build-mac.sh` + bundle assertions).
- `./scripts/build-mac.sh` produces `bin/NetLogger.app` and it launches.
- Any change tagged **Spec / behavioral** has been exercised live against a
  Windows NetLogger node on the same LAN.

## Entry template (copy for each Windows build)

```markdown
### <YYYY-MM-DD> · Windows `<short-hash>` (<one-line title>)

**Range:** `<prev-hash>..<hash>`  ·  **Net effect for Mac:** <rebuild-only | N seams | behavioral>

- **[Portable]** <what changed> → rebuild + verify. No Mac code.
- **[Platform seam]** <seam name> — contract: <what the darwin file must return/do>.
  - [ ] Implement `internal/<pkg>/<file>_darwin.go`
- **[Spec/behavioral]** <behavior> → verify live: <exact thing to observe>.
  - [ ] <verification step>

Verify: `./scripts/test-mac.sh` green; <extra live checks>.
```

---

## Change log

_Newest first. Each entry corresponds to one Windows build push._

### 2026-07-08 · Windows `edbc99f` (stress per-link server ports — v1.3.1)

**Range:** `44b3f34..edbc99f` · **Net effect for Mac: rebuild-only + one live check.** No new seams.

- **[Portable]** N≥3 full-mesh stress aborted one inbound link per node within
  seconds: iperf3 serves one test at a time, and two clients hit each node's
  single 5201 server. The orchestrator now assigns each target's inbound
  clients distinct ports and nodes spawn extra ephemeral `iperf3 -s -p 520N`
  listeners for the run (`StressOpts.TargetPorts`/`ListenPorts`, additive).
  Multi-port mode engages only when every participant runs the same release.
- **[Spec/behavioral]** live check on the Mac (needs ≥3 nodes, all 1.3.1):
  - [ ] Start a full-mesh stress including the Mac: all 2·N(N−1)/2 directed
    links load (none "aborted" at start), and `ps aux | grep iperf3` on the
    Mac shows the extra `-p 5202` listener during the run, gone after it.
  - [ ] macOS firewall: the extra listener is the same brew iperf3 binary
    already allowed — confirm no new prompt mid-run (a prompt would pause
    loading; if it appears, allow once and note it).

Verify: `./scripts/test-mac.sh` green (new: meshAssignments/portsSupported/
sanitize pair tests, listener spawn-stop lifecycle test).

### 2026-07-08 · Windows `9dc1919` (tests visual overhaul + directional matrix + stress history — v1.3.0)

**Range:** `cb68bfb..9dc1919` (8 feature commits, plan `docs/superpowers/plans/2026-07-08-tests-visual-overhaul.md`, rationale `docs/superpowers/specs/2026-07-08-tests-ux-research.md`) · **Net effect for Mac: rebuild-only + live checks.** No new platform seams.

- **[Portable]** All UI: scaled charts (y-axis labels, zero-based throughput),
  validated series palette (severity colors reserved), RRUL-aligned stress
  charts + quiet idle, internet latency strip + `Mb/s` units, config
  provenance chips. → rebuild + verify.
- **[Portable]** Directional speed matrix: cell = flow row→column (pure
  render-side transform; sweep engine unchanged), ▲ asymmetry markers,
  severity graded as % of min(endpoint link speeds).
- **[Portable]** Stress runs persist: orchestrator records a `stress` history
  row (links · cap · duration · worst added latency · aborts); rows appear
  under the Stress view and in the export bundle.
- **[Spec/behavioral]** One additive wire field: `LinkReport.LinkSpeedMbit`
  (fastest Up NIC, parsed from nicstat's LinkSpeed vocabulary). The darwin
  collector already emits that vocabulary ("1 Gbps", "2.5 Gbps" via
  `mediaToSpeed`), so `parseLinkSpeedMbit` should work unchanged — confirm live:
  - [ ] A Mac↔Windows 1.3.0 sweep shows `% of link` sub-lines on Mac-involved
    cells (not the absolute fallback), with the Mac's real negotiated rate as
    the denominator when it is the slower endpoint.
  - [ ] Wi-Fi caveat: CoreWLAN reports live PHY rate as LinkSpeed — %-of-link
    on a Wi-Fi Mac grades against PHY rate, which is optimistic; note what you
    observe (acceptable for 1.3, flag if it grades absurdly).
  - [ ] Stress run from the Mac records a history row with plausible worst
    added latency; mixed mesh with any pre-1.3 peer must fall back to
    absolute grading without crashing (covered by unit tests, verify visually).
- **Version:** 1.3.0 — `build-mac.sh` stamps the bundle automatically.

Verify: `./scripts/test-mac.sh` green (new suites: flow transform, linkPct/
pctBucket, parseLinkSpeedMbit, RecordStressRun, chartBounds, gradeSubLine),
then the live checks above.

### 2026-07-08 · Windows `0f6084f` (live speed/stress telemetry, loopback fix — v1.2.0)

**Range:** `cd2f7fb..0f6084f` (`89c6f72` loopback fix + units, `0f6084f` live telemetry) · **Net effect for Mac: rebuild-only + live checks.** No new platform seams — everything is untagged Go.

- **[Portable]** `internal/iperf/stream.go` — `RunClientStream` uses
  `--json-stream` for per-second live intervals. → rebuild + verify; the parser
  fixtures run on the Mac in layer 2.
- **[Portable]** Loopback-misroute fix: `SelfPeer.Addr` now advertises the
  primary LAN IP (`discovery.PrimaryIP`), and speed/stress refuse loopback
  targets. Remote→self matrix cells previously measured the remote's own
  loopback (100+ Gbit readings).
- **[Portable]** UI: rate units everywhere (Mb/s / Gb/s), sweep controls
  (direction/duration/streams), live rate in the actively-testing cell,
  per-second chart in pair detail, stress throughput-per-link chart + retr.
- **[Spec/behavioral]** to confirm live on the Mac:
  - [ ] `brew info iperf3` reports **≥ 3.17** (current brew is 3.x-recent — fine).
    Older installs silently fall back to final-only results (no live points
    from that node); if so, `brew upgrade iperf3`.
  - [ ] Sweep from the Mac against a Windows 1.2.0 node: the testing cell's
    rate ticks every second in BOTH directions (Mac-as-client and
    Mac-as-server pairs), and the Mac→Windows cell reads LAN-plausible
    (≈ link speed), NOT 20+ Gb/s.
  - [ ] Pair-detail chart: on the Mac, interval `retransmits`/`rtt` ARE
    populated (TCP_INFO exists there, unlike Windows/Cygwin) — retransmit
    counts should be non-zero under a saturating parallel run if the link
    genuinely drops.
  - [ ] Stress: per-link throughput chart + latency chart move together every
    second; rows show `retr N` accumulating on lossy links.
- **Version:** 1.2.0 — `build-mac.sh` stamps the bundle from
  `internal/version.Version` automatically; no plist edit. Mixed 1.1/1.2
  meshes show the version-mismatch banner until all nodes are rebuilt
  (expected; streamed sweeps degrade gracefully against 1.1 peers).

Verify: `./scripts/test-mac.sh` green (new tests: `internal/iperf` stream
fixtures, appcore NDJSON handler/client, sweep live-points, stress live-rate),
then the live checks above.

### 2026-07-04 · Windows `8523fb8` (version-based compatibility warning + v1.1.0 hygiene)

**Range:** `f124126..8523fb8` (two commits: `eed3fe3`, `8523fb8`) · **Net effect for Mac: rebuild-only + one live check.** No new seams.

- **[Portable]** `internal/datadir/datadir.go` (`eed3fe3`) — the "no writable
  data dir" error now formats paths with `%s` instead of `%q` (the `%q`
  backslash-escaping only ever hurt Windows). → rebuild + verify. The darwin
  `datadir` tests are unaffected; `test-mac.sh` layer 2 covers it.
- **[Portable]** `internal/version/version.go` — `Version` is now `1.1.0` and a
  new `Platform()` returns `GOOS/GOARCH`. On the Mac it resolves to
  `darwin/arm64` (or `amd64`) automatically — **no Mac code needed.** Note:
  `build-mac.sh` already stamps the bundle version from `version.Version`, so
  the `.app` becomes 1.1.0 on the next build with no plist edit.
- **[Spec/behavioral]** `internal/appcore/links.go` + `appcore.go` + `ui.go` —
  the mesh build-mismatch banner is replaced by a version-based
  `meshWarning`. `LinkReport` now beacons `Version` + `Platform` alongside
  `Build`. Semantics the Mac must confirm live:
  - [ ] A **Mac and a Windows node on the same release (both 1.1.0)** show **no
    banner** — this is the whole point; a same-version/different-OS peer must
    never warn.
  - [ ] The footer on the Mac reads `v1.1.0 (darwin/arm64) · build <hash>`.
  - [ ] A node left on an **older build** (e.g. a peer still on the pre-1.1
    line) surfaces the loud `version mismatch — update every node to 1.1.0`.

Verify: `./scripts/test-mac.sh` green (note `internal/appcore` unit tests now
include `TestMeshWarning`, which runs on the Mac too and already asserts the
cross-platform-silence case); then the three live checks above against a
Windows 1.1.0 node.

> **Deploy note:** peers still running pre-1.1 builds will show as "runs an
> older build" until redeployed — expected, they predate the `Version`/
> `Platform` link-report fields.
