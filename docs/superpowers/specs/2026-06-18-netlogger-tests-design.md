# NetLogger — Tests subsystem design (LAN speed · stress · internet)

- **Date:** 2026-06-18
- **Status:** Approved design → implementation planning
- **Supersedes/extends:** M4 (iperf3 load tests) — reuses `internal/iperf` and the per-agent control plane.

## 1. Goal

Add three operator-driven measurement tools to NetLogger, surfaced in a new **Tests** tab:

1. **LAN speed test** — point-to-point throughput between any two live devices, orchestrated from any device, presented as an N×N **Test Matrix**.
2. **Stress test** — coordinated, mesh-wide load with continuous latency/loss measurement, to *reproduce load-triggered faults* (the origin Moonlight/RDP-drop problem).
3. **Internet speed test** — device↔internet throughput + bufferbloat grade, runnable on any node.

These build directly on machinery that already exists: every agent runs an always-on `iperf3 -s`; the control plane (`controlPort`, Host-allowlisted, empty token) already carries `/api/command` (synchronized reset), `/api/events`, `/api/lossbuckets`; mesh discovery resolves peer id→host→control-addr; M3 provides NTP-style clock offsets; the heatmap already measures continuous per-link loss/latency.

## 2. Locked decisions

| Area | Decision |
|---|---|
| Surfacing | Dedicated **Tests** tab. Top nav `Dashboard / Tests / Events`. Three segmented sub-views: Speed (LAN) / Stress / Internet. |
| Copy style | Plain factual labels only. No editorial commentary, no narrative verdict banners. Numbers/grades speak for themselves. |
| LAN orchestration | Any pair, driven from any device; orchestrator need not be a participant. From/To are device pickers. |
| LAN direction | Default = **Down-then-Up** (one command to the *From* node; `-R` yields the reverse leg). |
| LAN scope | **Test Matrix (N×N) from day one.** Single-pair is one cell; click a cell to drill in. |
| Stress topology | **Full mesh** (every node → every other). |
| Stress intensity | **Per-link rate cap**, default ~200 Mbit/s, adjustable. |
| Stress safety | **Auto-abort on hard link fault** + hard duration cap + manual kill-switch. |
| Internet endpoint | **Cloudflare** free open endpoint for down/up throughput + NetLogger's own ICMP/UDP probes for idle/loaded latency → bufferbloat A–F / RPM grade. Endpoint configurable (Cloudflare default). |
| Internet orchestration | Runnable on any node, driven from anywhere. |

## 3. UI structure

- `internal/ui/ui.go` gains a top-level nav state (`Dashboard | Tests | Events`); the current single scroll becomes the Dashboard view; Events moves under its own tab.
- A new `internal/ui/tests.go` renders the Tests tab: a segmented control (Speed / Stress / Internet) + the active sub-view.
- Dark "ops" palette and existing `card`/`chipLabel`/`sevColor` helpers reused. Severity coloring: ≥900 Mbit/s good, 400–900 watch, <400 bad (LAN); bufferbloat grade A–F maps to good/watch/bad.

## 4. Architecture — one orchestration pattern, reused 3×

**Pattern:** the *orchestrator* (the device whose UI is open) holds transient test-session state, fans commands out to nodes over the control plane, nodes execute locally (run iperf3 / probes), results return to the orchestrator for display. No new always-listening services beyond the existing control-plane mux.

### 4.1 New control-plane endpoints

Mounted on the same mux as `/api/command` in `appcore.Start` (Host-allowlist, empty token — acceptable for a LAN diagnostic tool, consistent with the existing reset command). All injectable for `httptest`.

| Endpoint | Method | Request | Response |
|---|---|---|---|
| `/api/speedtest` | POST | `{target_addr, direction:"down"\|"up"\|"both"\|"bidir", proto:"tcp"\|"udp", streams, duration_s, cap_mbit, port}` | `{down?:Result, up?:Result, error?}` |
| `/api/stress/start` | POST | `{run_id, targets:[addr...], proto, per_link_cap_mbit, duration_s, start_at_unix_us}` | `{node_id, ack:true, error?}` |
| `/api/stress/stop` | POST | `{run_id}` | `{ack:true}` |
| `/api/stress/status` | GET | `?run_id=` | `{run_id, running, started_at, ends_at, links:[{target, sent_bps, retransmits, aborted}]}` |
| `/api/internet` | POST | `{endpoint, duration_s}` | `{down_mbit, up_mbit, idle_ms, loaded_ms, jitter_ms, loss_pct, rpm, grade}` |

`start_at_unix_us` is expressed in the **orchestrator's clock**; each node converts to its own clock using its known M3 offset before scheduling. If a node has no reliable offset, it falls back to "start immediately on receipt" and the orchestrator widens the alignment tolerance (the run is still useful; the heatmap aligns by absolute time as it already does).

### 4.2 `internal/iperf` extensions

Extend `Opts` and `buildArgs` (pure, TDD'd) to add:
- `Streams int` → `-P N` (parallel streams; requires bundled iperf3 ≥3.16 for one-thread-per-stream — **pre-flight check**).
- `Reverse bool` → `-R` (server sends; gives "download" from the client's seat).
- `Bidir bool` → `--bidir` (requires ≥3.7).
- `OmitS int` → `-O N` (warm-up omit, skip TCP slow-start).
- UDP loaded mode already supported via `-u -b`.

`Parse` already yields `Intervals`, `SumBitsPerSec`, `SumRetransmits`, `UDPLostPercent`, `UDPJitterMs`. For `-R` and `--bidir`, add direction-aware extraction (sum_received vs sum_sent) so download/upload are reported correctly. A "both" run executes two sequential client invocations (forward, then `-R`) and returns both `Result`s.

### 4.3 Stress execution on a node

On `/api/stress/start`, the node:
1. Records run state under a new leaf lock (`a.stressMu`): run_id, targets, ends_at, per-target status.
2. At `start_at` (converted to local clock), launches one rate-capped iperf3 client per assigned target (full-mesh ⇒ every other node), each as a managed process.
3. While running, monitors per-target health. **Auto-abort predicate:** if a target link produces a hard fault — a NIC link-down/flap event (existing paired Disconnected→Up detection) or repeated iperf3 client errors against that target — stop that target's load, mark `aborted`, keep others running.
4. Enforces the hard duration cap (`ends_at`); stops all load and clears run state.
5. `/api/stress/stop` (kill-switch) cancels immediately. Process group is killed via the same mechanism `iperf.Server.Stop` uses.

The **live readout is the existing heatmap** — the continuous loss/latency probes already persist via `store.Insert(... udp_iso / __gateway__)`, and `LossHeatByMachine` already renders them. The stress test adds load; the heatmap shows links going red. Minimal new read path: the Stress sub-view embeds the existing heatmap component plus a per-link load+health strip fed by `/api/stress/status` polling.

### 4.4 Internet test on a node

On `/api/internet`, the node:
1. Idle phase: probe latency to the gateway and a public anchor (reuse ICMP probing) → `idle_ms`, jitter.
2. Download phase: pull from the Cloudflare endpoint (HTTP, phased payload sizes) while probing latency → `down_mbit`, `loaded_ms` (down).
3. Upload phase: push to Cloudflare while probing → `up_mbit`, `loaded_ms` (up).
4. Grade: bufferbloat = loaded − idle; map to A–F and compute RPM (round-trips/min). Pure grading helper, TDD'd.

Throughput uses Go's `net/http` against Cloudflare (no cgo, stdlib + existing deps). Latency uses the existing pure-Go ICMP/UDP probing — NetLogger's accuracy advantage over browser tests.

## 5. Persistence & export

- A small `loadtest_results` store table records completed LAN-speed and internet runs (from, to/endpoint, direction, throughput, retransmits/jitter/loss, grade, ts) for history rows and inclusion in the existing JSON export. Stress runs record a run summary (run_id, topology, cap, duration, per-link aborts, peak loss) — the per-second detail already lives in the samples/heatmap store.
- Export (`internal/appcore` export path) extends to include recent test results and stress run summaries.

## 6. Security & safety

- Control plane stays Host-allowlisted on the trusted LAN (no token), consistent with `/api/command`. New endpoints validate inputs (target must be a known peer addr; caps/durations clamped to sane maxima).
- Stress guardrails are mandatory: per-link cap default, hard duration cap (clamped, e.g. ≤10 min), auto-abort on hard fault, always-available kill-switch. Unbounded mode is **not** offered by default.
- A stress run is idempotent per `run_id`; a second `start` for a live run is rejected.

## 7. Testing strategy

cgo-free build ⇒ no `go test -race`; concurrency reasoned by inspection + lifecycle tests, pure logic TDD'd.

**Pure unit (TDD, write tests first):**
- `buildArgs` flag matrix: `-P`, `-R`, `--bidir`, `-O`, UDP `-b`, port — exact argv per Opts combination.
- Direction mapping: parse of `-R` / `--bidir` JSON → correct down/up Result.
- Matrix aggregation + cell coloring thresholds.
- Bufferbloat → A–F grade + RPM computation (boundary cases: A≥400, F, zero-load).
- Stress scheduling: `start_at` clock-offset conversion; per-node target assignment for full mesh; duration→ends_at.
- Auto-abort predicate: given a synthetic fault signal, returns "abort this target, keep others".
- Input validation/clamping for all new endpoints.

**Handler tests (`httptest`, injectable runner — mirrors M4 Task 5/Fix 37):**
- `/api/speedtest` happy path + iperf3-absent + bad target.
- `/api/stress/start|stop|status` lifecycle; reject duplicate run_id; kill-switch stops cleanly.
- `/api/internet` with a stubbed throughput source.

**Lifecycle/concurrency (inspection + `-count=2`):**
- Stress run start→cap-expiry→clean shutdown leaves no orphan iperf3 processes.
- Kill-switch during active load; ctx cancellation on app shutdown stops stress.
- New `stressMu` is a leaf lock: acquired alone, read into Snapshot outside `a.mu` (matches existing `nicMu`/`heatMu` discipline).

**Manual gates (rendering, real hardware):** matrix render + cell drill-in; stress live readout (heatmap red under load) on the real ryzen↔Projector link; internet grade against a known-bufferbloated link.

## 8. Build sequence (matches priority)

1. **LAN speed + Test Matrix** — iperf Opts extensions, `/api/speedtest`, orchestrator fan-out, Tests tab shell + Speed sub-view + matrix + drill-in. Independently shippable.
2. **Stress test** — stress state/endpoints, sync via clock offsets, auto-abort, Stress sub-view (embed heatmap + load/health strip + kill-switch).
3. **Internet test** — Cloudflare throughput + probe-based bufferbloat grading, `/api/internet`, Internet sub-view.

## 9. Pre-flight checks (resolve before/at start of build #1)

- Confirm bundled iperf3 version ≥3.16 (multithreaded `-P`) and ≥3.7 (`--bidir`) at runtime via `iperf.Version()` (footers indicated 3.21 — expected OK; the matrix's parallel-stream accuracy depends on it).
- Confirm Cloudflare's open speed endpoint is reachable and its payload protocol is stable enough to depend on; otherwise fall back to the configurable endpoint field.

## 10. Open questions (non-blocking)

- Exact Cloudflare endpoint paths/payload sizing (resolve during build #3; the phased 100KB→250MB pattern is documented).
- Whether the matrix should auto-run on tab open or stay manual (default: manual, "Run all pairs" button).
