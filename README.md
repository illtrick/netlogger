# NetLogger

A cross-platform LAN diagnostic tool for isolating intermittent, load-triggered network drops. You **deploy** an agent to each machine, **verify** it's configured correctly, **run** probe + load tests, and read **per-component health and test coverage** — letting you isolate a fault (a switch, a cable, a NIC) from endpoint behavior alone. No wizards, no per-vendor opinions: it measures and reports; you conclude.

> Status: **M1 (Windows core vertical slice) complete.** See the [roadmap](#roadmap).

## What it does

- **Peer-to-peer probe mesh** — ICMP baseline + isochronous UDP (loss/jitter) between host pairs, not just to the gateway. (UDP is the backbone because the real symptom is UDP.)
- **Local-first storage** — each agent writes to its own WAL SQLite store and survives the very drops it diagnoses.
- **Per-component health + coverage** — health from measurements; coverage from how robustly each part has been tested. A cable/segment is covered *by inference* once both its endpoints are well tested.
- **Self-installing background service** — one Go binary per host (Windows Service / QNAP Docker / macOS LaunchDaemon).
- **Topology as data** — the network map and device list come from a config file (`config.Load`), not hardcoded.

## Build & run (Windows)

Requires Go 1.22+.

```powershell
go build -o bin/netlogger.exe ./cmd/netlogger
go test ./...

# foreground (Ctrl+C to stop) — serves http://127.0.0.1:8088
.\bin\netlogger.exe run

# as a Windows Service (elevated shell)
.\bin\netlogger.exe install
.\bin\netlogger.exe start
.\bin\netlogger.exe stop
.\bin\netlogger.exe uninstall
```

Data + the SQLite store live under `%ProgramData%\NetLogger\`.

## Architecture

One Go module. `internal/` packages each own one responsibility:

| Package | Responsibility |
|---|---|
| `config` | load/validate the network config file (topology + inventory) |
| `clock` | UTC microsecond timestamps (+ test fake) |
| `store` | local WAL SQLite sample store (`Insert`, cursor-based `Since`) |
| `probe` | ICMP probe, isochronous UDP probe, and the probe→store runner |
| `web` | embedded status SPA + `/api/status` |
| `agentsvc` | wires the probe loop + web server into a `kardianos/service` |

Stack: pure-Go SQLite (`modernc.org/sqlite`, no cgo), `prometheus-community/pro-bing` (ICMP via `IcmpSendEcho`, unprivileged on Windows), `kardianos/service`, `gopkg.in/yaml.v3`.

## Roadmap

- **M1 — Windows core vertical slice** ✅ probe → store → service → status page
- **M2** — coordinator role, resilient agent↔coordinator sync, configuration-readiness checks, multi-host mesh
- **M3** — clock-offset correlation + the per-component health/coverage board
- **M4** — iperf3 load tests + LAN/WAN & bufferbloat-vs-fault classifiers
- **M5** — QNAP agent (Docker) + analysis-bundle export
- **M6** — macOS agent + optional packet capture

Design spec: [`docs/superpowers/specs/2026-06-04-netlogger-design.md`](docs/superpowers/specs/2026-06-04-netlogger-design.md) · M1 plan: [`docs/superpowers/plans/2026-06-04-netlogger-m1-windows-core.md`](docs/superpowers/plans/2026-06-04-netlogger-m1-windows-core.md)
