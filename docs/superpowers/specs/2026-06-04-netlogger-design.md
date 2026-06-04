# NetLogger — Cross-Platform LAN Drop Diagnostic Tool

**Design spec — 2026-06-04**

This spec is the validated successor to `network-diagnostic-handoff.md`. It folds in six parallel research passes (sync resilience, probe methodology, cross-host clock correlation, Go service deployment, iperf3 orchestration, SQLite + analysis-bundle format), each of which overturned or refined first-instinct choices. Citations live in §13.

---

## 1. Problem & goal

A home LAN has sporadic, multi-second connection drops. The concrete reproduction is a **Moonlight game stream (UDP) from Ryzen → ProjectorPC stuttering** — pure peer-to-peer LAN traffic crossing an unmanaged switch (Switch 1, the prime suspect). Earlier manual ping testing measured the **gateway path** and missed the problem because it never exercised the **peer-to-peer path under load**.

**Goal:** an **operator-driven** cross-platform tool that you **deploy to your machines, verify is configured correctly, and run tests from** — which runs a probe **mesh between host pairs under controlled load**, **correlates drops across hosts** (simultaneous → shared device; independent → per-host cable/port), distinguishes **LAN-vs-WAN** and **hardware-fault-vs-bufferbloat**, and rolls all of that into a **per-component quality + confidence score** for every part of the network so you know where to focus effort. It survives the very drops it diagnoses and exports a **self-contained bundle for cold AI analysis**.

**Product stance (operator tool, not a wizard):** the user is technically competent and wants to *operate* the tool, not be guided by it. The core loop is **Deploy → Verify configuration → Run tests → Read per-component quality**. There are **no hand-holding flows** — no "what's wrong" culprit wizard, no "confirm & fix" stepper, no guided troubleshooting. The bypass-Switch-1 isolation step still exists, but as an ordinary test you queue and whose before/after the tool compares — not a wizard.

**Primary success criterion:** confirm or rule out Switch 1 as the cause of the load-triggered peer-to-peer drops, with evidence, and distinguish that from bufferbloat or per-host faults — surfaced as a low score on Switch 1 with stated confidence.

**The network under test (actual topology + hardware — see §2a):** Fiber Modem → Wall Jack → Wall Jack → Router (also serving Mac/PC/mobile over **WiFi**) → **Switch 1** (core) → {NAS; ProjectorPC via an inline **Connector**/coupler; **Switch 2** → Ryzen, NCASE}. Every physical element — including the two wall jacks and the connector — is an independently **scoreable component**.

**Topology facts that reframe the problem (from hardware research, §2a):**
- The switches are **Tenda TEM2010F = 2.5G** (8×2.5G + 2×2.5G SFP). **There is no 10G anywhere.** The UGREEN "10G coupler" adds no speed; ProjectorPC's Intel **I219-V negotiates at 1G** regardless. The LAN is **2.5G end-to-end**.
- **Switch 1 is no longer the clear prime suspect.** NCASE's **Intel I226-V** and Ryzen's **Killer E3100G** 2.5GbE NICs have a *well-documented* **Energy-Efficient-Ethernet (EEE) multi-second dropout** bug — an almost exact match for "intermittent drops under load." The endpoints are co-leading suspects with the switch. This is *why* the tool must show evidence/coverage and let the operator isolate, rather than prescribe a culprit.

**Non-goals:** permanent monitoring suite; reading unmanaged-switch counters (impossible — value is inferring faults from endpoint behavior); sub-second hardware clock sync; guided/wizard UX; embedding per-vendor/NIC known-issue logic; **prescriptive "this is the culprit" verdicts** (the tool surfaces measurements + coverage; the operator concludes).

---

## 2. Platforms & priority

Build/harden order: **Windows 11 first → QNAP (QTS) → macOS.**

| Platform | Role | Service mechanism | Notes |
|----------|------|-------------------|-------|
| Windows 11 (Ryzen, NCASE, ProjectorPC) | agent + coordinator | Windows Service via `kardianos/service`, run as `LocalService` (unprivileged) | Ryzen is the likely coordinator |
| QNAP (QTS, BusyBox/ARM) | agent (headless) | **Docker via Container Station `--restart always`** (primary) or QPKG (fallback) | NOT autorun.sh |
| macOS | agent | LaunchDaemon (`KeepAlive=true`) | signing/notarization required for distribution |

---

## 2a. Network config file (topology + inventory as data)

The topology and device list are **not hardcoded** — they live in a **network config file** the agent/coordinator loads and renders the map and component table from. (A later UI will let users enter/edit this; for now it's a hand-written file.) The tool is **generic**: it knows nodes, links, roles, and expected link speeds — nothing vendor-specific.

Config file shape (illustrative):
```yaml
nodes:
  - id: switch1   type: switch    label: "Switch 1"   model: "Tenda TEM2010F"   managed: false
  - id: router    type: router    label: "Router"     model: "TP-Link BE9300"
  - id: connector type: passive   label: "Connector"  model: "UGREEN coupler"
  - id: ryzen     type: endpoint  label: "Ryzen"      nic: "Killer E3100G"  link_speed: 2.5G  role: coordinator
  - id: ncase     type: endpoint  label: "NCASE"      nic: "Intel I226-V"   link_speed: 2.5G
  - id: projector type: endpoint  label: "ProjectorPC" nic: "Intel I219-V"  link_speed: 1G
  - id: nas       type: endpoint  label: "NAS"        nic: "QNAP TS-563"    link_speed: 1G   clock_res: 1s
  - id: wifi      type: cloud     label: "WiFi clients"
links:
  - [modem, walljack1] ; [walljack1, walljack2] ; [walljack2, router]
  - [router, switch1] ; [router, wifi]
  - [switch1, nas] ; [switch1, connector] ; [connector, projector] ; [switch1, switch2]
  - [switch2, ryzen] ; [switch2, ncase]
```
`model`/`nic` are **labels only** — they appear on the map and in the export, but the tool applies no behavior based on them. Each endpoint has both Ethernet and WiFi 6E, so the WiFi path can be added to the file and tested as a separate node when an agent is deployed there.

> **The example network here is the user's actual LAN.** Real-world hardware knowledge (e.g. the Tenda TEM2010F being 2.5G-only so the "10G" coupler adds no speed; documented EEE-dropout behavior on 2.5G NICs like the I226-V/Killer E3100G; the QNAP ~3 AM scheduled task) is **operator context applied by hand**, not logic the tool encodes. The one place it touched the design is the corrected **2.5G end-to-end** speed facts in §1; everything else stays out of the tool.

---

## 3. Architecture

**Control plane = star; data plane = peer-to-peer.** One agent is designated **coordinator** (extra role, same binary): it holds topology, orchestrates *who probes whom and when*, aggregates results, runs correlation, and serves the embedded web GUI. Actual probe traffic (UDP/ICMP/TCP mesh, iperf3 streams) flows **directly host-to-host** — that peer traffic is what crosses Switch 1.

```
┌──────────────────────────────────────────────────────────┐
│ COORDINATOR (one host — likely Ryzen)                      │
│  • embedded web GUI (go:embed SPA)  ── opened in browser   │
│  • orchestrator (probe plans, schedules)                   │
│  • sync puller (cursor-based, idempotent)                  │
│  • correlation engine (interval-overlap)                   │
│  • aggregated SQLite + export builder                      │
└───────▲────────────────────────────▲──────────────────────┘
   resilient sync (HTTP backfill +    │  app-level heartbeats
   WebSocket live tail, token-auth)   │
        │                  │          │
   ┌────┴───┐         ┌────┴───┐  ┌───┴────┐
   │ Agent  │         │ Agent  │  │ Agent  │   ← same binary, agent role
   │ +local │         │ +local │  │ +local │     always-on service
   │ SQLite │         │ SQLite │  │ SQLite │
   └───┬────┘         └───┬────┘  └───┬────┘
       │  peer probe mesh (UDP-isochronous backbone / ICMP / TCP)  │
       └──────── iperf3 load (sequential, port-leased) ────────────┘
              (direct host↔host — the real test traffic)
```

**Why this shape (validated):** local-first durable store + cursor-based idempotent pull is exactly how mature telemetry survives partitions, and it is *more* restart-resilient than in-memory-buffer designs. Pull-with-durable-source makes both agent restart and coordinator restart trivially resumable, and a failed pull is itself a diagnostic signal (the coordinator's vantage point becomes one more probe in the mesh).

---

## 4. The agent

One Go binary. Roles selected by config/flags: `--agent` (default) or `--coordinator` (agent + coordinator services).

**Responsibilities**
1. Run assigned probes (§5), timestamp with a monotonic-derived UTC clock, write to local SQLite immediately.
2. Snapshot endpoint NIC counters around load windows (§5.4).
3. Serve its local data to the coordinator via cursor pull (§7).
4. Participate in the clock-offset handshake (§6).
5. Self-install/uninstall as a service (`netlogger install|uninstall|start|stop`), logic inside the binary (no `.ps1`, dodges execution policy).

**Privilege model**
- Default **unprivileged**. Windows ICMP uses `IcmpSendEcho` (via `pro-bing`) — no admin. TCP/UDP probes need no elevation.
- Optional packet capture (§5.6) spawns a **short-lived elevated helper** (`netlogger --capture-helper`), never elevating the always-on service.

**Paths (gotchas baked in)**
- Windows: binary + data under `%ProgramData%\NetLogger\` — never a user/OneDrive-redirected path. Service runs as `LocalService` (no user profile anyway).
- QNAP: data under a persistent app volume / `/share/Public` if containerized with a bind mount.
- Logs: lifecycle/errors to platform log (Event Log/syslog); rich diagnostics to a file under the data dir.

---

## 5. Probe engine (the corrected core)

**Probe hierarchy (inverted from first instinct — the symptom is UDP):**

### 5.1 Isochronous UDP probe — BACKBONE
Design modeled on `irtt`. Sends on a **fixed cadence regardless of replies** (exposes queue buildup that reply-gated ping hides). Captures:
- one-way delay (OWD) when clocks are offset-corrected; RTT always
- **directional loss** (upstream vs downstream separately — LAN faults are often unidirectional)
- inter-packet delay variation (jitter)

Cadence: **5–10 ms during load/active windows**; **10–20 Hz** baseline; degrade to **1 Hz** for cheap 24/7 background watch. Packet size ~1200–1400B (sub-MTU), bitrate configurable toward the real stream (~10–50 Mbit/s) for the load probe.

Rationale: 1 Hz aliases a 200 ms loss burst into ≤1 missing sample; microbursts are 1–100 ms. A TCP-connect probe hides brief loss behind retransmits — structurally unable to see the fault.

### 5.2 ICMP probe — cheap baseline / liveness
`IcmpSendEcho` on Windows (unprivileged), raw/UDP ICMP elsewhere via `pro-bing`. On a flat L2 LAN, ICMP echo is forwarded by the switch ASIC like any frame (the "ICMP is deprioritized" warning applies to L3 routers with CoPP, not L2 transit), so it's a valid continuous baseline. Store per-probe samples, report **percentiles** (median + spread + loss%), never just averages.

### 5.3 TCP-connect probe — service sanity only
Periodic connect to app ports (SMB 445, Moonlight control). Confirms reachability; **not** a drop detector. Demoted from backbone.

### 5.4 Load testing — iperf3, RRUL-method
- **Shell out to a bundled per-platform official iperf3 binary** (drop `go-iperf`: abandoned, amd64-only, no ARM). Windows: `ar51an/iperf3-win-builds`; QNAP: Entware `opkg`/QPKG, verify `iperf3 --version` at startup; macOS: current arm64+amd64 build. Pin ≥3.16 (multi-threaded). `--json -i 1`.
- **One test per server port** → mesh runs **sequentially** by default (clean attribution); optional bounded concurrency via a **port-pool lease** (servers on 5201..520N, scheduler ensures no port double-books).
- **RRUL envelope:** 5 s idle baseline → ~60 s bidirectional multi-flow saturation → 5 s idle tail. Run the §5.1 UDP probe **concurrently** with saturation — the fault signature is loss/latency spiking on the independent probe *during* the load interval.
- iperf3 JSON lacks absolute interval timestamps → record wall-clock at test start, add interval offset.
- **UDP caveat:** iperf3 UDP is a *load + loss baseline*, not a faithful game emulator (CBR vs VBR). Set `-b <bitrate> -l ~1300`; validate against Moonlight's own overlay as ground truth.

High-signal JSON fields: TCP `retransmits`, `snd_cwnd`, `rtt`, `rttvar`, `bits_per_second`; UDP `lost_percent`, `lost_packets`, `jitter_ms` — captured **per interval**.

### 5.5 Endpoint NIC counters
Snapshot deltas before/after each load window: Windows `Get-NetAdapterStatistics` (+ link speed/duplex); Linux `ip -s link` / `/proc/net/dev`; macOS `netstat -i`. Surfaces CRC errors, RX/TX discards, **duplex/half-duplex mismatch** — fault classes no RTT probe reveals, and the discriminator between endpoint drops and in-network drops.

### 5.6 Packet capture (optional/advanced)
On a detected drop, trigger a `dumpcap`/`tshark` ring-buffer capture (filtered to retransmits/RSTs/dup-ACKs/zero-window) via the elevated helper, if present. Best-effort; absence is handled gracefully.

---

## 6. Clock correlation (interval-overlap, not fixed window)

**Offset measurement (NTP 4-timestamp handshake, per agent, every ~60–120 s):**
- T1 coord send, T2 agent recv, T3 agent send, T4 coord recv.
- `offset = ((T2−T1)+(T3−T4))/2`, `delay δ = (T4−T1)−(T3−T2)`.
- Take **N≈8 handshakes, keep the min-δ sample** (least queuing). Use a **monotonic** clock for δ.
- **Clamp:** `|offset| > 30 s` → mark agent clock UNRELIABLE, exclude from shared-device inference (Jaeger's lesson). Re-measure on reconnect; discard offset across sleep/suspend or a detected wall-clock step.

**Per-event uncertainty interval (Spanner/CockroachDB model):** every failure event carries an interval, not a point:
```
t_corr   = t_local − offset_agent
half_unc = δ/2 + resolution_agent + 50ppm × seconds_since_last_handshake
interval = [t_corr − half_unc,  t_corr + half_unc + drop_duration]
```
`resolution_agent` = **1.0 s for QNAP/BusyBox**, ~1 ms for Windows/macOS. (Treating the QNAP timestamp as a point was the original design's outright bug.)

**Correlation verdict (interval overlap, Allen's algebra):**
- Pre-filter candidate events within a coarse ±10 s window for efficiency.
- Two+ hosts whose intervals **overlap** → **SIMULTANEOUS → shared device (Switch 1 / AP)**.
- A host overlapping no other → **INDEPENDENT → that host's cable/port**.
- Overlap margin < `(half_unc_A + half_unc_B)` → label **"possibly simultaneous, low confidence."**
- Uncertainty terms always rounded **up** (conservative bound: under-estimating causes silent wrong blame; over-estimating only loses discrimination).

---

## 7. Resilient sync protocol

**Local-first, cursor-based, idempotent pull.**

- **Cursor = per-agent monotonic INTEGER** (`(agent_id, seq)` compound key), **never a timestamp** (clock skew/ties create silent gaps).
- Agent commits to local SQLite **before** shipping; coordinator pulls "everything since seq N."
- Coordinator **upsert on `(agent_id, seq)`** (`INSERT … ON CONFLICT DO NOTHING`) — duplicate delivery is a non-event (expected on every reconnect under at-least-once).
- Coordinator **advances its stored cursor only after durable commit**; requests `since = max stored seq`; on reconnect re-reads a **small overlap window** before the high-water mark (cheap off-by-one insurance).
- **Transport:** HTTP batch GET/POST for backfill (each request is its own timeout-bounded liveness check, no half-open zombies); WebSocket only for low-latency live tail when healthy. Durability never depends on the WebSocket.
- **App-level bidirectional heartbeats:** ping ~20 s / expect pong ~10 s / declare dead after 3 misses, then reconnect. (TCP keepalive defaults to ~2 h — useless for a drop-diagnostic tool. The half-open zombie is the #1 resilience trap.)
- **Reconnect:** exponential backoff + jitter (≈5 s → cap ≈30 s).
- **Agent-initiated connection, coordinator-pull semantics:** the agent dials the coordinator (NAT/Wi-Fi friendly), coordinator then pulls "since N" over that channel — push's connectivity with pull's resumable idempotent semantics.

**Disconnection as a first-class signal:** when the coordinator loses an agent, log a connectivity event and, on reconnect, reconcile against the agent's local log:
- agent's own probes also failed during the gap → genuine network drop (feed to correlation).
- agent's probes kept succeeding → only the coord link dropped → different fault domain.

---

## 8. Data model

**Local store (per agent): SQLite, WAL.** ~1–2+ samples/s for hours is 3–5 orders of magnitude below SQLite's limits.

PRAGMAs (set explicitly — never trust driver defaults):
```sql
PRAGMA journal_mode   = WAL;
PRAGMA synchronous    = NORMAL;     -- crash-durable; not power-loss durable (acceptable)
PRAGMA busy_timeout   = 5000;       -- the #1 omission; set on EVERY connection incl. coordinator reader
PRAGMA journal_size_limit = 67108864; -- 64MB cap; truncate WAL after checkpoint
PRAGMA wal_autocheckpoint = 1000;
PRAGMA temp_store     = MEMORY;
PRAGMA auto_vacuum    = INCREMENTAL; -- reclaim via incremental_vacuum, never full VACUUM on hot path
```

Schema (core):
```sql
CREATE TABLE probe_samples (
  seq         INTEGER PRIMARY KEY AUTOINCREMENT,  -- sync cursor
  ts_unix_us  INTEGER NOT NULL,    -- UTC epoch microseconds
  probe_type  TEXT NOT NULL,       -- 'udp_iso' | 'icmp' | 'tcp_connect'
  src_host    TEXT NOT NULL,
  dst_host    TEXT NOT NULL,
  direction   TEXT,                -- 'up' | 'down' | 'rtt'
  rtt_us      INTEGER,             -- NULL = loss/timeout (NEVER a sentinel like -1/0)
  jitter_us   INTEGER,
  lost        INTEGER              -- 0/1 for this datagram, where applicable
);
CREATE INDEX idx_probe_ts        ON probe_samples(ts_unix_us);
CREATE INDEX idx_probe_target_ts ON probe_samples(dst_host, ts_unix_us);
-- plus: iperf_intervals, nic_counter_snapshots, connectivity_events, clock_offsets, topology
```
Loss = **NULL rtt**, never a sentinel (sentinels silently corrupt averages — and mislead the analyzer).

**WAL-growth guard (#1 live risk):** keep coordinator reads short and transactions closed promptly; alert if WAL > 2× DB size. A long-held coordinator read transaction starves checkpoints and grows the WAL without bound.

**Coordinator store:** aggregated SQLite mirroring agent rows keyed `(agent_id, seq)` + derived correlation/event tables.

---

## 9. Correlation engine & classifiers

Real-time successor to `overlap.ps1`. Pipeline:
1. Ingest synced samples; detect per-path failure events (consecutive losses / threshold breaches) with start/end + duration.
2. Build uncertainty intervals (§6); run interval-overlap correlation → simultaneous vs independent, with confidence.
3. **LAN-vs-WAN classifier:** every host probes gateway (`192.168.0.1`) + external (`1.1.1.1`) alongside peers. Gateway-failing → LAN; only-external-failing → WAN/ISP.
4. **Bufferbloat-vs-fault classifier** (during load windows):

| Signal during load | Verdict |
|---|---|
| Latency/OWD climbs to a smooth plateau, little loss, drains when load stops | **Bufferbloat** (queue at a bottleneck, often router uplink — *exonerates the switch*) |
| Baseline latency stays low, discrete loss bursts / RTT spikes, no smooth ramp | **Hardware/link fault** |
| Loss tracks NIC `discards`/`errors`/CRC incrementing at an endpoint | **Endpoint/NIC** |
| Loss only on peer↔peer pairs transiting the suspect switch, scales with throughput, no ramp | **The unmanaged switch (the hypothesis)** |

5. **Topology-aware localization:** attribute mesh results to shared segments — pairs that traverse Switch 1 vs pairs that bypass it. Loss isolated to the shared segment localizes the fault even without switch counters.

### 9a. Per-component health + test coverage (the headline output)

The correlation/classifier results roll up into a per-component view — the primary thing the operator reads. It does **not** prescribe a culprit. It shows, for every component (modem, each wall jack, router, WiFi segment, both switches, connector, NAS, each endpoint NIC), **two independent axes**:

- **Health** — `GOOD | FAIR | POOR | UNTESTED` — what testing found, with a **measured-vs-inferred** flag (endpoints measured directly via own probes + NIC counters; unmanaged switches and passive parts inferred from the paths that cross them).
- **Test coverage / robustness** — `NONE | LIGHT | PARTIAL | THOROUGH` — *how well exercised* this component is, scored from: is any agent/path covering it; how many **independent paths** cross it; tested **under load** vs idle-only; **both directions**; sufficient duration. This is the axis the operator uses to judge how much to trust the health verdict and where coverage gaps remain.

**Segment coverage by endpoint inference (the core isolation mechanic).** The network is modelled as nodes **and links (segments)**. Every peer test exercises the *path* between two agents — i.e. every segment along it. A **segment is considered covered when both its endpoints are thoroughly tested and peer traffic has crossed it**: a fault on that segment would have shown up in the endpoint-to-endpoint result, so good endpoints + clean cross-traffic ⇒ the cable/segment between them is proven good *by inference* (e.g. Ryzen↔NCASE both thorough + clean ⇒ the Switch-2 segment is covered without a probe sitting on the switch). Coverage accumulates per segment across all crossing tests; health is inferred from the verdicts of those tests. A segment crossed only by *failing* paths, whose endpoints are individually clean, is what isolates the fault to that segment — and the UI shows exactly that, rather than naming it for the user.

**Honesty rules:** confidence in an inferred health verdict is capped by the weakest input — coarse clocks (NAS ±1 s), few corroborating paths, idle-only coverage. `UNTESTED` components are surfaced specifically so the operator can extend coverage (deploy an agent to a WiFi client, queue a load test across a thin segment). The tool reports measurements and coverage only; it does **not** attach vendor/NIC known-issue verdicts — interpretation is the operator's.

---

## 10. GUI (embedded web)

Coordinator serves a `go:embed` SPA; opened in any browser (works against headless QNAP by hitting its IP). **Operator-first, no wizards.** Views:

1. **Overview — component metrics table (landing).** A dense, analytical table — components identified by **name + model** (from the config file), then the key metrics per component: **health** (color tag), **coverage** (meter), loss-under-load, p95-under-load, jitter, RTT, failing/total paths crossing it, NIC errors. No prose, no "focus here" — just the numbers. A single header stat line reports coverage %, isolated yes/no, failing-path count, LAN speed, clock tolerance.
2. **Network map.** The real topology rendered with each node coloured by health and carrying a coverage meter + model label; **link weight encodes segment coverage** (thin dashed = untested, thick solid = exercised under load), failing paths highlighted, segments covered by inference shown distinctly. Same model as the board, visualized.
3. **Agents (deploy).** Per-machine deployment + lifecycle: install status, service running, version, last-seen, NIC under test; per-platform install artifacts (Windows self-install `.exe`, QNAP Docker compose, macOS signed `.pkg`); deploy/update actions. Notes that each PC's WiFi 6E adapter is a separately-deployable path.
4. **Configuration (readiness).** Generic per-machine checks the operator clears before results are trustworthy: service running, reachable by coordinator, clock sync within tolerance, probe ports open (firewall), iperf3 present & correct version, data dir writable, link speed matches the config, probe role/targets assigned. Each failure lists its effect on results + a fix action. These checks are **device-agnostic** — the tool does not embed per-NIC/per-vendor known-issue logic (that's operator knowledge, deliberately out of scope).
5. **Tests.** Continuous mesh status/schedule (probe types, rates, targets) + on-demand load tests (pick a path, RRUL profile, run) + a test queue (including the bypass-Switch-1 re-test as an ordinary queued item with auto before/after compare).
6. **Activity.** Latency/loss over time per path with a healthy-range band and drops marked; raw numbers behind a collapsed "technical numbers" expander.
7. **Report.** One click → analysis bundle (§11), including the per-component scores.

Plain-language labels with the real term one step away (e.g. "stress test" / iperf3 saturation); technical depth via collapsed expanders, not a separate mode.

---

## 11. Analysis bundle (for cold AI analysis)

`netlogger-session-<UTC-timestamp>.zip` — **two-tier**: small summary the analyzer reads first, full raw it opens only to confirm.

| File | Contents | Read first? |
|---|---|---|
| `README.md` | what this is, how to read it, "start with summary.json" | yes |
| `manifest.json` | self-describing schema: every field's meaning, **unit**, type, **null = loss** semantics, timezone (UTC), tool/bundle version | yes |
| `summary.txt` | generated plain-language narrative: topology, what failed, when, suspected faults (the auto-written successor to the handoff doc) | yes |
| `summary.json` | rollups + detected events + per-agent clock-offset/NTP metadata + LAN/WAN + bufferbloat/fault verdicts + iperf3 interval summaries | yes |
| `component-scores.json` | per-component health + coverage (§9a) with evidence and measured/inferred flag | yes |
| `coverage.json` | per-segment coverage incl. inferred-from-endpoints links, and which tests exercised each | yes |
| `network-config.json` | the network config file (§2a): topology + device labels (models/NICs) + expected vs measured link speeds | yes |
| `events.csv` | all connectivity + anomaly events with raw timestamps | yes |
| `rollups.csv` | per-10s/min per-path aggregates: min/avg/p95/max RTT, loss% | yes |
| `raw/<agent>.parquet` (or `.csv.gz`) | full raw samples, UTC µs | on demand |
| `iperf3/<agent>.json` | native iperf3 JSON | on demand |

Principles: summary-first/raw-last; CSV for bulk (~50% fewer tokens than JSON); raw windowed to **±60 s around detected events** for the reading tier (full fidelity stays in Parquet); **UTC everywhere**; explicit clock-offset caveat ("cross-host ordering trustworthy only to ~ms; QNAP to ~1 s") so the analyzer never over-claims causality.

---

## 12. Build milestones (Windows-first)

1. **M1 — Windows core (single host, vertical slice).** Go skeleton; SQLite store + PRAGMAs/schema; ICMP (`pro-bing`/`IcmpSendEcho`) + isochronous UDP probe; `install`/`uninstall` as Windows Service (`LocalService`); the **Agents** view showing this machine's install/service/version status. Proves the probe engine + store + self-installing service on the priority platform.
2. **M2 — Deploy + configuration readiness across Windows hosts.** Coordinator role; agent↔coordinator resilient sync (heartbeats, backfill, idempotent upsert); the **Configuration** readiness checks (service, reachability, clock-sync, firewall ports, iperf3 presence, data dir, role/targets) with fix actions; embedded web GUI shell + Agents/Config views. Gets the operator to a trustworthy, correctly-configured multi-host setup — the explicit "deploy then make sure config is right" step.
3. **M3 — Mesh, correlation, and the component quality board.** Clock-offset handshake; interval-overlap correlation; **per-component quality + confidence scoring (§9a)**; the **Overview** board + **Network map** + **Activity** timeline. Reproduces the multi-host test on Windows and turns it into the headline per-component scores.
4. **M4 — Load + classifiers.** Bundle/shell iperf3; RRUL envelope with concurrent UDP probe; NIC-counter snapshots; bufferbloat-vs-fault + LAN/WAN classifiers feeding the scores; **Tests** view (continuous mesh + on-demand load + queue, incl. bypass-Switch-1 re-test with auto before/after compare).
5. **M5 — QNAP agent + Report.** Docker image via Container Station (`--restart always`); iperf3 via Entware; BusyBox 1 s timestamp resolution flowing through scoring as capped confidence; full analysis-bundle export incl. `component-scores.json`.
6. **M6 — macOS agent + packet capture.** LaunchDaemon; signing/notarization pipeline; optional `dumpcap` ring-buffer hook via elevated helper; lets a WiFi client join the mesh so that segment stops being `UNTESTED`.

Each milestone is independently useful; M1–M4 on the Windows boxes deploy, verify, test, and score the components that reproduce the stutter — enough to answer the Switch-1 question.

---

## 13. Key references (real-world validation)

- **Sync/resilience:** Prometheus remote-write WAL; OpenTelemetry Collector persistent queue + retry; Kafka idempotent producer (monotonic seq + upsert); WebSocket heartbeat/zombie-detection guidance; SQLite durability (`synchronous` semantics).
- **Probe methodology:** `irtt` (isochronous UDP, OWD, directional loss); bufferbloat.net RRUL spec & Flent; Moonlight issue #724 (clean iperf3 but stutters); microburst literature (cPacket/ntop/qacafe); ICMP-CoPP applies to L3 not L2 transit.
- **Clock correlation:** Mills/NTP 4-timestamp offset & δ/2 error bound; Google Spanner TrueTime uncertainty intervals; CockroachDB static-max-offset + uncertainty-interval restart; Jaeger clock-skew clamp; Allen's interval algebra.
- **Go service:** `kardianos/service` (v1.2.4, recovery options in mainline); Windows `IcmpSendEcho` unprivileged + `prometheus-community/pro-bing`; QNAP QPKG/QDK vs Container Station `--restart always` vs autorun.sh wipe risk; macOS LaunchDaemon + notarytool.
- **iperf3:** ESnet multithreading ≥3.16 but one-test-per-server-port; `ar51an/iperf3-win-builds`; UDP `-b`/`-l` burst artifacts; interval timestamps absent from JSON.
- **SQLite + bundle:** SQLite WAL docs + checkpoint-starvation post-mortem; production PRAGMA sets; HAR self-describing format; CSV-vs-JSON token efficiency; OTel clock-skew export guidance.
- **Hardware context (§2a — operator background, NOT tool logic):** informed only the corrected 2.5G-end-to-end speed facts. Tenda TEM2010F = 2.5G, no 10G (ServeTheHome review); Intel I225/I226-V + Killer E3100G EEE-dropout saga and I219-V EEE/power disconnects (Intel Community/KB, Guru3D, Tom's Hardware, club386, ASRock FAQ); TP-Link BE9300 firmware/QoS (TP-Link forum); QNAP TS-563 drop + driver/QTS mismatch (QNAP forum); passive 10G coupler CRC→downshift; Calix C5500XK = WAN-side ONT. These remain the operator's to apply by hand.
