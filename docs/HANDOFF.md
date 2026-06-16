# NetLogger — Project Handoff

Single entry point for resuming work. Repo: https://github.com/illtrick/netlogger (branch `main`).

## What this is
A **portable, self-elevating native desktop app** (Go + Gio) that diagnoses intermittent, load-triggered LAN problems by running the same `NetLogger.exe` on every machine. Instances **auto-discover** each other on the LAN and continuously **measure every link** (ICMP + high-rate UDP with jitter/micro-drop capture) plus the **gateway** — to isolate whether a fault is an endpoint NIC, a shared switch, or the router. Original symptom: Moonlight stutter Ryzen→ProjectorPC. No web UI, no installer, no services, no terminal — double-click, approve one UAC prompt, read the window.

## Status (milestones N1–N3e complete + legacy retired, all on `main`)
- **N1** portable elevated Gio app shell + lifecycle. **N3a** identity + LAN multicast discovery. **N3b** discovery wired in (peers visible). **N3c** per-peer ICMP RTT/loss. **N3d** high-fidelity 200 Hz UDP probing (jitter + micro-drop episodes) + multi-homed primary-IP fix. **N3e** default-gateway probing. Each verified live on ryzen + ProjectorPC.
- Legacy web/service stack **retired** (web UI, kardianos service, coordinator, agentsvc, svcctl, launch, readiness, localsettings, old `cmd/netlogger`).
- cgo-free; `go test -race` unavailable. Full suite green. Active packages well-covered (appcore 93%, discovery 87%, firewall 93%, probe 92%; ui logic extracted + tested, Gio render aside).

## Build & run
- **App build:** `./scripts/build-app.ps1` → `bin/NetLogger.exe` (windowsgui, elevated via embedded `requireAdministrator` manifest, ~18 MB with bundled iperf3). Requires `goversioninfo` on PATH (`go install github.com/josephspurrier/goversioninfo/cmd/goversioninfo@latest`).
- Go is at `C:\Program Files\Go\bin` (not on default PATH; prepend it). Commit footer: `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`. **Avoid `"`/`>` in PowerShell here-string commit bodies** (use Bash `git commit -m`). The `gofmt -l` listing source files is a Windows CRLF/autocrlf artifact, not a real issue.
- **Run:** double-click `bin\NetLogger.exe` on each machine → UAC → a native window shows data dir, iperf3 status, self-probe, **gateway** RTT/loss, and **discovered peers** with per-link RTT / jitter / loss / drop-episodes. Close the window (X) for a clean shutdown. The app self-adds firewall rules (program inbound + ICMP echo) when elevated.

## Architecture (one portable binary per host, symmetric peers)
**Active app** (`cmd/netlogger-app` → `internal/...`):
- `appcore` — in-process engine controller: opens the SQLite store, starts the bundled iperf3 server + UDP echo responder, runs self-probe + per-peer ICMP + 200 Hz UDP + gateway loops, exposes a thread-safe `Snapshot`. Injectable seams (`Ping`, `StartIperf`, `ProbeUDP`, `Discovery`, `GatewayIP`) for tests.
- `discovery` — UDP multicast announce/listen on a private group `239.255.74.76:48076` (TTL 1); peer table dedups by persistent UUID with TTL expiry; nodes advertise their **primary outbound IP** (multi-homed fix); graceful "bye".
- `identity` (persistent node UUID), `gateway` (default-gateway discovery via jackpal/gateway), `firewall` (program + ICMP allow, netsh), `datadir` (portable data dir + write-probe), `applog` (file log), `singleton` (named-mutex single instance), `ui` (Gio window; `statusLines` is the tested pure render logic).
- Reused engine: `probe` (ICMP via pro-bing + isochronous UDP `ProbeUDP`/`UDPEcho`), `store` (WAL SQLite), `iperf` (bundled iperf3 + always-on server + co-located/no-console exec), `clock`, `version`.

**Kept engine library (tested, not yet wired into the app — for upcoming milestones):** `correlate` (interval-overlap event correlation), `score` (BFS path attribution + component health/coverage, uses `config` topology), `classify` (bufferbloat-vs-fault, LAN-vs-WAN), `sysinfo` (NIC counters), `mesh` (agent sync API + puller + clock-offset), `httpauth` (bearer + Host allowlist), `config` (topology model: nodes + links).

## Design docs
- **Authoritative spec:** `docs/superpowers/specs/2026-06-15-netlogger-portable-design.md` (portable recalibration; supersedes the 2026-06-04 service/web design for the presentation + lifecycle layers).
- **Plans:** `docs/superpowers/plans/2026-06-15-netlogger-portable-n{1,3a,3b,3c,3d,3e}-*.md`.
- Original problem brief: `network-diagnostic-handoff.md`.

## Real network (user's home LAN)
Modem(Calix C5500XK)→Router(TP-Link BE9300)→Switch1(Tenda TEM2010F 2.5G)→{NAS(QNAP TS-563); ProjectorPC(I219-V 1G via UGREEN coupler); Switch2(Tenda)→Ryzen(Killer E3100G 2.5G **+ AX1675x Wi-Fi** — multi-homed!), NCASE(Intel I226-V 2.5G)}. **LAN is 2.5G end-to-end.** Leading hardware suspects: I226-V/E3100G **EEE dropout** > Switch1 > coupler.

## Diagnostic findings so far (live, on ryzen + ProjectorPC)
- The wired ryzen↔ProjectorPC link measures **clean** (sub-ms jitter, ~0 drops) at idle and under a 4 GB file write — i.e. bulk TCP doesn't reproduce the fault, and the **direct wired link is not the cause** of the disconnect observed.
- During a real **RDP disconnect** (user remotes in from a third LAN machine), the ryzen↔ProjectorPC probe stayed clean → the fault is on a path **not between those two** (the RDP-source machine's link, Wi-Fi, or the shared router). Hence N3e (gateway probing) and the guidance to **run NetLogger on the RDP-source machine too**.
- ryzen is multi-homed (wired `.154` + Wi-Fi `.24`); for a clean wired diagnosis, disable Wi-Fi on ryzen during testing.

## Next steps (not yet built)
1. **Catch the fault in the act:** run NetLogger on all 3+ machines (incl. the RDP-source PC), reproduce the disconnect/stutter, read which row spikes (gateway = router; one peer = that NIC/cable; all-from-one-box = that NIC).
2. **N3f** — manual probe targets by IP (modem/NAS/router) + a GUI to add them.
3. **N2** — rich UI: the **Link Matrix** (N×N, CVD-safe color bands, asymmetry) + Timeline/Correlation, wiring in the kept `correlate`/`score`/`classify` engine. (Spec §5–6.)
4. **N4** — synchronized full-mesh UDP load round ("test all at once").
5. **Release:** code-sign the exe (SmartScreen/AV friction on unsigned self-elevating binaries — spec §11).
