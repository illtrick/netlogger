# NetLogger — Project Handoff

Single entry point for resuming work. Repo: https://github.com/illtrick/netlogger (branch `main`).

## What this is
A cross-platform LAN diagnostic tool (Go) that deploys agents to each machine, runs a peer-to-peer probe mesh + iperf3 load tests, correlates drops across hosts, and scores per-component health + coverage — to isolate intermittent, load-triggered drops (the original symptom: Moonlight stutter Ryzen→ProjectorPC across an unmanaged switch). Operator-driven, GUI-first, no terminal required.

## Status: M1–M4 complete + full code review fixes + UX1–UX7 (app polish). All committed/pushed.
- 106 test functions across 17 test packages, all green (`go test -count=1 ./...`). cgo-free (so `go test -race` can't run).
- Dev build: `go build -o bin/netlogger.exe ./cmd/netlogger`. Release/app build: `./scripts/build.ps1` (windowsgui no-console app + Linux incl. QNAP arm64 + macOS).
- Go is at `C:\Program Files\Go\bin` (not on default PATH; prepend it). Commit footer: `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`.
- PowerShell here-strings for commit messages: **avoid `"` and `>`** in the body (they break parsing).

## Architecture (one Go binary per host)
Every node runs the agent; one is `role: coordinator` (serves the GUI + aggregates). Control plane = star (coordinator pulls), data plane = peer-to-peer probes.
- `internal/config` — network YAML (nodes + links); Load/Save/WriteStarter/ToYAML; Resolve(node)→self+peers. Shared with peers.
- `internal/localsettings` — per-machine `settings.json` (db dir + bind-address overrides); Load/Save/Path/ResolveDBPath/ResolveListen. Machine-local, **never** shared with peers.
- `internal/store` — local WAL SQLite; per-agent samples, idempotent `Upsert` on `(agent_id,seq)`, sync cursors, connectivity_events.
- `internal/probe` — ICMP (pro-bing, unprivileged on Win) + isochronous UDP; runner.
- `internal/mesh` — agent sync API (`/api/info,/samples,/time`), coordinator `Puller` (cursor-based, idempotent, liveness), NTP-style offset handshake, `Offsets`, `AuthClient`.
- `internal/correlate` — event detection + interval-overlap correlation (peak-concurrent-agents → simultaneous).
- `internal/score` — topology BFS paths + per-component health/coverage.
- `internal/readiness` `internal/classify` `internal/iperf` `internal/sysinfo` (NIC counters) — checks, bufferbloat-vs-fault + LAN-vs-WAN, iperf3 wrap, self-checks. **iperf3 is bundled** in the Windows build (`internal/iperf/bundled/`, embedded via build-tagged `bundle_windows.go`); each agent self-extracts it to the data dir (`Bootstrap`) and runs an **always-on iperf3 server** on :5201 (`StartServer`) so any node is a turnkey load-test target. Resolution: bundled > co-located > PATH. Linux/macOS builds don't embed (fall back to PATH). NOTE: bundled `cygwin1.dll` is GPLv3 — fine for internal use; reconsider for public redistribution.
- `internal/coordinator` — HTTP handlers (components/correlation/loadtest/classify/topology…).
- `internal/httpauth` — bearer token (`NETLOGGER_TOKEN`) + Host allowlist; loopback + `/`/`/api/status` exempt; `/api/*` protected; `/download/*` open.
- `internal/svcctl` — elevated Windows service control (UAC) + status.
- `internal/launch` — browser-open + address normalize. `internal/agentsvc` — wires it all into a kardianos service. `cmd/netlogger` — CLI/flags.

## The app experience (no terminal)
Double-click `netlogger.exe` → starter config + dashboard opens. GUI does: edit network (Configuration), set the local **Network access** bind address and **Data storage** directory (Configuration → `/api/settings`; changes apply on restart), Install/Start/Stop service (UAC), run load tests, **Deploy to another machine** (Agents → copy a one-paste elevated-PowerShell bootstrap that pulls `/download/agent` + `/download/config.yaml` and installs on the peer), Quit (service-aware: offers to stop an installed service too). Auto-refreshes 3s. Layout reflows + scrolls on narrow/zoomed windows.

DB path and bind address are resolved at startup from `localsettings` (honored by interactive, service, and self-restart launches). **Default bind is `0.0.0.0:8088`** so peers can reach a node out of the box (the loopback auth bypass keeps the local dashboard usable without a token); set `127.0.0.1:8088` in the GUI to make a node private. `--listen` flag overrides the saved setting. Default data dir: `%ProgramData%\NetLogger` (Windows) / `~/.netlogger` (else); `settings.json` always lives there even when it points the DB elsewhere.

## Design docs
- Spec: `docs/superpowers/specs/2026-06-04-netlogger-design.md` (the authoritative design; §-numbered).
- Plans: `docs/superpowers/plans/` (M1, M2a, M2b, M3, M4).
- UI mockup: `docs/mockups/netlogger-ui-mockup.html` (v4 — the live GUI is ported from this).
- Original problem brief: `network-diagnostic-handoff.md`.

## Real network (user's home LAN — see examples/home-network.yaml)
Modem(Calix C5500XK)→2 wall jacks→Router(TP-Link BE9300)→Switch1(Tenda TEM2010F, 2.5G)→{NAS(QNAP TS-563); ProjectorPC(I219-V, 1G) via UGREEN coupler; Switch2(Tenda)→Ryzen(Killer E3100G 2.5G, coordinator), NCASE(Intel I226-V 2.5G)}. **LAN is 2.5G end-to-end** (the "10G" coupler adds no speed). Leading hardware suspects from research: I226-V/E3100G **EEE dropout** (disable Energy-Efficient Ethernet) > Switch1 > coupler.

## Next steps (not yet done)
1. **Deploy & run the real diagnosis** on the 3 Windows boxes (the planned pause point) — reproduce the stutter, read Components/Correlation. Install iperf3 (`winget install Anvil.iperf3`) or bundle `iperf3.exe`.
2. **M5** — QNAP agent (Docker/Container Station, `--restart always`) to bring the NAS into the mesh; analysis-bundle export.
3. **M6** — macOS agent (LaunchDaemon, notarization); optional packet capture.
4. **Gaps:** service control + push-to-peer are Windows-only (mac/linux say "unsupported"); push-to-peer is "pull from coordinator with one paste" (true remote exec not built); no tray icon (deferred — cgo); deferred review test items are minor.
