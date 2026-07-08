# NetLogger

**A portable, zero-config LAN diagnostic tool for catching intermittent, load-triggered network faults.**

Drop the same app on every machine on your network (Windows `.exe`, macOS `.app`). The copies find each other automatically, probe every link continuously, and line their timelines up side by side — so when your stream stutters or your remote desktop drops at 2 AM, you can see **which machine's link** misbehaved, **when**, and **whether it happened under load**.

NetLogger exists because of a real fault: a PC whose wired link silently reset only under streaming load, on a network where every one-shot speed test said "all healthy." One-shot tools can't catch intermittent faults. NetLogger watches all links, all the time, and makes concurrency visible.

![platform](https://img.shields.io/badge/platform-Windows%2010%2F11%20·%20macOS%2011%2B-blue) ![go](https://img.shields.io/badge/Go-single%20binary-00ADD8) ![license](https://img.shields.io/badge/license-MIT-green)

![Dashboard — activity heatmap, gateway/internet probes, NIC diagnostics](docs/screenshots/dashboard.png)

---

## Features

### Continuous mesh monitoring

- Every node probes every other node with **ICMP** and **high-rate isochronous UDP** (loss + jitter — the traffic class that actually breaks streams), plus the gateway and an internet anchor.
- The **activity heatmap** lines every machine's health up on one shared time axis. Simultaneous red cells across machines point at a shared cause (switch, uplink); red on one machine points at a local cause (NIC, cable, port). Hovering a cell shows what happened in that window — including whether a test was running at the time.
- **NIC diagnostics** — link speed, power-saving/EEE states (a classic silent-dropout suspect), error/discard counters, and hard **link-flap detection**. On macOS, live Wi-Fi radio state (PHY rate, channel, RSSI, noise) via CoreWLAN.
- **Mesh-wide event log** — link flaps, loss episodes, node up/down, and test runs from every machine, merged onto one timeline.

### LAN speed matrix

![Speed matrix — directional cells graded as % of link speed](docs/screenshots/speed-matrix.png)

- Every directed link measured with the bundled **iperf3**: cell = data flowing **row → column**, one number, graded as **% of the link's capability** (each node beacons its NIC speed — a 2.5 GbE leg running at 940 Mb/s grades amber even though the absolute number looks fine).
- `▲` marks a direction meaningfully slower than its reverse — direction asymmetry is a classic one-ended fault.
- Cells fill **live, updating every second** while the sweep runs (iperf3 `--json-stream`). Click a cell for the per-second chart, retransmits, and the reverse direction.
- Controls for direction (both/down/up/bidir), duration, and parallel streams; the first second is omitted so averages reflect steady state, not TCP slow-start. History rows record the exact settings used.

### Full-mesh stress test

![Stress test — all links loading at cap, throughput and latency on one time axis](docs/screenshots/stress.png)

- Every node loads every other node **simultaneously** with rate-capped traffic *while the continuous probes keep measuring* — the [RRUL](https://www.bufferbloat.net/projects/bloat/wiki/RRUL_Spec/) method for reproducing load-triggered faults. The heatmap going red under load **is** the diagnosis.
- Throughput-per-link and latency stacked on **one shared time axis**: a rate sag and an RTT spike on the same second is the fault, caught in the act.
- Links are labeled `source → target`; per-target server ports are assigned automatically so N-node meshes genuinely load all N·(N−1) links at once.
- Guardrails: adjustable per-link cap, hard duration limit, per-link auto-abort on repeated failure, and a kill-switch. Every run persists a summary — duration, links, **worst added latency per node**, aborts.

### Internet speed test + bufferbloat grade

![Internet test — idle vs loaded latency, bufferbloat grade, provenance](docs/screenshots/internet.png)

- Parallel-stream throughput against public **LibreSpeed** servers (auto-picked by ping, or pinned via dropdown), runnable from **any node in the mesh** to compare each machine's WAN path.
- Latency is measured **throughout** the transfer: idle → loaded → added delta → an **A–F bufferbloat grade** (scale shown right on the card) plus RPM. Bufferbloat is why gaming lags while a speed test says everything's fine.
- Results record when, on which node, and against which server.

### Built like a tool you leave running

- One dark, dense dashboard — a native Gio app in a single binary. No browser, no Electron, no installer.
- **Closes to the system tray** (Windows) and keeps monitoring; left-click the tray icon to reopen, right-click for Open/Quit.
- **One-click JSON export** bundles events, link matrix, NIC state, loss timeline, and all test history for off-box analysis.
- Everything is stored locally in a per-machine SQLite database. Nothing leaves your network except the internet test's own traffic.

---

## Quick start

1. Get the app (build below) and put the **same build** on two or more machines on the same LAN.
2. Run it on each machine. Windows asks for elevation once per launch (ICMP probing + its own firewall rules); macOS needs no elevation — just allow **Local Network** and incoming connections when prompted.
3. That's it — no config. Nodes discover each other via UDP multicast within seconds and the dashboard starts filling in. Leave it running; the value compounds with time on the clock.
4. When something misbehaves, open the **Dashboard** heatmap and look for concurrency across machines. To provoke a suspected load-triggered fault on purpose, run the **Stress** test and watch the heatmap.

**Requirements:** Windows 10/11 or macOS 11+, machines on the same L2 network (multicast reachable).

**Ports used** (rules are added automatically on Windows when elevated):

| Port | Protocol | Purpose |
|---|---|---|
| 8088 | TCP | Control plane (LAN-only, host-allowlisted) |
| 8089 | UDP | Probe echo responder |
| 5201 | TCP | Always-on iperf3 server (speed/stress tests) |
| 5202+ | TCP | Extra per-link iperf3 listeners, only while a stress run is active |
| 48076 | UDP | Multicast discovery (`239.255.74.76`) |

---

## Building from source

### Windows

Requires **Go 1.26+**. No cgo, no C toolchain. For the resource (app icon + elevation manifest), install [goversioninfo](https://github.com/josephspurrier/goversioninfo) once:

```powershell
go install github.com/josephspurrier/goversioninfo/cmd/goversioninfo@latest

# One-shot release build (icon, manifest, build id) → bin/NetLogger.exe
./scripts/build-app.ps1
```

Or manually:

```powershell
go generate ./cmd/netlogger-app
$env:CGO_ENABLED = "0"
go build -ldflags "-H windowsgui -s -w -X netlogger/internal/version.Build=$(git rev-parse --short HEAD)" -o NetLogger.exe ./cmd/netlogger-app
```

Run the tests with `go test ./...`. The app icon is generated by `go run ./tools/genicon` (committed as `cmd/netlogger-app/icon.ico`).

### macOS

The engine is identical; the mac build must be compiled **on a Mac** (Gio's macOS backend uses cgo). Full developer guide: [docs/MACOS.md](docs/MACOS.md).

```bash
./scripts/bootstrap-mac.sh # once: Xcode CLT, Go ≥ 1.26, iperf3
./scripts/build-mac.sh     # → bin/NetLogger.app (universal, ad-hoc signed)
./scripts/test-mac.sh      # 6-layer test suite (units, race, cross-OS, live smoke, bundle)
```

macOS notes: no elevation ever (unprivileged ICMP); speed/stress tests need `brew install iperf3` (monitoring works fully without it); data lives in `~/Library/Application Support/NetLogger`; Gatekeeper on the unsigned app → right-click → Open, once; closing the window quits (tray is Windows-only for now).

---

## FAQ

**Why does it need administrator on Windows?**
Two things: raw-socket ICMP probing, and adding its own firewall rules (control plane, probe echo, iperf3 ports) so peers can reach it. macOS exposes unprivileged ICMP, so the Mac build never elevates.

**My machines don't see each other.**
Discovery is UDP multicast on the local L2 segment. The usual culprits: nodes on different VLANs or subnets, a guest Wi-Fi with client isolation, "AP isolation" on the router, or a third-party firewall blocking `48076/udp`. All nodes must also run the **same release** — a version-mismatch banner in the header tells you if one is behind.

**What does "% of link" in the speed matrix mean?**
Each node reports its NIC's negotiated speed; a cell is graded against the *slower* endpoint's link. 940 Mb/s is excellent on a 1 GbE leg (94%) and a problem on a 2.5 GbE leg (38%) — absolute thresholds can't tell those apart. Nodes that don't report a link speed fall back to absolute grading. Note: on Wi-Fi the denominator is the live PHY rate, which is optimistic.

**What does the ▲ in a matrix cell mean?**
That direction is meaningfully slower (>20%) than the same link in reverse. Asymmetry usually points at one endpoint — duplex mismatch, a failing NIC, EEE power-saving, or a bad cable pair.

**Why do my numbers differ from Ookla / fast.com?**
Different measurement, different path. The matrix measures your **LAN**, machine-to-machine — an internet test never touches those links. The internet test uses LibreSpeed servers with parallel streams and a steady-state window; run-to-run variance of ±10% is normal (congestion, server load, time of day). Look for patterns across the history, not single peaks.

**What's a good bufferbloat grade?**
The grade is added latency while the line is saturated: A < 30 ms, B < 60, C < 100, D < 200. A connection can ace a throughput test and still be an F here — that's the "internet feels slow while the speed test looks fine" disease, and it's what makes games and calls lag when someone else uploads.

**Is the stress test safe to run?**
It's designed to saturate your LAN on purpose — expect sluggish file transfers and stream hiccups *during* the run. Guardrails: per-link rate cap, hard duration limit (10 min max), automatic per-link abort after repeated failures, and a Stop button that kills every node's load immediately. It does not touch your internet connection.

**Where is my data? Does anything leave my network?**
Each machine keeps its own SQLite database next to the exe (Windows) or in `~/Library/Application Support/NetLogger` (macOS). Nodes exchange probe stats and test commands only across your LAN. The only external traffic is the internet speed test itself (to a LibreSpeed server you can see and pin) and an ICMP probe to `8.8.8.8` as an internet-reachability anchor.

**How do I actually quit it on Windows?**
The X hides to the tray so monitoring continues (that's the point of the tool). Right-click the tray icon → **Quit**.

**How many machines can join?**
Discovery has no fixed limit; tests cap at 64 targets per node. The matrix is N×N, so it stays readable up to a handful of nodes — which matches the home/small-lab problem it's built for.

**A speed test says "iperf3 not found" on my Mac.**
`brew install iperf3` (3.17+ gets you the live per-second readout; older versions fall back to end-of-run results). Windows builds bundle iperf3, so this only comes up on macOS.

**The header shows "version mismatch."**
One or more nodes run an older release. Same version = compatible, regardless of OS (a Mac and a PC on the same release interoperate). Update every node to the version shown; features degrade gracefully in the meantime, but mixed meshes disable a few coordination tricks (like per-link stress ports).

---

## How it's put together

| Package | Role |
|---|---|
| `internal/appcore` | The engine: probe loops, peer sync, heatmap assembly, test orchestration, events, export |
| `internal/ui` | The Gio dashboard: heatmap, tests, events, custom window chrome, tray |
| `internal/probe` | ICMP + isochronous UDP probing and the UDP echo responder |
| `internal/discovery` | UDP-multicast peer discovery |
| `internal/iperf` | Bundled iperf3 wrapper: JSON/NDJSON stream parsing, parallel/reverse/bidir modes, context cancellation |
| `internal/store` | WAL-mode SQLite persistence (samples, events, test history) |

Every node runs a small LAN-only HTTP control plane (`/api/links`, `/api/events`, `/api/lossbuckets`, `/api/speedtest`, `/api/stress/*`, `/api/internet`, …) guarded by a host allowlist. That's what lets any node orchestrate tests on any other node and assemble the mesh-wide views — the UI you're looking at is a window onto the whole mesh, not just the local machine.

UI conventions live in [docs/design-guide.md](docs/design-guide.md); the research behind the Tests UI is in [docs/superpowers/specs/2026-07-08-tests-ux-research.md](docs/superpowers/specs/2026-07-08-tests-ux-research.md); macOS development in [docs/MACOS.md](docs/MACOS.md); design specs and implementation plans under [docs/superpowers/](docs/superpowers/).

## Honest limitations

- Cross-machine heatmap alignment assumes roughly synced clocks (bucket-level tolerance).
- The internet test measures latency over HTTP round-trips (slightly above ICMP ping); the bufferbloat *delta* and grade are the meaningful numbers. Internet jitter/loss aren't measured yet.
- Windows/Cygwin iperf3 doesn't expose TCP_INFO, so per-second retransmit/RTT detail is richest when the client side is macOS/Linux; totals work everywhere.
- It reports; you conclude. NetLogger localizes a fault to a machine/link/time window — it won't tell you to replace a cable (though it has, in fact, found a bad one).

---

## Credits & acknowledgements

NetLogger stands on excellent prior work:

- **[Gio UI](https://gioui.org)** — Elias Naur and contributors. The immediate-mode, pure-Go GUI toolkit that makes a single-binary native dashboard possible.
- **[iperf3](https://software.es.net/iperf/)** — ESnet / Lawrence Berkeley National Laboratory (BSD-3-Clause). The canonical network load generator, bundled for the LAN speed and stress tests. The Windows build ships with **cygwin1.dll** from the [Cygwin](https://www.cygwin.com/) project (LGPL).
- **[LibreSpeed](https://librespeed.org)** and its public server sponsors — notably **Clouvider** and **Sharktech** — whose open endpoints power the internet speed test. Please be considerate of their donated bandwidth.
- **[Bufferbloat.net](https://www.bufferbloat.net)** — Dave Täht, Toke Høiland-Jørgensen, and the bufferbloat community. Their **[RRUL](https://www.bufferbloat.net/projects/bloat/wiki/RRUL_Spec/)** methodology (saturate while measuring latency) is the intellectual basis of the stress test, demonstrated by **[Flent](https://flent.org)**. Apple's network-responsiveness work inspired the **RPM** metric shown alongside the bufferbloat grade. Cloudflare's speed test methodology informed the measurement design.
- **Go dependencies:** [modernc.org/sqlite](https://gitlab.com/cznic/sqlite) (Jan Mercl's pure-Go SQLite — the reason there's no cgo on Windows), [pro-bing](https://github.com/prometheus-community/pro-bing) (Prometheus community, descended from Cameron Sparr's go-ping), [jackpal/gateway](https://github.com/jackpal/gateway), [google/uuid](https://github.com/google/uuid), [gopkg.in/yaml.v3](https://github.com/go-yaml/yaml), the golang.org/x packages, and [goversioninfo](https://github.com/josephspurrier/goversioninfo) (Joseph Spurrier) for the Windows resource embedding.
- Built with **[Claude Code](https://claude.com/claude-code)** (Anthropic).

## License

[MIT](LICENSE). The bundled third-party binaries carry their own licenses: `iperf3.exe` is BSD-3-Clause (ESnet/LBNL) and `cygwin1.dll` is LGPL (Cygwin project).
