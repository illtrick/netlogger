# macOS Build — Kickoff Runbook

This is the single entry point for building the macOS version of NetLogger **on a Mac**.
Everything needed is already in this repo; nothing else has to be transferred.

Why on a Mac: Gio's macOS backend links Apple's Cocoa/Metal frameworks through
cgo, so the app binary cannot be cross-compiled from Windows. The engine is
pure Go and already cross-compiles; only the GUI + `.app` packaging need the Mac.

---

## 0. Prerequisites (once)

From the repo root on the Mac:

```bash
./scripts/bootstrap-mac.sh
```

Installs Xcode Command Line Tools, Homebrew, Go ≥ 1.26, and iperf3. Re-run it if
it tells you to finish the CLT dialog first. If you prefer to install by hand:
Xcode CLT (`xcode-select --install`), Go ≥ 1.26 from https://go.dev/dl/, and
`brew install iperf3`.

Sanity check:

```bash
go version          # go1.26+ 
go build ./internal/appcore   # engine compiles
```

---

## 1. Kick it off with Claude Code (recommended)

Open this repo folder in Claude Code on the Mac and paste this prompt:

> You are on macOS, building the NetLogger mac port natively (cgo is available
> here). Read `docs/superpowers/plans/2026-07-03-netlogger-macos-port.md` and
> `docs/MACOS-BUILD-KICKOFF.md`, then implement the plan end-to-end using the
> `superpowers:subagent-driven-development` skill. **macOS adaptation:** the
> plan's Phase A verification commands are written for a Windows dev box
> (PowerShell `$env:...`, cross-compiling the engine for darwin). You don't need
> those — on this Mac just build and test natively after each task:
> `go build ./...` and `go test ./...`, adding `-race` where the plan calls for
> it. Because cgo works here, `go build ./...` compiles the real UI too, so the
> Phase A / Phase B split collapses: you can build and run `NetLogger.app`
> immediately. After the code tasks pass, run `./scripts/build-mac.sh` and work
> through the Phase B verification checklist (Task 11), which needs a Windows
> NetLogger node on the same LAN.

That's it — the plan is self-contained (exact files, code, and tests per task).

---

## 2. macOS adaptation cheat-sheet (for a human or the agent)

The plan was authored on Windows, so translate its verification steps:

| Plan says (Windows dev)                              | On the Mac, do instead                         |
| ---------------------------------------------------- | ---------------------------------------------- |
| `$env:CGO_ENABLED='0'; go test ./...`                | `go test ./...`                                |
| the `GOOS=darwin GOARCH=arm64 ... go build <engine>` cross-compile check | `go build ./...` (builds everything, UI included) |
| "darwin side of `internal/ui` can't be built here"   | it builds here — verify the UI compiles for real |
| `-race` "runs for the first time in Phase B"         | you can run `-race` from task 1 onward         |
| `git update-index --chmod=+x` for scripts            | `chmod +x scripts/*.sh` works directly         |

Everything else — the actual source files, test code, and `git commit` steps —
is identical on both OSes. The `go test ./internal/<pkg>/` commands are already
OS-neutral; only the `$env:` wrappers and the cross-compile are Windows-isms.

---

## 3. Building by hand (if you skip the agent)

Work through `docs/superpowers/plans/2026-07-03-netlogger-macos-port.md` tasks
1–10 in order (each is TDD: write test → run → implement → run → commit), then:

```bash
./scripts/build-mac.sh     # created by Task 10 → bin/NetLogger.app (universal, ad-hoc signed)
open bin/NetLogger.app
```

Then run the Task 11 checklist (permissions prompts, mesh join against a Windows
node, NIC/Adapters, the three tests, lifecycle).

---

## 4. What "done" looks like

- `go test ./...` and `go test -race ./internal/...` green on the Mac.
- `bin/NetLogger.app` launches with native window chrome (traffic lights, no
  custom caption buttons), no admin/elevation prompt.
- The Mac joins an existing Windows NetLogger mesh: peers, heatmap, Adapters,
  events, and all three tests work Mac↔Windows.
- Tag `v1.1.0` and push (final step of Task 11).

Known v1 limitations are listed at the bottom of the plan (no tray, iperf3 via
Homebrew, no EEE properties, ad-hoc signature). They are intentional, not bugs.
