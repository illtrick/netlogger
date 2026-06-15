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
- `internal/discovery`: each instance periodically **broadcasts** a small announce datagram (`node id`, host, control port, app version) on a fixed UDP discovery port, and **listens** for others. It maintains a live peer table with last-seen timestamps; a peer not heard for a TTL is marked stale/offline (but its accumulated data is retained).
- **Manual add by IP** supplements discovery for networks where broadcast is blocked.
- The discovered peer set feeds probe targets, pull sources, and offset measurement — replacing the static `config` file as the primary source. `config` remains an optional override / persistence of manually-added peers.
- **Symmetric:** there is no coordinator. Every instance probes every peer, pulls every peer's samples, and can aggregate the full mesh locally. Whichever window you look at shows the complete picture it has gathered.

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

1. **Link Matrix (centerpiece):** an N×N grid of every directed link with session metrics — loss %, median/p99 latency, jitter, and drop-episode count. A single bad link is one hot cell; a bad shared device shows a hot row/column or *simultaneous* hot cells across links.
2. **Correlation:** the existing interval-overlap engine classifies whether drops across links are **simultaneous** (shared device — switch/coupler) or **isolated** (one link — cable/port/NIC). Unreliable clocks are excluded (existing behavior).
3. **Components:** the existing BFS path attribution maps a bad link/path onto the **device** most likely responsible, with health + coverage scoring.

The **Faults/Overview** view summarizes the whole session from the compact connectivity-event log + correlation (not raw samples), so it stays fast after 12 hours. Raw samples back the detail drill-downs and export.

## 6. Native UI (Gio) — screens

- **Overview / Faults:** session status, top suspect links/components ranked by accumulated drop evidence, a drop-episode timeline, shared-vs-isolated verdict.
- **Link Matrix:** the N×N per-link grid (section 5.1).
- **Components / Map:** BFS-tiered topology with health + coverage (port of the existing map's information design).
- **Peers:** discovered peers (name, IP, link, last-seen) + manual add by IP.
- **Tests:** **Test all machines** (synchronized full-mesh round) + on-demand point-to-point iperf3 against a chosen peer.
- **Export:** write the analysis bundle to a file.
- **Settings:** data dir, discovery port / control port, shared secret, retention.

UI runs on the Gio event loop; engine state is read through a thread-safe snapshot the UI polls (~1–2 Hz) — no engine work on the UI goroutine.

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

- **Gio on Windows is cgo-free** (uses Direct3D/Win32 via syscall), preserving the project's cgo-free constraint and the single static portable exe. (macOS/Linux Gio needs cgo — deferred with the GUI for those platforms.)
- Build: `go build -ldflags "-H windowsgui -s -w"` plus the embedded `.syso` manifest and the existing embedded iperf3 bundle. `scripts/build.ps1` is updated accordingly.
- `go test -race` remains unavailable (cgo-free); concurrency is guarded by channel/lock discipline as today.

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
