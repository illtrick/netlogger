# NetLogger Portable — Design Spec

Date: 2026-06-15
Status: Approved for planning (supersedes the service/web-UI model of `2026-06-04-netlogger-design.md` for the presentation + lifecycle layers; the diagnostic engine is reused).

## 1. Goal & scope recalibration

NetLogger is a LAN diagnostic tool that isolates intermittent, load-triggered network drops by correlating per-link measurements across multiple machines (original symptom: Moonlight stutter Ryzen→ProjectorPC across an unmanaged switch + 2.5G coupler).

This spec **recalibrates the product** to a single, self-contained, **portable native desktop app**:

- **Discrete native UI** (Gio) — **no web UI, no browser.**
- **Single portable `.exe`** per platform — **no installer, no long-lived service.**
- **No terminal** — the user never runs scripts or commands; everything is in the app.
- **Self-elevating** — the exe carries a `requireAdministrator` manifest so it has the permissions it needs (firewall, sockets, NIC stats) from a single UAC prompt.
- **Copy-to-run on each machine** — the same exe runs on every PC; instances **auto-discover** each other and form a **symmetric peer mesh** (no coordinator role).

The **diagnostic engine is reused unchanged**: `probe`, `store`, `mesh` (agent API / puller / offsets / auth), `correlate`, `score`, `classify`, `iperf` (bundled iperf3 + always-on server), `sysinfo`, `readiness`. What changes is the **presentation layer** (native UI replaces the web SPA) and the **lifecycle layer** (in-process app replaces the kardianos service).

### Non-goals
- No Windows service / installer / auto-start. The app runs only while open.
- No human-facing web server (the machine-to-machine HTTP sync API stays — it is data plumbing, not a UI).
- No push-to-peer deploy (you copy the exe; discovery replaces config push).
- macOS/Linux GUI is deferred (Windows-first). The engine already cross-compiles; a headless mode for QNAP remains possible later.

## 2. The user's session workflow (acceptance narrative)

1. Launch the app on 2+ computers → one UAC prompt each → a native window opens on each.
2. Watch **auto-discovery**: each instance's Peers panel fills in as it hears the others on the LAN.
3. Click **Test all machines** on any instance → a **synchronized full-mesh load round** runs: every machine loads every other machine *at once*, while all instances keep passively probing.
4. **Minimize to taskbar** and leave it for **12+ hours** unattended; the engine keeps running while the UI is not drawing.
5. **Glance in periodically**: the **Link Matrix** and **Faults** view summarize the whole session — which link(s) are bad, and whether faults are shared-device or link-isolated.
6. Click **Export** to write an analysis bundle to a file to share for deeper analysis.

## 3. Architecture

### 3.1 Process model
The app process **is** the agent, in-process. On launch (elevated):
1. Resolve the **portable data dir** (`NetLogger-data` next to the exe; fall back to `%LOCALAPPDATA%\NetLogger` if the exe dir is read-only). Open the SQLite store (WAL).
2. Extract bundled iperf3 (existing `iperf.Bootstrap`) and start the always-on iperf3 server (existing `iperf.StartServer`).
3. Start the **discovery** service (UDP broadcast announce + listen).
4. Start the **sync API** HTTP server on a control port (existing `mesh.AgentAPI` routes: `/api/info`, `/api/samples`, `/api/time`) — machine-to-machine only, token-guarded as today.
5. Start the engine loops: probe (ICMP+UDP) to the live peer set, puller (cursor-based aggregation from peers), offset (clock handshake) — driven by the **discovered** peer set rather than a static config.
6. Render the **Gio UI**.

On window close / Quit: cancel all loops, stop the iperf server, stop discovery, shut the sync API, `wg.Wait()`, close the store. Nothing persists; nothing is installed.

Minimize → the OS window is minimized to the taskbar; the engine goroutines are independent of the UI frame loop and keep running. This is the 12-hour-session mode (no special "background" state — minimized is just not drawing).

### 3.2 Peer set: discovery-driven, symmetric
- `internal/discovery`: each instance periodically **multicasts** a small announce datagram (`node id`, advertised IPs, control port, app version) on a **private app multicast group `239.255.x.y` (administratively-scoped, RFC 2365), TTL=1**, and **listens** for others. We deliberately do **not** reuse mDNS `224.0.0.251:5353` — a private group avoids collisions with the host OS's own mDNS responder (Bonjour/Avahi/Windows). It maintains a live peer table with monotonic last-seen timestamps; a peer not heard for ~3× the announce interval is marked stale/offline (data retained).
- **Announce cadence:** a 3-datagram burst (~250 ms apart) on startup so peers learn each other fast despite UDP loss, then a 2–5 s heartbeat; a graceful "bye" on clean shutdown for fast removal.
- **Identity:** a stable random **UUID persisted in the data dir** (not keyed on IP/hostname — DHCP churn and multi-homing cause duplicate-peer bugs). Dedup the peer table by UUID; when the same UUID arrives from a new IP, update the row. Prefer the UDP **source address** over self-reported IPs when they disagree.
- **Multi-NIC handling (Windows):** bind the receive socket to the **wildcard `0.0.0.0`** (required on Windows to receive multicast), set `SO_REUSEADDR` before bind, enumerate interfaces and **JoinGroup on each real, up, multicast-capable NIC** (skip Hyper-V/WSL/VPN virtual adapters); `SetMulticastInterface` per send so announces egress every real NIC; filter out our own UUID on receive.
- **Manual add by IP is a first-class path, not just a fallback:** on multicast-hostile networks (AP client isolation, VLANs without a reflector, managed switches) the user adds a peer by IP and we talk **unicast** to its control port directly. This is the reliable path the field reality demands.
- The discovered peer set feeds probe targets, pull sources, and offset measurement — replacing the static `config` file as the primary source. `config` remains optional persistence of manually-added peers.
- **Symmetric:** there is no coordinator. Every instance probes every peer, pulls every peer's samples, and aggregates the full mesh locally. Whichever window you look at shows the complete picture it has gathered.

### 3.3 Identity & security
- Each instance derives a stable `node id` (hostname-based, deduped) and announces it.
- The control plane keeps the existing bearer-token + Host-allowlist middleware. For a zero-config LAN tool, the token is derived from a **shared LAN secret** (e.g., a value entered once, or a default-open mode on trusted LANs with a visible warning) — carried over from the existing `httpauth`. Loopback stays exempt.
- The self-elevation (admin manifest) lets the app open its own firewall rules for the control port, discovery port, iperf3 server, and UDP-load ports (existing `iperf.ensureFirewallPort` pattern, generalized).

## 4. Synchronized full-mesh load round ("Test all machines")

Goal: stress **every directed link at once** so a single bad link, or a shared device, is observable under load.

- **Mechanism = our own UDP load generator**, not iperf3. Rationale: a single `iperf3 -s` serves one client at a time, so true all-pairs concurrency would need a server port per flow; our isochronous UDP path already handles arbitrary concurrent peers and yields per-link loss directly. iperf3 remains available for **on-demand point-to-point throughput** on a single link the user wants to scrutinize.
- **Orchestration:** the initiating instance computes a common **start time `T`** (now + small lead, e.g., 2 s) and broadcasts a "load round" command to every peer over the sync API, including `T`, duration `D`, and target bitrate. Each instance translates `T` into its own clock using the **already-measured offset**, then at `T` begins sending the UDP load to **every other peer** simultaneously for `D` seconds.
- During the round, the normal 1 Hz probes continue, and each receiver records per-link loss/latency/jitter; drop episodes are written to the connectivity-event log.
- **Result:** a clock-aligned window where all links + the shared switch are loaded together — the condition that reproduces load-triggered drops — captured per link.
- Bounds/safety: duration clamp, bitrate clamp, node allowlist (existing load-test hardening patterns apply).

## 5. Fault analysis: three levels

1. **Link Matrix (centerpiece):** an N×N grid of every directed link. **Rows = source, columns = destination**, with the directionality labeled explicitly on screen. We keep **A→B and B→A as separate cells (full matrix, not triangular)** — directional asymmetry is diagnostic gold (one-way loss points at a NIC TX path or duplex mismatch). The **diagonal (A→A) is hatched/disabled**. Default cell metric is **packet loss %** (the unambiguous "broken" signal); a toggle recolors to median/p99 latency, jitter, or drop-episode count. Host ordering is **stable across sessions** (spatial memory); we do **not** re-sort the matrix by severity — severity ranking lives in a separate Top-Suspects list (§6). Each cell never collapses 12 h into one number alone (that hides episodic faults) — it pairs the value with a micro-sparkline and an episode badge.
2. **Correlation:** the existing interval-overlap engine classifies whether drops across links are **simultaneous** (shared device — switch/coupler) or **isolated** (one link — cable/port/NIC). Episode detection uses **hysteresis** (enter "drop" at ≥ threshold for ≥2 intervals, exit below a lower threshold) so a stray packet doesn't flap. Unreliable clocks are excluded (existing behavior).
3. **Components:** the existing BFS path attribution maps a bad link/path onto the **device** most likely responsible, with health + coverage scoring.

**The headline differentiator** (no consumer tool does this): because we control the full small-mesh topology, we **automatically infer the shared device** — "all degraded links traverse SW1 → suspect SW1" vs "only host-C's outbound links degraded → suspect host-C NIC/cable" — and surface it as a plain-language verdict.

The **Faults/Overview** view summarizes the whole session from the compact connectivity-event log + correlation (not raw samples), so it stays fast after 12 hours. Raw samples back the detail drill-downs and export.

### 5.1 Severity color encoding (accessibility)
Discrete **severity bands**, never a continuous rainbow ramp, double-encoded with **color + shape + number** (≈8% of men have color-vision deficiency; red/green alone fails them). Default thresholds, **user-editable** (LAN baselines vary):

| Band | Default trigger (loss %) | Color (Wong/IBM CVD-safe) | Shape |
|---|---|---|---|
| Good | < 0.1% | `#009E73` bluish-green | ● |
| Warn | 0.1–1% | `#E69F00` orange | ▲ |
| Bad | ≥ 1% | `#D55E00` vermillion | ✕ |
| No data | — | `#999999` gray | — |

Latency uses a separate single-hue blue saturation ramp so "slow" isn't confused with "lossy."

## 6. Native UI (Gio) — screens

Three levels of progressive disclosure: **glance → which link → when/why.**

- **Overview / Faults (default landing; the minimized-glance screen):** a single big **global verdict chip** ("ALL HEALTHY ●" / "2 LINKS DEGRADED ▲" / "1 LINK FAILING ✕" — this is also what a future taskbar badge/title reflects); a **Top-Suspects ranked list** (worst links/inferred devices by composite of loss × episodes × recency — *not* the matrix re-sorted); the **shared-device inference** sentence (§5); a **"since you last looked"** delta ("since 2:14 PM: +4 episodes on host-C→host-D"); session elapsed + load-test status. No raw grid here — it must answer "is it bad / where / what kind" in <2 s.
- **Link Matrix:** the N×N per-link grid (§5/§5.1) with the metric toggle and per-cell color+shape+number+sparkline+episode badge; hover tooltip = all metrics; A→B vs B→A asymmetry visible.
- **Timeline / Correlation:** one horizontal lane per directed link on a shared session time axis, **lanes grouped by shared device** so a switch fault appears as a contiguous **vertical streak** across lanes (simultaneous) while a single-link fault is one lane; auto-detected simultaneous-drop clusters annotated ("12 links dropped at 03:14 for 40 s — shared-device event"). On-screen legend: "vertical streak = shared device · single lane = one link/cable."
- **Components / Map:** BFS-tiered topology with health + coverage (port of the existing map's information design).
- **Peers:** discovered peers (name, IP, link, last-seen) + manual add by IP.
- **Tests:** **Test all machines** (synchronized full-mesh round) + on-demand point-to-point iperf3 against a chosen peer.
- **Export:** write the analysis bundle to a file.
- **Settings:** data dir, discovery/control ports, severity thresholds, colorblind-palette toggle, shared secret, retention, "remove firewall rules" action.

UI runs on the Gio event loop (which **blocks idle**, ~0 CPU between events). Engine goroutines own all state; the UI reads a **thread-safe snapshot** and redraws only on `w.Invalidate()` after data changes (~1 Hz, coalesced) — never engine work on the UI goroutine, never widget state created inside the frame loop. Static scene parts (axis labels, unchanged matrix cells) are cached via `op.Record`. Navigation/tables use `gioui.org/x/component`; the matrix grid uses `gioui.org/x/outlay.Grid` (virtualized).

## 7. Data & portability

- **Store:** existing SQLite WAL store. A 12-hour multi-peer session is tens of MB — acceptable. Auto-checkpoint already configured. Optional retention/rollup is a later enhancement, not required for v1 (the fault summary already reads the compact event log, not raw samples).
- **Location:** portable `NetLogger-data` next to the exe; fall back to `%LOCALAPPDATA%\NetLogger`; user-changeable in Settings (existing `localsettings`).
- **Export bundle (`internal/export`):** a single file (JSON, optionally zipped) containing session metadata, per-link metrics, connectivity events, correlation groups, component scores, classify output, and sysinfo — everything needed for off-box analysis. This satisfies the original "produce an output I can upload for analysis" requirement, now first-class.

## 8. Components: reused / new / retired

**Reused unchanged:** `probe`, `store`, `mesh` (agentapi, puller, offsets, auth), `correlate`, `score`, `classify`, `iperf` (bundle + server), `sysinfo`, `readiness`.

**New:**
- `internal/discovery` — UDP broadcast announce/listen + peer table (+ manual add).
- `internal/ui` — Gio screens (section 6) + a thread-safe view-model snapshot.
- `internal/app` — in-process controller: wires store/iperf/discovery/sync-API/engine-loops/UI lifecycle (replaces the service `Program`).
- `internal/loadround` — synchronized full-mesh UDP load orchestration (initiate, schedule at `T`, run, bound).
- `internal/export` — analysis bundle writer.
- Windows manifest resource (`requireAdministrator`) embedded via a `.syso` (build step; e.g., `goversioninfo`/`rsrc`).

**Retired:** `internal/web` (web UI + SPA), `internal/agentsvc` (service Program), `internal/svcctl` (service control), `internal/launch` (browser), push-to-peer/download endpoints, and the `kardianos/service` dependency. `config` is retained but demoted to optional override/persistence.

## 9. Build & toolchain

- **Gio on Windows is cgo-free** — confirmed by the official install docs ("no CGo for Windows support"); it renders via Direct3D 11. Build with `CGO_ENABLED=0` for a single static portable exe. (macOS/Linux Gio needs cgo — deferred with the GUI for those platforms.)
- **Manifest/version/icon resource:** generated with `github.com/josephspurrier/goversioninfo` via `//go:generate`, embedding an `app.exe.manifest` that requests **`requireAdministrator`** (the app cannot function without admin, so not `highestAvailable`), plus `dpiAwareness=PerMonitorV2,system`, the Win10/11 `supportedOS` GUID, and `longPathAware`. The resulting `resource.syso` sits in the main package and is auto-linked; it must match `GOARCH` (regenerate per arch). The manifest and `-H windowsgui` are orthogonal and compose cleanly; `//go:embed` (iperf3 bundle, icons) is independent of the syso.
- **Single instance:** a named mutex (`golang.org/x/sys/windows` `CreateMutex`, `Local\` scope) acquired early in `main`; on `ERROR_ALREADY_EXISTS`, exit (raising/focusing the existing window is later polish).
- **Logging:** `-H windowsgui` discards stdout, so logs go to a **file in the data dir** (rolling), not the console.
- **Firewall provisioning:** an elevated, idempotent `netsh advfirewall firewall` step — **delete-then-add** under a namespaced rule name, scoped to the **program path**, `profile=any` (so a "Public" network reclassification doesn't silently drop discovery/sync); plus an explicit **"remove firewall rules"** action in Settings (the one piece of persistent machine state a portable app leaves).
- Build: `go generate ./... && go build -ldflags "-H windowsgui -s -w"` with the embedded `.syso` + embedded iperf3 bundle. `scripts/build.ps1` updated accordingly.
- `go test -race` remains unavailable (cgo-free); concurrency is guarded by channel/lock discipline (the validated Gio pattern: engine-owns-state → snapshot under lock → `Invalidate`).

## 10. Milestones (each gets its own implementation plan)

- **N1 — App shell + lifecycle + permissions:** Gio window, embedded admin manifest, in-process engine for a single machine (probe self/loopback, store, iperf server), portable data dir, minimize-to-taskbar, clean quit. De-risks Gio + manifest + lifecycle first.
- **N2 — Native UI screens:** Overview/Faults, Link Matrix, Components map, Peers, Tests, Export, Settings — reading the engine via the view-model snapshot.
- **N3 — Auto-discovery + symmetric peers:** UDP broadcast discovery, manual add, config-less mesh feeding probe/pull/offset.
- **N4 — Synchronized full-mesh load round:** clock-aligned all-pairs UDP load + bounds.
- **N5 — Analysis export bundle.**
- **N6 — Retire** `web`/`agentsvc`/`svcctl`/`launch`/`kardianos` + deploy; final portable-build packaging.

## 11. Risks & open points

- **Gio learning curve / UI effort:** immediate-mode; the Link Matrix and timeline are custom-drawn. Mitigation: N1 de-risks the toolchain before building screens; the information design is already settled (port from the existing dashboard/mockup).
- **Discovery on segmented networks:** UDP broadcast may be blocked across VLANs/managed switches. Mitigation: manual add by IP fallback (included).
- **System tray vs taskbar:** the user specified *taskbar* (minimized window), which Gio supports natively; a true notification-area tray icon would need extra cgo-free Win32 work and is **out of scope** for now.
- **Admin manifest UX:** a UAC prompt on every launch. Accepted trade-off for self-contained permissions per the design decision.
- **Multi-instance per host (dev/testing):** discovery + ports assume one instance per machine in production; tests parameterize ports for loopback multi-instance.
- **SmartScreen / antivirus on an unsigned self-elevating exe (real-world friction):** an unsigned downloaded exe trips "Windows protected your PC"; the combination of self-elevation + named mutex + `netsh` firewall edits + NIC-stat reads reads as "suspicious" to AV/EDR heuristics; Win11 Smart App Control can block unsigned exes outright; unsigned reputation resets every build. Mitigation: **code-sign** (Microsoft Artifact Signing, ~$10/mo, CI-friendly; note EV certs no longer give instant SmartScreen reputation), keep firewall rules narrow/named, and tell early users they'll see one prompt and should verify the publisher. Signing/distribution is out of scope for the initial milestones but tracked as a release prerequisite.

## 12. Research provenance

The technical choices in §3.2 (multicast discovery), §5/§5.1 (matrix + CVD-safe color), §6 (screen hierarchy), and §9 (manifest/single-instance/firewall) are grounded in a 2026-06-15 best-practices review across four areas — Gio architecture, portable self-elevating Windows Go apps, LAN peer discovery, and network-diagnostic UX — validated against primary sources (gioui.org docs, Microsoft Learn, goversioninfo, RFC 2365/6762, Brendan Gregg's latency-heatmap work, Smokeping/Grafana, and the Wong/IBM colorblind-safe palettes). Notable validated facts: Gio is cgo-free on Windows and its render loop pauses while minimized (background goroutines keep running — the 12 h model); roll-your-own UDP multicast on a private group beats mDNS for a single-vendor mesh; and automated shared-device inference on a known small mesh is the feature no existing consumer tool provides.
