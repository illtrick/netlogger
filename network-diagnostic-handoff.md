# Network Diagnostic Tool — Handoff & Findings

This document captures (1) the network problem under investigation, (2) what diagnostic work has been done and what it revealed, and (3) requirements for a cross-platform GUI app to drive this diagnosis going forward. Hand this to Claude Code to build the app.

---

## 1. The Network

### Topology

```
Fiber Modem ── Wall Jack ── Wall Jack ── Router (192.168.0.1)
                                              │
                                          Switch 1 ──── NAS (QNAP)
                                              │
                              ┌───────────────┼───────────────┐
                          HTPC      Switch 2
                          (via inline       │
                           10G coupler)     ├──── Desktop-A (Windows 11)
                                            └──── Desktop-B (Windows 11)
```

Key paths:
- **Desktop-A → HTPC** traverses: Desktop-A → Switch 2 → Switch 1 → HTPC (pure LAN, peer-to-peer).
- **Anything → NAS** traverses Switch 1.
- **Switch 1 is the most-shared element** for all cross-device (peer-to-peer) traffic.
- Both switches are **unmanaged** — no logs, no per-port error counters. This is the core diagnostic handicap.
- HTPC's run includes an **inline 10G coupler**.

### Devices
| Name | OS | Connection | Notes |
|------|----|-----------|-------|
| Router | — | gateway | LAN IP `192.168.0.1` |
| Switch 1 | unmanaged | core | prime suspect |
| Switch 2 | unmanaged | downstream of Switch 1 | feeds Desktop-A + Desktop-B |
| Desktop-A | Windows 11 | Switch 2 | OneDrive-redirected Desktop (see gotcha below) |
| Desktop-B | Windows 11 | Switch 2 | |
| HTPC | (Windows, assumed) | Switch 1 via 10G coupler | Moonlight stream target |
| NAS | QNAP (QTS, Linux/BusyBox base) | Switch 1 | SSH-accessible; `screen` available |

> **IPs still needed:** HTPC and NAS LAN IPs were not captured. The app should auto-discover or prompt for these.

---

## 2. The Problem

**Symptom:** Sporadic connection drops lasting a few seconds, "every little while." Sustained throughput and general uptime are excellent — only brief, intermittent dropouts.

**Affected:** Originally noted on Desktop-A, NAS, and Desktop-B. Later confirmed drops have been observed on **all machines**.

**Critical clue:** The most concrete reproduction is a **Moonlight game stream from Desktop-A → HTPC** stuttering. This is **pure LAN peer-to-peer traffic with zero internet involvement.** Moonlight uses UDP for video, so a "drop" manifests as dropped frames, not a reconnect.

---

## 3. What Was Tested & What We Learned

### Test performed
A ~12-hour ping-logging run on Desktop-A and the QNAP NAS. Each machine pinged **two targets once per second** with timestamps:
- Gateway `192.168.0.1` (LAN-side health)
- External `1.1.1.1` (WAN-side health)

Logs were collected to CSV and run through an overlap parser that flags failures occurring on more than one machine within a 2-second window.

### Findings

**Finding 1 — Normal-hours drops were almost entirely WAN-side, NOT LAN.**
From ~8:50 PM to ~10:30 PM, nearly every failure targeted `1.1.1.1` (external); the gateway `192.168.0.1` almost never failed. When a LAN element drops, the gateway ping fails too — it didn't. **The LAN held during normal operation, as measured by gateway pings.** These external blips were short, isolated, single-ping events — consistent with brief ISP/fiber/Cloudflare-path hiccups, not the switches.

**Finding 2 — A sustained ~90-second LAN outage occurred at 3:00–3:01 AM.**
Both machines failed repeatedly against **both** targets (including the gateway) for ~90 seconds. Gateway unreachable = genuine LAN-side outage. 3 AM timing strongly suggests a **scheduled task**: QNAP firmware/AV/RAID-scrub/reboot, or a nightly router/ISP-gear re-sync. The QNAP showed more failures than Desktop-A here, pointing at the NAS being busy or restarting. **This is a separate, once-nightly event — probably unrelated to the daytime/streaming complaint.**

**Finding 3 (the key realization) — The test measured the wrong path.**
The Moonlight clue proved the real symptom is **LAN peer-to-peer** (Desktop-A↔HTPC), but the ping test only measured the path **toward the gateway/internet**. A gateway ping from Desktop-A never exercises the Switch1↔HTPC or Switch1↔NAS segments the way peer traffic does. **A device can ping the gateway perfectly while peer-to-peer traffic between two other ports stutters — especially under load.** This is why the LAN looked clean while the stream stuttered.

### Current leading hypothesis
**Switch 1**, because it is the most-shared element on every peer-to-peer path, and because drops are now reported on all machines (which rules the HTPC-only coupler back *out* as the common cause). The test simply never stressed the right traffic to catch it.

### Open questions to resolve
1. When drops happen, are they **simultaneous** across machines (→ shared device / Switch 1) or **independent** (→ per-port/per-cable)?
2. Do drops occur only under **load** (active stream / large transfer) or also at idle?
3. HTPC and NAS LAN IPs.

---

## 4. Recommended Diagnostic Approach (informs app design)

The hand-rolled ping loops worked but measured the wrong path and don't reproduce load-triggered drops. The better approach, drawn from standard network-engineering practice:

### Core principles the app should embody
1. **Test peer-to-peer paths, not just the gateway.** A full mesh of probes between LAN hosts exercises the segments that actually carry the problematic traffic.
2. **Test under load.** Idle pings pass while saturated links drop. Generating real traffic and watching for loss simultaneously is what catches the actual failure.
3. **Test at the TCP layer, not just ICMP.** Some switches/NICs treat ICMP and TCP differently. Probing the actual TCP ports apps use (e.g. SMB 445, Moonlight ports) is more representative than ICMP ping.
4. **Synchronize clocks** across machines so multi-host logs can be correlated (NTP). Sub-second precision isn't critical — drops last seconds and a 2-second correlation window tolerates loose clocks — but clocks should be roughly aligned.
5. **Isolate by topology change.** The decisive test: bypass Switch 1 (run two affected devices both into Switch 2 or both into the Router) and re-test. If drops vanish, Switch 1 is confirmed.

### Tools the pro community uses (the app should wrap or replicate these)
- **iperf3** — *the* standard for load testing between two hosts. Reports throughput, retransmits, jitter. Runs on Windows, macOS, QNAP. **Highest-value tool for this problem** — reproduces a load-triggered drop on demand. Server on one host, client on another, push traffic across Switch 1 and watch for retransmits/throughput collapse.
- **tcping / PsPing** — TCP-port reachability probing with timestamps (vs ICMP). Tests the actual TCP path apps use.
- **PingPlotter** (Windows) — continuous hop-aware latency/loss graphing over time; shows *which hop* loses packets. SmokePing is the open-source equivalent (could live permanently on the QNAP).
- **Wireshark / tshark** — definitive packet-level proof. A ring-buffer capture filtered to `tcp.analysis.retransmission || tcp.flags.reset==1` shows retransmits, dup ACKs, RSTs, zero-window events. Heavy data, needs interpretation; use rolling capture files for long intermittent watches.
- **Moonlight performance overlay** (Ctrl+Alt+Shift+S during stream) — ground truth on whether a stutter is network loss vs decode. Worth checking first to confirm it's even network-layer.
- **A cheap managed switch** — temporarily swapping a $30–40 smart switch in for Switch 1 exposes per-port CRC/error counters. A climbing CRC count on one port is a smoking gun no ping test reveals. Often faster than software.

### Recommended diagnostic sequence (the app should guide the user through this)
1. Enable Moonlight overlay, reproduce stutter, confirm it's network loss not decode.
2. Run iperf3 Desktop-A↔HTPC across Switch 1; watch for retransmits/throughput dips coinciding with stutters.
3. Bypass Switch 1 (both devices into Switch 2 or Router); re-run iperf3. If clean → Switch 1 confirmed.

---

## 5. App Requirements

### Goal
A **cross-platform GUI app** (Windows, QNAP, macOS) that drives the full diagnostic workflow above — replacing the manual SSH/PowerShell/CSV/parser dance with a guided, visual tool.

### Must run on
- **Windows 11** (Desktop-A, Desktop-B, HTPC)
- **macOS**
- **QNAP** (QTS — Linux/BusyBox base; may run in a container, as a QPKG, or headless agent)

Implication: pick a stack that genuinely covers all three. Options to weigh in Claude Code:
- **Go** (single static binary per platform, trivial to run on QNAP, easy cross-compile) with a web-based GUI served locally — strong fit given QNAP's awkwardness.
- **Electron / Tauri** for desktop GUI on Win/Mac, plus a headless agent on QNAP.
- **Python** + a GUI toolkit — works but packaging for QNAP is fiddly.

A likely clean architecture: **lightweight agents** on each host (especially the headless QNAP) that run probes and report to a **coordinator with the GUI** running on a desktop. This naturally supports the multi-host mesh and correlation.

### Core features
1. **Host discovery / inventory** — auto-discover LAN hosts or let the user define the topology (the diagram above), label devices, assign roles.
2. **Peer-to-peer probe mesh** — ICMP *and* TCP-port probes between any pair of hosts, not just to the gateway. Continuous, timestamped, persisted.
3. **Load testing** — integrate/wrap **iperf3** between host pairs; surface throughput, retransmits, jitter live.
4. **Clock sync awareness** — sync or at least measure clock offset between agents so logs correlate; use a configurable correlation window (default ~2s).
5. **Correlation engine** — the heart of it: detect failures across hosts and flag **simultaneous** (shared-device) vs **independent** (per-port/cable) drops. This is what the manual `overlap.ps1` parser did — make it real-time and visual.
6. **LAN-vs-WAN distinction** — probe a gateway target and an external target so the app can classify each drop as LAN-side or WAN-side (the single most useful distinction from the manual test).
7. **Timeline visualization** — PingPlotter-style: latency/loss over time per path, with drops marked, zoomable, overlaid across hosts to see correlation visually.
8. **Packet capture hook (optional/advanced)** — trigger a tshark ring-buffer capture on an agent around detected drop events for deep inspection.
9. **Guided isolation workflow** — walk the user through the "bypass Switch 1, re-test" sequence and compare before/after results automatically.
10. **Export** — CSV/JSON of raw data plus a summary report (replacing the manual handoff doc).

### Hard-won environment gotchas (carry these into the build)
- **Windows OneDrive-redirected Desktop:** `$env:USERPROFILE\Desktop` may not exist (redirected to `...\OneDrive\Desktop`). Don't assume Desktop paths — write to `$env:USERPROFILE` root or an app data dir, or resolve the real Desktop path.
- **Windows execution policy** blocks `.ps1` by default — relevant if shelling out to PowerShell. App should avoid depending on user execution-policy state.
- **Windows Time service (w32time)** is often stopped/disabled and requires **admin** to start/config. Clock sync on Windows may need elevation; handle gracefully.
- **QNAP is BusyBox**, not full GNU. `date` may lack `%N` (no sub-second timestamps — second resolution only). `ping` output wording is `1 packets received` and `time=0.578 ms`. `nohup` was unreliable (`Done(127)`); **`screen` worked** but needed `TERM=vt100 screen -S name` to dodge a missing terminfo entry. Detach is Ctrl+A then D (separate keystrokes). Writable share: `/share/Public`. Plan for a long-running headless agent rather than interactive shell tricks.
- **QNAP scheduled tasks** (firmware/AV/RAID-scrub/reboot ~3 AM) can cause genuine LAN outages — the app should help distinguish these from the real problem, and it's worth auditing Control Panel → System schedules + App Center auto-update.
- **Clock correlation tolerance:** drops last seconds; a 2-second window tolerates loose clocks. Don't over-engineer sub-second sync.

### Non-goals / scope notes
- This is a **diagnostic** tool, not a permanent monitoring suite (though SmokePing-style persistent monitoring on the QNAP could be a stretch goal).
- Unmanaged switches expose nothing programmatically — the app cannot read switch counters. Its value is inferring switch faults from endpoint behavior. (A managed-switch swap remains a manual hardware step.)

---

## 6. Reference: the manual artifacts built so far

These can be discarded once the app exists, but document the approach:
- **Windows logger** (`netlog.ps1`): per-second `Test-Connection` to gateway + external, CSV with `timestamp,target,status,latency_ms`.
- **QNAP logger** (`netlog.sh`): same in `sh`, run under `screen`, output to `/share/Public/netlog_qnap.csv`, second-resolution timestamps.
- **Overlap parser** (`overlap.ps1`): loads all CSVs, lists failures by time, flags any within a 2s window spanning >1 machine. **This logic is the seed of the app's correlation engine.**

---

## TL;DR for the builder
The user has intermittent multi-second LAN drops, reproduced as Moonlight stutter (Desktop-A→HTPC). Manual ping testing proved the drops are LAN peer-to-peer (not WAN) but accidentally measured the gateway path instead of the peer path. Leading suspect is the unmanaged **Switch 1** (shared by all peer traffic); decisive test is bypassing it. Build a cross-platform (Win/Mac/QNAP) GUI app with per-host agents that runs a **peer-to-peer ICMP+TCP probe mesh under iperf3 load**, **correlates drops across hosts** (simultaneous=shared-device, independent=cable/port), classifies LAN-vs-WAN, visualizes a timeline, and guides the user through the bypass-Switch-1 isolation test. Mind the documented Windows (OneDrive/exec-policy/w32time) and QNAP (BusyBox/screen/nohup) gotchas.
