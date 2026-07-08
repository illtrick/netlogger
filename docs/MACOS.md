# macOS Development Guide

The macOS port is complete: NetLogger runs natively (Apple Silicon + Intel,
universal binary) and joins the Windows mesh as a full peer. This is the
working reference for building, testing, and extending it. History lives in
`docs/superpowers/plans/2026-07-03-netlogger-macos-port.md` (the original
plan) and `docs/MACOS-BUILD-KICKOFF.md` (the bootstrap runbook that started
it).

**Keeping up with the Windows line:** after each Windows build, a sync entry
is pushed to [docs/MAC-PARITY.md](MAC-PARITY.md) telling you what changed and
what the Mac must do (usually just rebuild + verify). Read its newest entry
whenever you `git pull`.

## TL;DR

```bash
./scripts/bootstrap-mac.sh   # once: Xcode CLT, Go ≥ 1.26, iperf3
./scripts/build-mac.sh       # → bin/NetLogger.app (universal, ad-hoc signed)
./scripts/test-mac.sh        # full 6-layer suite (see below)
open bin/NetLogger.app
```

Gio's macOS backend needs cgo + the Apple SDK, so the app binary **must be
built on a Mac**. The engine stays pure Go and cross-compiles from anywhere.

If `bootstrap-mac.sh` can't install Homebrew (needs an admin password), Go
and iperf3 also work from user space: official Go tarball into `~/.local/go`,
iperf3 built from source into `~/.local/bin`. Only `xcode-select --install`
is a hard system requirement.

## Test suite (`scripts/test-mac.sh`)

| Layer | What | Why |
|---|---|---|
| 1 | gofmt + go vet | static hygiene |
| 2 | `go test ./...` | all unit/fixture tests, UI included (cgo) |
| 3 | `go test -race` on concurrency-bearing packages | macOS is the only dev platform here where `-race` runs |
| 4 | engine cross-compile for `GOOS=windows` and `linux` | catches build-tag gaps / stranded symbols |
| 5 | live `nicstat.Collect()` smoke against real networksetup/ifconfig/netstat | asserts the UI's vocabulary contracts on real hardware |
| 6 | `build-mac.sh` + bundle assertions | universal archs, adhoc signature, plist version == `internal/version.Version`, `iconutil` accepts the icns |

Still manual (needs a Windows NetLogger node on the same LAN): mesh join,
heatmap/adapters/events cross-checks, speed/stress/internet tests, and the
`v1.1.0` tag — the checklist is Task 11 in the plan doc.

## How the port is put together

One pattern everywhere: **small build-tagged files define per-platform
values; untagged code consumes them.** Pure parsing/decision logic lives in
untagged files so it is unit-tested on every OS.

| Package | Platform seam | macOS behavior |
|---|---|---|
| `probe` | `privilegedICMP` const | `false` → unprivileged UDP-ICMP, **no elevation ever** |
| `datadir` | `preferBeside` / `fallbackBase` / `SidecarDir` | data in `~/Library/Application Support/NetLogger` when running from a bundle; **nothing is ever written inside the .app** (breaks the codesign seal) — this covers the DB, the log, and exports |
| `singleton` | flock on `$TMPDIR/<name>-<uid>.lock` | per-user single instance (parity with the Windows Local-scope mutex) |
| `keepawake` | `caffeinate -i -w <pid>` child | `-w` ties the assertion to our pid so the child can never outlive the app (Cmd+Q terminates without running Go defers) |
| `iperf` | `extraLookPaths`, `installHint` | Finder-launched apps get a minimal PATH; Homebrew paths are checked last; the error text says `brew install iperf3` |
| `nicstat` | `nicstat_darwin.go` + `wifi_darwin.go` | 3 execs per 8s poll (`networksetup`, one `ifconfig -a -v`, `netstat -ibnd`) + a ~10ms CoreWLAN call |
| `ui` | `customChrome` / `nativeDecorations` / `dragRegions` / `trafficLightInset` | see Window chrome below |

### NIC diagnostics on macOS

- **Wi-Fi**: `LinkSpeed` is the live PHY tx rate and `Detail` carries
  `802.11ax · ch 40 (5 GHz, 160 MHz) · RSSI −45 dBm · noise −91 dBm · WPA2`,
  read via CoreWLAN (`CWWiFiClient`) — **no root, no entitlement**. macOS
  location-redaction applies only to SSID/BSSID/scan results, not to the
  associated interface's own radio properties; SSID is deliberately not
  shown. Fallback when unassociated: `ifconfig`'s `downlink rate`/`link
  quality` lines.
- **Wired**: negotiated rate from the `media:` line (`1000baseT` → `1 Gbps`;
  a gigabit port linked at `100 Mbps` is the classic cable fault) and duplex
  in `Detail` (`half-duplex` = mismatch fault).
- **Counters**: BSD's netstat `Drop` column is the **TX** output-queue drop
  counter → `TxDiscards`. There is no rootless RX-drop source on macOS, so
  `RxDiscards` stays 0 here (Windows-only signal). `Power`/EEE is also
  Windows-only; macOS fills `Detail` instead and the UI shows whichever the
  platform provides.
- Avoid: `wdutil` (sudo), the legacy `airport` binary (removed from macOS),
  `system_profiler SPAirPortDataType` in the poll path (~8s runtime).

### Window chrome (the hard-won part)

- Gio requires `app.Main()` to be called from `main()` — it hands the main
  OS thread to Cocoa. Without it the app **bounces in the Dock and never
  shows a window**. All real work runs in a goroutine (`run()` in
  `cmd/netlogger-app/main.go`); `os.Exit` fires after its defers.
- Integrated title bar = `app.Decorated(false)` (Gio applies full-size
  content + transparent title bar and disables its fallback decorations by
  definition) **plus** re-showing the three standard window buttons in
  AppKit (`chrome_native_darwin.go`), re-asserted on every `ConfigEvent`
  because Gio's Configure re-hides them.
- **Never mutate the NSWindow style mask behind Gio's back**: its
  `updateWindowMode` watches `NSWindowStyleMaskFullSizeContentView`, decides
  the decoration mode changed, and draws its own Material-indigo title bar
  over yours.
- Window dragging / double-click-zoom work through Gio's drag regions
  (`dragArea` → `performWindowDragWithEvent`), gated by `dragRegions`.
  `customChrome` (the hand-drawn caption buttons + their click handlers)
  stays Windows-only.
- The app bar leads with `trafficLightInset` (78dp) so the brand clears the
  native buttons.

### Release mechanics

- `build-mac.sh` stamps `CFBundleShortVersionString`/`CFBundleVersion` from
  `internal/version.Version` via PlistBuddy — bump the Go constant and the
  bundle follows; never edit the plist version by hand.
- The build id gets `-dirty` when `git status --porcelain` is non-empty
  (staged and untracked included, same as `build-app.ps1`).
- The `.app` is **ad-hoc signed**: fine locally (keeps the firewall "Allow"
  sticky); other Macs get right-click → Open friction once. Real Developer
  ID + notarization is a separate future task.
- `genicon -icns` renders the icon; layer 6 of the test suite round-trips it
  through `iconutil` to prove macOS accepts it.

## Discovery on Wi-Fi (multicast-hostile networks)

Real-world finding from the first Mac↔Windows mesh session: the Wi-Fi AP
delivered the Mac's multicast announces upstream (Windows saw the Mac) but
filtered group traffic toward wireless clients (the Mac never heard
Windows) — classic IGMP-snooping behavior; verified with a raw listener
that received zero group packets in 15s while the Windows node announced
every 3s. Discovery therefore uses three transports (since 1.1.0):

1. multicast to the group (unchanged),
2. subnet broadcast per interface (APs pass broadcast where they filter
   multicast),
3. **unicast reply**: a node that hears an announce answers straight back
   to the source's discovery port (rate-limited, reply-flagged so replies
   are never re-answered) — completes discovery even when multicast is
   one-way.

**Both ends need ≥ 1.1.0** — an old 1.0.0 Windows node never replies, so a
Mac behind a filtering AP won't see it until Windows is rebuilt.

## Known issues / limitations

- **Upstream Gio bug, now mitigated**: `gio@v0.10.0 os_macos.go
  window.init` panics (`misuse of an invalid Handle`) instead of returning
  the error when display-link creation fails — which happens reliably when
  every display is asleep (launch at login, lid closed; reproduced with
  `pmset displaysleepnow`). Handled two ways in `cmd/netlogger-app`:
  `waitForDisplay` (CGDisplayIsAsleep poll) holds the engine+UI until a
  display wakes, and `uiPanicRecover` re-execs the app if Gio panics
  anyway (10s backoff, capped). Verified live under forced display sleep.
  Still worth an upstream report.
- No tray on macOS: closing the window quits (NSStatusItem needs objc
  bridging — future work, e.g. via purego).
- iperf3 is not bundled in the .app (would complicate signing); Homebrew or
  a binary next to `Contents/MacOS/` both resolve.
- Linux stubs are untouched by design: `!windows && !darwin` files keep the
  old no-op behavior.
