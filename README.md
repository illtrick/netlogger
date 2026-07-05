# NetLogger

**A portable, zero-config LAN diagnostic tool for catching intermittent, load-triggered network faults.**

Drop the same single `NetLogger.exe` on every Windows machine on your network. The copies find each other automatically, probe every link continuously, and line their timelines up side by side — so when your stream stutters or your remote desktop drops at 2 AM, you can see **which machine's link** misbehaved, **when**, and **whether it happened under load**.

NetLogger exists because of a real fault: a PC whose wired link silently reset only under streaming load, on a network where every one-shot speed test said "all healthy." One-shot tools can't catch intermittent faults. NetLogger watches all links, all the time, and makes concurrency visible.

![status](https://img.shields.io/badge/platform-Windows%2010%2F11-blue) ![go](https://img.shields.io/badge/Go-cgo--free-00ADD8)

---

## What it does

### Continuous mesh monitoring (Dashboard)

- Every node probes every other node with **ICMP** and **high-rate isochronous UDP** (loss + jitter — the traffic class that actually breaks streams), plus the gateway and an internet anchor.
- The **activity heatmap** lines every machine's health up on one shared time axis. Simultaneous red cells across machines point at a shared cause (switch, uplink); red on one machine points at a local cause (NIC, cable, port). Hovering a cell shows exactly what happened in that window — including whether a test was running at the time.
- **NIC diagnostics** — link speed, power-saving states, error/discard counters, and hard **link-flap detection** (the physical Disconnected→Up events one-shot tools never see).
- **Mesh-wide event log** — link flaps, loss episodes, node up/down, and test runs from every machine, merged onto one timeline.

### On-demand tests (orchestrated from any node)

- **LAN speed matrix** — every device pair measured in both directions (iperf3 under the hood), rendered as an N×N grid that makes the one slow leg jump out. Cells fill in live; click any cell for retransmits, RTT, and per-direction detail; Stop genuinely cancels in-flight runs, including on remote machines.
- **Full-mesh stress test** — every node loads every other node simultaneously with rate-capped traffic *while the continuous probes keep measuring*. This is the RRUL-style method for reproducing load-triggered faults: the heatmap going red under load **is** the diagnosis. Guardrails: adjustable per-link cap, duration limit, per-link auto-abort on repeated failure, and a kill-switch.
- **Internet speed test** — parallel-stream throughput against public LibreSpeed servers (auto-picked by ping, or pinned via dropdown), with idle vs. loaded latency measured throughout and a **bufferbloat grade (A–F)** plus RPM. Runs on any node in the mesh, so you can compare each machine's WAN path.
- **Test history** — runs persist locally, show as trend rows in the UI, and ship in the diagnostic export.

### Built like a tool you leave running

- One dark, dense dashboard — a native Gio app in a single binary. No browser, no Electron, no installer.
- **Closes to the system tray** and keeps monitoring; left-click the tray icon to reopen, right-click for Open/Quit.
- **One-click JSON export** bundles events, link matrix, NIC state, loss timeline, and test history for off-box analysis.
- Everything is stored locally in a per-machine SQLite database, beside the exe. Nothing leaves your network except the internet test's own traffic.

---

## Quick start

1. Get `NetLogger.exe` (build it below) and copy the **same exe** to two or more Windows machines on the same LAN.
2. Run it on each machine. It asks for elevation (needed for ICMP probing and to add its own firewall rules on first run).
3. That's it — no config. Nodes discover each other via UDP multicast within seconds and the dashboard starts filling in. Leave it running; the value compounds with time on the clock.
4. When something misbehaves, open the **Dashboard** heatmap and look for concurrency across machines. To provoke a suspected load-triggered fault on purpose, run the **Stress** test from the Tests tab and watch the heatmap.

**Requirements:** Windows 10/11 or macOS 11+ (beta), machines on the same L2 network (multicast reachable).

**Ports used** (firewall rules are added automatically when elevated):

| Port | Protocol | Purpose |
|---|---|---|
| 8088 | TCP | Control plane (LAN-only, host-allowlisted) |
| 8089 | UDP | Probe echo responder |
| 5201 | TCP | Bundled iperf3 server (speed/stress tests) |
| 48076 | UDP | Multicast discovery (`239.255.74.76`) |

---

## Building from source

Requires **Go 1.26+**. No cgo, no C toolchain — `go build` is all it takes. For the Windows resource (app icon + elevation manifest), install [goversioninfo](https://github.com/josephspurrier/goversioninfo) once:

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

### macOS (beta)

The engine is identical; the mac build must be compiled **on a Mac** (Gio's
macOS backend uses cgo). Full developer guide: [docs/MACOS.md](docs/MACOS.md).

```bash
./scripts/bootstrap-mac.sh # once: Xcode CLT, Go ≥ 1.26, iperf3
./scripts/build-mac.sh     # → bin/NetLogger.app (universal, ad-hoc signed)
./scripts/test-mac.sh      # 6-layer test suite (units, race, cross-OS, live smoke, bundle)
```

Platform notes:

- **No admin needed.** macOS allows unprivileged ICMP, so there is no
  elevation prompt at all.
- **Speed & stress tests need iperf3**: `brew install iperf3`. Without it,
  monitoring still works fully; the Tests tab explains what's missing.
- First launch prompts for **Local Network** access and the firewall's
  "Allow incoming connections" — accept both or peers won't see this node.
  If Gatekeeper blocks the unsigned app: right-click → Open, once.
- Data lives in `~/Library/Application Support/NetLogger` (never inside the
  .app bundle).
- The Adapters panel shows live Wi-Fi radio state (PHY rate, channel, RSSI,
  noise) via CoreWLAN and wired negotiated rate + duplex — no root needed.
- Tray mode is Windows-only for now; on macOS closing the window quits.

---

## How it's put together

| Package | Role |
|---|---|
| `internal/appcore` | The engine: probe loops, peer sync, heatmap assembly, test orchestration, events, export |
| `internal/ui` | The Gio dashboard: heatmap, tests, events, custom window chrome, tray |
| `internal/probe` | ICMP + isochronous UDP probing and the UDP echo responder |
| `internal/discovery` | UDP-multicast peer discovery |
| `internal/iperf` | Bundled iperf3 wrapper: JSON parsing, parallel/reverse/bidir modes, context cancellation |
| `internal/store` | WAL-mode SQLite persistence (samples, events, test history) |

Every node runs a small LAN-only HTTP control plane (`/api/links`, `/api/events`, `/api/lossbuckets`, `/api/speedtest`, `/api/stress/*`, `/api/internet`, …) guarded by a host allowlist. That's what lets any node orchestrate tests on any other node and assemble the mesh-wide views — the UI you're looking at is a window onto the whole mesh, not just the local machine.

UI conventions live in [docs/design-guide.md](docs/design-guide.md); design specs and implementation plans are under [docs/superpowers/](docs/superpowers/).

## Honest limitations

- **Windows-first.** The core is portable Go, but tray, window chrome, NIC introspection, and the bundled iperf3 are Windows implementations today.
- Cross-machine heatmap alignment assumes roughly synced clocks (bucket-level tolerance).
- The internet test measures latency over HTTP round-trips (slightly above ICMP ping); the bufferbloat *delta* and grade are the meaningful numbers. Internet jitter/loss aren't measured yet.
- It reports; you conclude. NetLogger localizes a fault to a machine/link/time window — it won't tell you to replace a cable (though it has, in fact, found a bad one).

---

## Credits & acknowledgements

NetLogger stands on excellent prior work:

- **[Gio UI](https://gioui.org)** — Elias Naur and contributors. The immediate-mode, pure-Go GUI toolkit that makes a cgo-free single-binary native dashboard possible.
- **[iperf3](https://software.es.net/iperf/)** — ESnet / Lawrence Berkeley National Laboratory (BSD-3-Clause). The canonical network load generator, bundled for the LAN speed and stress tests. The Windows build ships with **cygwin1.dll** from the [Cygwin](https://www.cygwin.com/) project (LGPL).
- **[LibreSpeed](https://librespeed.org)** and its public server sponsors — notably **Clouvider** and **Sharktech** — whose open endpoints power the internet speed test. Please be considerate of their donated bandwidth.
- **[Bufferbloat.net](https://www.bufferbloat.net)** — Dave Täht, Toke Høiland-Jørgensen, and the bufferbloat community. Their **[RRUL](https://www.bufferbloat.net/projects/bloat/wiki/RRUL_Spec/)** methodology (saturate while measuring latency) is the intellectual basis of the stress test, demonstrated by **[Flent](https://flent.org)**. Apple's network-responsiveness work inspired the **RPM** metric shown alongside the bufferbloat grade. Cloudflare's speed test methodology informed the measurement design.
- **Go dependencies:** [modernc.org/sqlite](https://gitlab.com/cznic/sqlite) (Jan Mercl's pure-Go SQLite — the reason there's no cgo), [pro-bing](https://github.com/prometheus-community/pro-bing) (Prometheus community, descended from Cameron Sparr's go-ping), [jackpal/gateway](https://github.com/jackpal/gateway), [google/uuid](https://github.com/google/uuid), [gopkg.in/yaml.v3](https://github.com/go-yaml/yaml), the golang.org/x packages, and [goversioninfo](https://github.com/josephspurrier/goversioninfo) (Joseph Spurrier) for the Windows resource embedding.
- Built with **[Claude Code](https://claude.com/claude-code)** (Anthropic).

## License

[MIT](LICENSE). The bundled third-party binaries carry their own licenses: `iperf3.exe` is BSD-3-Clause (ESnet/LBNL) and `cygwin1.dll` is LGPL (Cygwin project).
