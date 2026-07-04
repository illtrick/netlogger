# NetLogger macOS Port Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** NetLogger 1.x runs natively on macOS (Apple Silicon + Intel) as `NetLogger.app`, joins the existing Windows mesh as a full peer (discovery, probing, heatmap, events, all three tests), with a native macOS window and a scripted `.app` build.

**Architecture:** The engine (`internal/appcore`, `probe`, `discovery`, `store`, `iperf`, …) is already cross-platform pure Go — verified: it cross-compiles for `darwin/arm64` with `CGO_ENABLED=0` today. Every Windows-only package (`keepawake`, `singleton`, `nicstat`, `firewall`, `iperf` bundling, UI chrome/tray) already has a `//go:build !windows` no-op stub. The port = replace stubs with real darwin implementations where macOS has an equivalent, keep no-ops where it doesn't, and gate the custom Windows chrome behind a platform constant. Pure parsing/decision logic lives in **untagged** files (the existing `nicstat.parseNICs` pattern) so it is TDD-able from any OS; only thin shell-out glue is build-tagged.

**Tech Stack:** Go 1.26+, Gio v0.10.0 (macOS backend **requires cgo** — the app binary must be built ON a Mac with Xcode Command Line Tools; there is no cgo-free darwin path in `gioui.org/internal/gl`), pro-bing (unprivileged ICMP on darwin), `caffeinate` (sleep prevention), `netstat`/`ifconfig`/`networksetup` (NIC stats), Homebrew iperf3 (not bundled in v1).

---

## Design decisions (locked — do not relitigate during implementation)

1. **Build on a Mac, not cross-compile.** Gio's darwin backend needs cgo + Apple SDK. The repo's cgo-free rule stays true for the *engine* and for the Windows build; the macOS *app binary* is `CGO_ENABLED=1`. Bonus: on the Mac, `go test -race` finally works — the verification phase runs it.
2. **No elevation on macOS.** pro-bing supports unprivileged ICMP on darwin (UDP-datagram ICMP sockets). `SetPrivileged` becomes a per-platform constant. No admin prompt, no manifest.
3. **iperf3 is NOT bundled on macOS in v1.** There is no way to build/verify a darwin iperf3 from the Windows dev box. The existing graceful degradation already handles absence (clear error string in `RunClientCtx`). We add Homebrew path resolution (Finder-launched apps get `PATH=/usr/bin:/bin:/usr/sbin:/sbin` — `exec.LookPath` alone would miss `/opt/homebrew/bin/iperf3`) and document `brew install iperf3`. Monitoring works fully without it; speed/stress tests need it.
4. **No tray on macOS in v1.** NSStatusItem needs objc bridging (cgo or purego) — out of scope. Closing the window quits (the `tray_other.go` stubs already do this). Windows keeps close-to-tray.
5. **Native window chrome on macOS.** `app.Decorated(true)` — real traffic lights, native drag, native fullscreen. The app bar still renders brand + nav tabs + status, but hides the custom caption buttons and skips the drag-region ops. Gated by a `customChrome` build-tagged constant.
6. **Firewall is a no-op on macOS.** macOS ALF prompts the user automatically on first listen ("Allow incoming connections?"). The build script ad-hoc codesigns the .app so that prompt sticks. No `netsh` equivalent needed.
7. **Data dir**: never store inside the `.app` bundle. On darwin, if the exe lives under `*.app/Contents/`, skip the beside-exe candidate and go straight to `~/Library/Application Support/NetLogger`.
8. **NIC diagnostics on macOS = link status/speed + error/discard counters** from `ifconfig -v` / `netstat -ibnd`, with `networksetup -listallhardwareports` supplying human names and acting as the interface filter (which naturally excludes `lo0`, `utun*`, `awdl0`, …). EEE/power-saving properties are not exposed by macOS — `Power` stays empty (the UI already tolerates it: Wi-Fi adapters on Windows have empty `Power` too).

## Two phases

- **Phase A (Tasks 1–10):** all code. Every task is verifiable from ANY dev OS: pure-logic tests run natively, platform glue is verified by `GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build <engine packages>` cross-compile plus the full native Windows suite staying green.
- **Phase B (Task 11):** on-Mac build + manual verification checklist. Requires a Mac with Go 1.26+, Xcode CLT, and LAN access to a Windows NetLogger node.

**Cross-compile check used throughout Phase A** (PowerShell; from repo root):

```powershell
$env:PATH = "C:\Program Files\Go\bin;$env:PATH"
$env:CGO_ENABLED='0'; $env:GOOS='darwin'; $env:GOARCH='arm64'
go build ./internal/appcore ./internal/probe ./internal/discovery ./internal/iperf ./internal/store ./internal/nicstat ./internal/keepawake ./internal/singleton ./internal/firewall ./internal/datadir ./internal/gateway ./internal/identity ./internal/applog ./internal/appsettings
Remove-Item Env:GOOS, Env:GOARCH
```

Expected: no output (success). NOTE: `./...` will NOT build for darwin from Windows — `internal/ui` needs cgo + Apple SDK; that is expected and fine. Always `Remove-Item Env:GOOS, Env:GOARCH` afterward or the native test runs will break.

**Native suite check used throughout** (must stay green after every task):

```powershell
$env:CGO_ENABLED='0'; go test ./...
```

Expected: `ok` for every package, no `FAIL`.

## File Structure

**Create:**
- `internal/probe/privileged_windows.go` — `const privilegedICMP = true`
- `internal/probe/privileged_darwin.go` — `const privilegedICMP = false`
- `internal/probe/privileged_other.go` — `const privilegedICMP = true` (linux etc.; out of scope, keep current behavior)
- `internal/datadir/beside_windows.go`, `beside_darwin.go`, `beside_other.go` — `preferBeside(exeDir)` gate
- `internal/singleton/singleton_unix.go` (+ `singleton_unix_test.go`) — flock-based single instance for darwin/linux
- `internal/keepawake/keepawake_darwin.go` — `caffeinate -i` child process
- `internal/nicstat/macosparse.go` (+ `macosparse_test.go`) — UNTAGGED pure parsers for `netstat -ibnd`, `ifconfig -v`, `networksetup`
- `internal/nicstat/nicstat_darwin.go` — build-tagged `Collect()` shell-out glue
- `internal/ui/chrome_platform_windows.go`, `chrome_platform_other.go` — `customChrome` constant
- `cmd/netlogger-app/Info.plist` — bundle metadata
- `scripts/build-mac.sh` — universal binary + .app assembly + ad-hoc codesign

**Modify:**
- `internal/probe/icmp.go:25` — use `privilegedICMP`
- `internal/datadir/datadir.go` — honor `preferBeside`
- `internal/singleton/singleton_other.go` — narrow build tag to exclude darwin/linux
- `internal/keepawake/keepawake_other.go` — narrow build tag to exclude darwin
- `internal/iperf/iperf.go` — extra lookup paths (Homebrew) in `binary()`
- `internal/ui/chrome.go` — `dragArea` no-ops and caption buttons hidden when `!customChrome`
- `internal/ui/ui.go:31-33` — `app.Decorated(!customChrome)`
- `tools/genicon/main.go` — add `-icns` output
- `README.md` — macOS section

---

### Task 1: Unprivileged ICMP on macOS

pro-bing's `SetPrivileged(true)` requires raw sockets (admin on Windows, root on macOS). On darwin, `SetPrivileged(false)` uses UDP-datagram ICMP, which works for ANY user — this is what removes the elevation requirement entirely on macOS.

**Files:**
- Create: `internal/probe/privileged_windows.go`
- Create: `internal/probe/privileged_darwin.go`
- Create: `internal/probe/privileged_other.go`
- Modify: `internal/probe/icmp.go:25`

- [ ] **Step 1: Create the three constant files**

`internal/probe/privileged_windows.go`:

```go
//go:build windows

package probe

// privilegedICMP selects raw-socket ICMP. On Windows the app runs elevated
// (manifest) and raw sockets are the reliable path.
const privilegedICMP = true
```

`internal/probe/privileged_darwin.go`:

```go
//go:build darwin

package probe

// privilegedICMP selects raw-socket ICMP. macOS supports unprivileged
// UDP-datagram ICMP sockets for any user, so the app never needs root.
const privilegedICMP = false
```

`internal/probe/privileged_other.go`:

```go
//go:build !windows && !darwin

package probe

// privilegedICMP selects raw-socket ICMP. Non-darwin unix keeps the raw-socket
// path (unprivileged ICMP on linux depends on a sysctl; out of scope).
const privilegedICMP = true
```

- [ ] **Step 2: Wire the constant into icmp.go**

In `internal/probe/icmp.go` line 25, change:

```go
	pinger.SetPrivileged(true)
```

to:

```go
	pinger.SetPrivileged(privilegedICMP)
```

- [ ] **Step 3: Verify native suite + darwin cross-compile**

Run the native suite check and the cross-compile check from the header. Expected: all `ok`, cross-compile silent.

- [ ] **Step 4: Commit**

```bash
git add internal/probe/privileged_windows.go internal/probe/privileged_darwin.go internal/probe/privileged_other.go internal/probe/icmp.go
git commit -m "feat(macos): unprivileged ICMP on darwin via per-platform constant"
```

---

### Task 2: Data dir — never store inside the .app bundle

On macOS the executable lives at `NetLogger.app/Contents/MacOS/netlogger`; "beside the exe" would put the SQLite DB inside the bundle (breaks codesigning, survives badly across updates). Rule: on darwin, skip the beside-exe candidate when the exe is inside a bundle, and use `~/Library/Application Support` as the fallback base.

**Files:**
- Create: `internal/datadir/beside_windows.go`, `internal/datadir/beside_darwin.go`, `internal/datadir/beside_other.go`
- Modify: `internal/datadir/datadir.go`
- Test: `internal/datadir/datadir_test.go` (add cases — the pure `insideAppBundle` check is untagged and tests run on any OS)

- [ ] **Step 1: Write the failing test**

Append to `internal/datadir/datadir_test.go`:

```go
func TestInsideAppBundle(t *testing.T) {
	cases := []struct {
		dir  string
		want bool
	}{
		{"/Applications/NetLogger.app/Contents/MacOS", true},
		{"/Users/x/dev/netlogger/bin", false},
		{"/Users/x/My.app/Contents/MacOS", true},
		{"C:\\tools\\netlogger", false},
	}
	for _, c := range cases {
		if got := insideAppBundle(c.dir); got != c.want {
			t.Errorf("insideAppBundle(%q) = %v, want %v", c.dir, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run it — expect FAIL (`undefined: insideAppBundle`)**

```powershell
go test ./internal/datadir/ -run TestInsideAppBundle -v
```

- [ ] **Step 3: Implement**

Add to `internal/datadir/datadir.go` (untagged — pure string logic):

```go
// insideAppBundle reports whether dir is inside a macOS .app bundle, where
// data must never be written (breaks codesigning; lost on update).
func insideAppBundle(dir string) bool {
	return strings.Contains(filepath.ToSlash(dir), ".app/Contents/")
}
```

(Add `"strings"` to the imports.)

Create `internal/datadir/beside_windows.go` (complete file):

```go
//go:build windows

package datadir

import "os"

// preferBeside reports whether the beside-the-exe data dir should be tried
// first. Always true on Windows (the portable-app contract).
func preferBeside(exeDir string) bool { return true }

// fallbackBase returns %LOCALAPPDATA%, falling back to the OS temp dir.
func fallbackBase() string {
	if v := os.Getenv("LOCALAPPDATA"); v != "" {
		return v
	}
	return os.TempDir()
}
```

Create `internal/datadir/beside_darwin.go` (complete file):

```go
//go:build darwin

package datadir

import "os"

// preferBeside is true for a bare binary (dev builds, bin/ copies) but false
// when running from inside a .app bundle.
func preferBeside(exeDir string) bool { return !insideAppBundle(exeDir) }

// fallbackBase returns ~/Library/Application Support (via UserConfigDir),
// falling back to the OS temp dir.
func fallbackBase() string {
	if d, err := os.UserConfigDir(); err == nil {
		return d
	}
	return os.TempDir()
}
```

Create `internal/datadir/beside_other.go` (complete file):

```go
//go:build !windows && !darwin

package datadir

import "os"

func preferBeside(exeDir string) bool { return true }

func fallbackBase() string {
	if d, err := os.UserConfigDir(); err == nil {
		return d
	}
	return os.TempDir()
}
```

Modify `resolve` in `internal/datadir/datadir.go` to accept and honor the gate — replace the existing `resolve` function with:

```go
// resolve is the injectable core: prefer <exeDir>/NetLogger-data when allowed
// and writable, else <fallbackBase>/NetLogger.
func resolve(exeDir, fallbackBase string, beside bool, writable func(string) bool) (string, error) {
	if beside {
		cand := filepath.Join(exeDir, "NetLogger-data")
		if err := os.MkdirAll(cand, 0o755); err == nil && writable(cand) {
			return cand, nil
		}
	}
	fb := filepath.Join(fallbackBase, "NetLogger")
	if err := os.MkdirAll(fb, 0o755); err != nil {
		return "", fmt.Errorf("create fallback data dir %q: %w", fb, err)
	}
	if !writable(fb) {
		return "", fmt.Errorf("no writable data dir (tried beside-exe and %q)", fb)
	}
	return fb, nil
}
```

And `Resolve()` becomes:

```go
// Resolve returns the data dir to use, creating it if needed.
func Resolve() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate executable: %w", err)
	}
	dir := filepath.Dir(exe)
	return resolve(dir, fallbackBase(), preferBeside(dir), probeWritable)
}
```

Delete the old `localAppData()` from `datadir.go` — its role is taken by the per-platform `fallbackBase()` shown in the complete files above. Update any existing tests that call `resolve(...)` with the old 3-arg signature: pass `true` for `beside` to preserve their previous behavior, and add one case passing `beside=false` asserting the fallback is returned even when the exeDir IS writable:

```go
func TestResolveSkipsBesideWhenNotPreferred(t *testing.T) {
	exeDir := t.TempDir()
	fb := t.TempDir()
	got, err := resolve(exeDir, fb, false, func(string) bool { return true })
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := filepath.Join(fb, "NetLogger")
	if got != want {
		t.Errorf("resolve = %q, want fallback %q", got, want)
	}
}
```

- [ ] **Step 4: Run the package tests — expect PASS**

```powershell
go test ./internal/datadir/ -v
```

- [ ] **Step 5: Native suite + darwin cross-compile (header commands). Commit**

```bash
git add internal/datadir/
git commit -m "feat(macos): data dir skips .app bundle interior, falls back to Application Support"
```

---

### Task 3: Single instance via flock on darwin/linux

The Windows named-mutex has no unix equivalent; `flock(2)` on a lock file is the standard pattern. The lock auto-releases when the process dies (even on SIGKILL), matching the mutex semantics.

**Files:**
- Create: `internal/singleton/singleton_unix.go`
- Create: `internal/singleton/singleton_unix_test.go`
- Modify: `internal/singleton/singleton_other.go` (narrow the build tag)

- [ ] **Step 1: Create `internal/singleton/singleton_unix.go`**

```go
//go:build darwin || linux

package singleton

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Acquire takes an exclusive flock on a lock file derived from name. The lock
// is released automatically when the process exits (even if killed), matching
// the Windows named-mutex semantics. ok=false means another instance holds it.
func Acquire(name string) (release func(), ok bool, err error) {
	path := filepath.Join(os.TempDir(), name+".lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return func() {}, true, err // fail open, like the Windows path logs-and-continues
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if err == unix.EWOULDBLOCK {
			return func() {}, false, nil
		}
		return func() {}, true, err
	}
	return func() {
		unix.Flock(int(f.Fd()), unix.LOCK_UN)
		f.Close()
	}, true, nil
}
```

- [ ] **Step 2: Narrow `singleton_other.go`'s tag**

Change line 1 of `internal/singleton/singleton_other.go` from `//go:build !windows` to:

```go
//go:build !windows && !darwin && !linux
```

- [ ] **Step 3: Create `internal/singleton/singleton_unix_test.go`** (runs in Phase B on the Mac; written now so it's ready)

```go
//go:build darwin || linux

package singleton

import "testing"

func TestAcquireSecondCallerBlocked(t *testing.T) {
	rel1, ok, err := Acquire("netlogger-singleton-test")
	if err != nil || !ok {
		t.Fatalf("first acquire: ok=%v err=%v", ok, err)
	}
	defer rel1()

	// Same-process flock re-acquire on a NEW fd must be refused.
	rel2, ok2, err2 := Acquire("netlogger-singleton-test")
	if err2 != nil {
		t.Fatalf("second acquire err: %v", err2)
	}
	if ok2 {
		rel2()
		t.Fatal("second acquire succeeded; want blocked")
	}
}

func TestAcquireReleasedThenReacquired(t *testing.T) {
	rel, ok, err := Acquire("netlogger-singleton-test2")
	if err != nil || !ok {
		t.Fatalf("first acquire: ok=%v err=%v", ok, err)
	}
	rel()
	rel2, ok2, err2 := Acquire("netlogger-singleton-test2")
	if err2 != nil || !ok2 {
		t.Fatalf("re-acquire after release: ok=%v err=%v", ok2, err2)
	}
	rel2()
}
```

NOTE (verify during Phase B): flock semantics for two descriptors in the SAME process differ by OS — on macOS a second `open`+`flock(LOCK_EX|LOCK_NB)` of the same file from the same process FAILS with EWOULDBLOCK, which is what the first test asserts. If it flakes, replace the same-process assertion with a subprocess-based one (spawn `go run` helper) — do not weaken it to a smoke test.

- [ ] **Step 4: Native suite + darwin cross-compile (header commands — the unix file only compiles under the darwin cross-build, which is exactly what the cross-compile check exercises). Commit**

```bash
git add internal/singleton/
git commit -m "feat(macos): flock-based single instance on darwin/linux"
```

---

### Task 4: Keep-awake via caffeinate on darwin

Windows uses `SetThreadExecutionState`. macOS's supported no-root mechanism without cgo is spawning `/usr/bin/caffeinate -i` (prevents idle system sleep) for the app's lifetime and killing it on Stop. `caffeinate` ships with every macOS.

**Files:**
- Create: `internal/keepawake/keepawake_darwin.go`
- Modify: `internal/keepawake/keepawake_other.go` (narrow tag)

- [ ] **Step 1: Create `internal/keepawake/keepawake_darwin.go`**

```go
//go:build darwin

package keepawake

import (
	"log"
	"os/exec"
)

// Keeper holds a running `caffeinate -i` child that prevents idle system
// sleep while NetLogger monitors. Killed on Stop; dies with us if we crash
// (caffeinate exits when its parent's stdin closes is NOT relied on — the
// assertion simply lapses when the process is gone).
type Keeper struct {
	cmd *exec.Cmd
}

// Start launches caffeinate. A failure to start is logged, not fatal —
// monitoring works fine, the machine just may sleep.
func Start() *Keeper {
	cmd := exec.Command("/usr/bin/caffeinate", "-i")
	if err := cmd.Start(); err != nil {
		log.Printf("keepawake: caffeinate failed to start: %v", err)
		return &Keeper{}
	}
	return &Keeper{cmd: cmd}
}

// Stop kills the caffeinate child, releasing the sleep assertion.
func (k *Keeper) Stop() {
	if k.cmd != nil && k.cmd.Process != nil {
		_ = k.cmd.Process.Kill()
		_, _ = k.cmd.Process.Wait()
	}
}
```

- [ ] **Step 2: Narrow `keepawake_other.go`'s tag**

Change line 1 from `//go:build !windows` to:

```go
//go:build !windows && !darwin
```

- [ ] **Step 3: Native suite + darwin cross-compile (header commands). Commit**

```bash
git add internal/keepawake/
git commit -m "feat(macos): prevent idle sleep via caffeinate child process"
```

---

### Task 5: iperf3 resolution finds Homebrew installs

`internal/iperf/iperf.go`'s `binary()` resolves: bundled → co-located → `exec.LookPath`. A Finder-launched .app gets `PATH=/usr/bin:/bin:/usr/sbin:/sbin`, so LookPath will MISS a brew-installed iperf3. Add explicit well-known paths, checked after LookPath.

**Files:**
- Modify: `internal/iperf/iperf.go` (function `binary`, around line 123)
- Create: `internal/iperf/extrapaths_darwin.go`, `internal/iperf/extrapaths_other.go`
- Test: `internal/iperf/iperf_test.go` (or the package's existing test file — add to whichever file holds `binary`/`pick` coverage)

- [ ] **Step 1: Write the failing test** (untagged — pure logic, runs everywhere)

```go
func TestFirstExecutable(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "iperf3")
	if err := os.WriteFile(real, []byte("#!"), 0o755); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "nope", "iperf3")

	if got := firstExecutable([]string{missing, real}); got != real {
		t.Errorf("firstExecutable = %q, want %q", got, real)
	}
	if got := firstExecutable([]string{missing}); got != "" {
		t.Errorf("firstExecutable(miss) = %q, want empty", got)
	}
	if got := firstExecutable(nil); got != "" {
		t.Errorf("firstExecutable(nil) = %q, want empty", got)
	}
}
```

- [ ] **Step 2: Run it — expect FAIL (`undefined: firstExecutable`)**

```powershell
go test ./internal/iperf/ -run TestFirstExecutable -v
```

- [ ] **Step 3: Implement**

Add to `internal/iperf/iperf.go` (untagged):

```go
// firstExecutable returns the first path in paths that exists as a regular
// file, or "".
func firstExecutable(paths []string) string {
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			return p
		}
	}
	return ""
}
```

Create `internal/iperf/extrapaths_darwin.go`:

```go
//go:build darwin

package iperf

// extraLookPaths are well-known install locations checked after PATH. A
// Finder-launched app inherits a minimal PATH that excludes Homebrew.
var extraLookPaths = []string{
	"/opt/homebrew/bin/iperf3", // Apple Silicon Homebrew
	"/usr/local/bin/iperf3",    // Intel Homebrew / manual installs
}
```

Create `internal/iperf/extrapaths_other.go`:

```go
//go:build !darwin

package iperf

var extraLookPaths []string
```

In `binary()` (iperf.go ~line 123), after the existing `look(name)` PATH attempt fails, add the extra-paths check so the resolution order is: bundled → co-located → PATH → extraLookPaths. Locate the final return of the function and insert before returning "":

```go
	if p := firstExecutable(extraLookPaths); p != "" {
		return p
	}
```

(Read the current `binary()` body first and keep its existing structure — only append this as the last resort before the not-found return.)

- [ ] **Step 4: Run package tests — expect PASS. Native suite + darwin cross-compile. Commit**

```bash
git add internal/iperf/
git commit -m "feat(macos): resolve Homebrew iperf3 for Finder-launched apps"
```

---

### Task 6: NIC diagnostics on macOS — pure parsers (TDD)

Three untagged pure parsers with fixture tests (they run on Windows too), then Task 7 wires them to real commands. Data sources:
- `networksetup -listallhardwareports` → device→human-name map AND the filter for "real" interfaces (excludes lo0/utun/awdl/bridge noise).
- `ifconfig -v <dev>` → media line (link speed) + status line.
- `netstat -ibnd` → cumulative Ierrs/Oerrs/Drop/Ibytes/Obytes per interface (the `<Link#N>` row).

**Files:**
- Create: `internal/nicstat/macosparse.go`
- Create: `internal/nicstat/macosparse_test.go`

- [ ] **Step 1: Write the failing tests** — `internal/nicstat/macosparse_test.go`:

```go
package nicstat

import "testing"

const hwPortsFixture = `Hardware Port: Ethernet
Device: en0
Ethernet Address: f0:18:98:aa:bb:cc

Hardware Port: Wi-Fi
Device: en1
Ethernet Address: f0:18:98:dd:ee:ff

Hardware Port: Thunderbolt Bridge
Device: bridge0
Ethernet Address: 36:5d:22:11:22:33

VLAN Configurations
===================`

func TestParseHardwarePorts(t *testing.T) {
	got := parseHardwarePorts(hwPortsFixture)
	if len(got) != 3 {
		t.Fatalf("ports = %d, want 3", len(got))
	}
	if got["en0"] != "Ethernet" || got["en1"] != "Wi-Fi" || got["bridge0"] != "Thunderbolt Bridge" {
		t.Errorf("map wrong: %#v", got)
	}
}

const ifconfigFixture = `en0: flags=8863<UP,BROADCAST,SMART,RUNNING,SIMPLEX,MULTICAST> mtu 1500
	options=6463<RXCSUM,TXCSUM,TSO4,TSO6,CHANNEL_IO,PARTIAL_CSUM,ZEROINVERT_CSUM>
	ether f0:18:98:aa:bb:cc
	inet 192.168.0.42 netmask 0xffffff00 broadcast 192.168.0.255
	media: autoselect (1000baseT <full-duplex>)
	status: active
`

const ifconfigDownFixture = `en0: flags=8863<UP,BROADCAST,SMART,RUNNING,SIMPLEX,MULTICAST> mtu 1500
	media: autoselect (none)
	status: inactive
`

func TestParseIfconfig(t *testing.T) {
	speed, status := parseIfconfig(ifconfigFixture)
	if speed != "1 Gbps" {
		t.Errorf("speed = %q, want 1 Gbps", speed)
	}
	if status != "Up" {
		t.Errorf("status = %q, want Up", status)
	}

	speed, status = parseIfconfig(ifconfigDownFixture)
	if status != "Disconnected" {
		t.Errorf("down status = %q, want Disconnected", status)
	}
	if speed != "" {
		t.Errorf("down speed = %q, want empty", speed)
	}
}

func TestMediaToSpeed(t *testing.T) {
	cases := []struct{ media, want string }{
		{"10Gbase-T <full-duplex>", "10 Gbps"},
		{"5000Base-T <full-duplex>", "5 Gbps"},
		{"2500Base-T <full-duplex>", "2.5 Gbps"},
		{"1000baseT <full-duplex>", "1 Gbps"},
		{"100baseTX <full-duplex>", "100 Mbps"},
		{"autoselect", "autoselect"}, // unknown → raw passthrough
		{"none", ""},
	}
	for _, c := range cases {
		if got := mediaToSpeed(c.media); got != c.want {
			t.Errorf("mediaToSpeed(%q) = %q, want %q", c.media, got, c.want)
		}
	}
}

const netstatFixture = `Name       Mtu   Network       Address            Ipkts Ierrs     Ibytes    Opkts Oerrs     Obytes  Coll Drop
lo0        16384 <Link#1>                          41684     0    9152351    41684     0    9152351     0   0
lo0        16384 127           127.0.0.1           41684     -    9152351    41684     -    9152351     -   -
en0        1500  <Link#12>   f0:18:98:aa:bb:cc   9876543     2 9876543210  8765432     1 8765432109     0   5
en0        1500  192.168.0     192.168.0.42      9876543     - 9876543210  8765432     - 8765432109     -   -
en1        1500  <Link#13>   f0:18:98:dd:ee:ff    123456     0  123456789   654321     0  987654321     0   0
`

func TestParseNetstatIB(t *testing.T) {
	got := parseNetstatIB(netstatFixture)
	en0, ok := got["en0"]
	if !ok {
		t.Fatalf("en0 missing: %#v", got)
	}
	if en0.RxErrors != 2 || en0.TxErrors != 1 || en0.RxDiscards != 5 {
		t.Errorf("en0 counters = %+v", en0)
	}
	if en0.RxBytes != 9876543210 || en0.TxBytes != 8765432109 {
		t.Errorf("en0 bytes = %+v", en0)
	}
	if _, ok := got["lo0"]; !ok {
		t.Error("lo0 should parse (filtering happens later, by hardware-ports map)")
	}
}
```

- [ ] **Step 2: Run — expect FAIL (undefined functions)**

```powershell
go test ./internal/nicstat/ -run "TestParseHardwarePorts|TestParseIfconfig|TestMediaToSpeed|TestParseNetstatIB" -v
```

- [ ] **Step 3: Implement `internal/nicstat/macosparse.go`** (NO build tag — pure logic):

```go
package nicstat

// macOS NIC parsing: pure functions over the text output of networksetup,
// ifconfig, and netstat. Untagged so the fixtures test on every dev OS; the
// darwin-only Collect glue lives in nicstat_darwin.go.

import (
	"strconv"
	"strings"
)

// parseHardwarePorts maps device name → human port name from
// `networksetup -listallhardwareports`. Doubles as the interface allowlist:
// devices absent from it (lo0, utun*, awdl0, …) are not physical ports.
func parseHardwarePorts(out string) map[string]string {
	ports := map[string]string{}
	var port string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "Hardware Port: "); ok {
			port = v
			continue
		}
		if v, ok := strings.CutPrefix(line, "Device: "); ok && port != "" {
			ports[v] = port
			port = ""
		}
	}
	return ports
}

// parseIfconfig extracts (link speed, status) from `ifconfig -v <dev>`.
// Status maps to the Windows Get-NetAdapter vocabulary the UI and the NIC
// change-event detector already understand: "Up" / "Disconnected" / "Unknown".
func parseIfconfig(out string) (speed, status string) {
	status = "Unknown"
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "media: "); ok {
			// "autoselect (1000baseT <full-duplex>)" → inner "1000baseT <full-duplex>"
			if i := strings.IndexByte(v, '('); i >= 0 {
				if j := strings.IndexByte(v[i:], ')'); j > 0 {
					v = v[i+1 : i+j]
				}
			}
			speed = mediaToSpeed(v)
		}
		if v, ok := strings.CutPrefix(line, "status: "); ok {
			switch v {
			case "active":
				status = "Up"
			case "inactive":
				status = "Disconnected"
			default:
				status = v
			}
		}
	}
	return speed, status
}

// mediaToSpeed converts an ifconfig media token to the "<n> Gbps"/"<n> Mbps"
// vocabulary the Windows collector reports; unknown tokens pass through raw.
func mediaToSpeed(media string) string {
	m := strings.ToLower(media)
	switch {
	case m == "none":
		return ""
	case strings.HasPrefix(m, "10gbase"):
		return "10 Gbps"
	case strings.HasPrefix(m, "5000base"):
		return "5 Gbps"
	case strings.HasPrefix(m, "2500base"):
		return "2.5 Gbps"
	case strings.HasPrefix(m, "1000base"):
		return "1 Gbps"
	case strings.HasPrefix(m, "100base"):
		return "100 Mbps"
	case strings.HasPrefix(m, "10base"):
		return "10 Mbps"
	}
	// Strip the duplex suffix for readability on unknown tokens.
	if i := strings.IndexByte(media, '<'); i > 0 {
		media = strings.TrimSpace(media[:i])
	}
	return media
}

// linkCounters is one interface's cumulative counters from `netstat -ibnd`.
type linkCounters struct {
	RxErrors, TxErrors, RxDiscards int64
	RxBytes, TxBytes               int64
}

// parseNetstatIB reads the `<Link#N>` row per interface from `netstat -ibnd`.
// Field layout after the <Link#N> token: [mac?] Ipkts Ierrs Ibytes Opkts
// Oerrs Obytes Coll Drop — the MAC column is absent for interfaces without
// one (lo0), so it is skipped by shape (contains ':').
func parseNetstatIB(out string) map[string]linkCounters {
	res := map[string]linkCounters{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		link := -1
		for i, tok := range f {
			if strings.HasPrefix(tok, "<Link#") {
				link = i
				break
			}
		}
		if link < 0 || len(f) < link+8 {
			continue
		}
		j := link + 1
		if j < len(f) && strings.Contains(f[j], ":") {
			j++ // skip MAC
		}
		if len(f) < j+8 {
			continue
		}
		n := func(k int) int64 {
			v, _ := strconv.ParseInt(f[j+k], 10, 64)
			return v
		}
		res[f[0]] = linkCounters{
			RxErrors:   n(1),
			RxBytes:    n(2),
			TxErrors:   n(4),
			TxBytes:    n(5),
			RxDiscards: n(7),
		}
	}
	return res
}
```

- [ ] **Step 4: Run — expect PASS**

```powershell
go test ./internal/nicstat/ -v
```

- [ ] **Step 5: Commit**

```bash
git add internal/nicstat/macosparse.go internal/nicstat/macosparse_test.go
git commit -m "feat(macos): pure parsers for networksetup/ifconfig/netstat NIC data"
```

---

### Task 7: NIC diagnostics on macOS — Collect glue

**Files:**
- Create: `internal/nicstat/nicstat_darwin.go`
- Modify: `internal/nicstat/nicstat_other.go` (narrow tag)

- [ ] **Step 1: Create `internal/nicstat/nicstat_darwin.go`**

```go
//go:build darwin

package nicstat

import (
	"os/exec"
	"sort"
)

// Collect gathers per-adapter state on macOS from networksetup (names +
// physical-port filter), ifconfig (speed/status), and netstat (counters).
// EEE/power-saving properties are not exposed by macOS; Power stays empty.
func Collect() []NIC {
	ports := runParse("networksetup", []string{"-listallhardwareports"}, parseHardwarePorts)
	if len(ports) == 0 {
		return nil
	}
	counters := runParse("netstat", []string{"-ibnd"}, parseNetstatIB)

	devs := make([]string, 0, len(ports))
	for dev := range ports {
		devs = append(devs, dev)
	}
	sort.Strings(devs) // stable order for the UI and change detection

	var nics []NIC
	for _, dev := range devs {
		out, err := exec.Command("ifconfig", "-v", dev).Output()
		if err != nil {
			continue // port exists but interface is gone (e.g. unplugged adapter)
		}
		speed, status := parseIfconfig(string(out))
		n := NIC{
			Name:        dev,
			Description: ports[dev],
			LinkSpeed:   speed,
			Status:      status,
		}
		if c, ok := counters[dev]; ok {
			n.RxErrors, n.TxErrors = c.RxErrors, c.TxErrors
			n.RxDiscards = c.RxDiscards
			n.RxBytes, n.TxBytes = c.RxBytes, c.TxBytes
		}
		nics = append(nics, n)
	}
	return nics
}

// runParse runs a command and applies a pure parser, returning the zero map
// on any error (NIC diagnostics degrade, never fail the app).
func runParse[T any](name string, args []string, parse func(string) T) T {
	var zero T
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return zero
	}
	return parse(string(out))
}
```

- [ ] **Step 2: Narrow `nicstat_other.go`'s tag**

Change line 1 from `//go:build !windows` to:

```go
//go:build !windows && !darwin
```

- [ ] **Step 3: Native suite + darwin cross-compile (header commands — the darwin file compiles only in the cross-build). Commit**

```bash
git add internal/nicstat/
git commit -m "feat(macos): NIC Collect via networksetup/ifconfig/netstat"
```

---

### Task 8: Native window chrome on macOS

On Windows the window is undecorated and `titleBar` draws everything including caption buttons. On macOS we keep the native title bar (traffic lights, native drag/fullscreen) and render the same app bar minus caption buttons and minus drag-region ops.

**Files:**
- Create: `internal/ui/chrome_platform_windows.go`, `internal/ui/chrome_platform_other.go`
- Modify: `internal/ui/chrome.go` (`dragArea`, `titleBar`)
- Modify: `internal/ui/ui.go:31-33`

- [ ] **Step 1: Create the platform constants**

`internal/ui/chrome_platform_windows.go`:

```go
//go:build windows

package ui

// customChrome selects the undecorated window with the hand-drawn title bar
// (drag regions + caption buttons). Windows-only; macOS uses native chrome.
const customChrome = true
```

`internal/ui/chrome_platform_other.go`:

```go
//go:build !windows

package ui

const customChrome = false
```

- [ ] **Step 2: Gate the decoration option in `internal/ui/ui.go`**

Lines 31–33 currently:

```go
	w.Option(app.Title("NetLogger"), app.Size(unit.Dp(880), unit.Dp(720)),
		app.MinSize(unit.Dp(760), unit.Dp(520)), // keep the layout from squishing into overlap
		app.Decorated(false))                    // the app bar IS the title bar (brand·nav·status·caption)
```

Change the last option to:

```go
		app.Decorated(!customChrome))            // Windows: the app bar IS the title bar; macOS: native chrome
```

- [ ] **Step 3: Gate `dragArea` in `internal/ui/chrome.go`**

At the top of `dragArea` (currently line ~51), add an early return so decorated platforms skip the caption-region op:

```go
func dragArea(gtx layout.Context, w layout.Widget) layout.Dimensions {
	if !customChrome {
		return w(gtx) // native title bar handles dragging
	}
	macro := op.Record(gtx.Ops)
	...
```

- [ ] **Step 4: Gate the caption buttons in `titleBar`**

In `titleBar` (chrome.go), the flex row currently ends with:

```go
			layout.Rigid(gapX(10)),
			captionBtn(th, &cs.minBtn, glyphMin, false, barH),
			captionBtn(th, &cs.maxBtn, glyphForMax(cs.maximized), false, barH),
			captionBtn(th, &cs.closeBtn, glyphClose, true, barH),
		)
```

Rebuild the row with a children slice so the caption cluster is appended only under custom chrome. Replace the whole `row :=` closure body's flex call with:

```go
		children := []layout.FlexChild{
			// Brand block: draggable (clicking the wordmark does nothing else).
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return dragArea(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(18), Right: unit.Dp(18)}.Layout(gtx,
						func(gtx layout.Context) layout.Dimensions {
							return layout.W.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								gtx.Constraints.Min.Y = barH
								return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return brand(gtx, th)
								})
							})
						})
				})
			}),
			pill(navDash, navDashboard), pill(navTst, navTests), pill(navEvt, navEvents),
			// The big middle stretch: the main drag surface.
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return dragArea(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, barH)}
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return dragArea(gtx, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.Y = barH
					return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						l := material.Label(th, unit.Sp(12), status)
						l.Color = colTextMut
						return l.Layout(gtx)
					})
				})
			}),
		}
		if customChrome {
			children = append(children,
				layout.Rigid(gapX(10)),
				captionBtn(th, &cs.minBtn, glyphMin, false, barH),
				captionBtn(th, &cs.maxBtn, glyphForMax(cs.maximized), false, barH),
				captionBtn(th, &cs.closeBtn, glyphClose, true, barH),
			)
		} else {
			children = append(children, layout.Rigid(gapX(16))) // right margin where captions would be
		}
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, children...)
```

(The caption click handlers in ui.go — minimize/maximize/close-to-tray — fire only from these widgets, so they are naturally dead on macOS; `applyDarkTitleBar` and `startTray` are already `!windows` no-ops. Do not touch them.)

- [ ] **Step 5: Verify — Windows build must be pixel-identical**

```powershell
$env:CGO_ENABLED='0'; go build ./... ; go test ./...
go vet ./internal/ui/
```

Expected: builds green, all tests `ok`. (The darwin side of `internal/ui` cannot be compiled from Windows — Gio needs the Apple SDK. It is covered in Phase B.)

- [ ] **Step 6: Commit**

```bash
git add internal/ui/chrome_platform_windows.go internal/ui/chrome_platform_other.go internal/ui/chrome.go internal/ui/ui.go
git commit -m "feat(macos): native window chrome — custom title bar becomes Windows-only"
```

---

### Task 9: genicon emits .icns

The `.app` needs `NetLogger.icns`. The ICNS container is trivial: magic `"icns"` + big-endian total length, then chunks of (4-byte type + 4-byte length + payload), and modern types accept raw PNG payloads. Reuse genicon's existing per-size rendering.

**Files:**
- Modify: `tools/genicon/main.go`
- Test: `tools/genicon/icns_test.go`

- [ ] **Step 1: Write the failing test** — `tools/genicon/icns_test.go`:

```go
package main

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestWriteICNS(t *testing.T) {
	var buf bytes.Buffer
	// Two fake "PNG" payloads are fine — writeICNS only frames bytes.
	err := writeICNS(&buf, []icnsChunk{
		{"ic07", []byte("png-a")},
		{"ic08", []byte("png-bb")},
	})
	if err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	if string(b[:4]) != "icns" {
		t.Fatalf("magic = %q", b[:4])
	}
	total := binary.BigEndian.Uint32(b[4:8])
	if int(total) != len(b) {
		t.Errorf("total len field = %d, actual %d", total, len(b))
	}
	if string(b[8:12]) != "ic07" {
		t.Errorf("first chunk type = %q", b[8:12])
	}
	c1len := binary.BigEndian.Uint32(b[12:16])
	if int(c1len) != 8+len("png-a") {
		t.Errorf("chunk1 len = %d, want %d", c1len, 8+len("png-a"))
	}
}
```

- [ ] **Step 2: Run — expect FAIL (`undefined: writeICNS` / `icnsChunk`)**

```powershell
go test ./tools/genicon/ -run TestWriteICNS -v
```

- [ ] **Step 3: Implement.** Add to `tools/genicon/main.go`:

```go
// icnsChunk is one icon element: a 4-char OSType and a raw PNG payload.
type icnsChunk struct {
	Type string
	PNG  []byte
}

// writeICNS frames chunks into the ICNS container: "icns" + total length,
// then (type + length + payload) per chunk. Lengths include the 8-byte header.
func writeICNS(w io.Writer, chunks []icnsChunk) error {
	total := 8
	for _, c := range chunks {
		total += 8 + len(c.PNG)
	}
	hdr := make([]byte, 8)
	copy(hdr, "icns")
	binary.BigEndian.PutUint32(hdr[4:], uint32(total))
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	for _, c := range chunks {
		ch := make([]byte, 8)
		copy(ch, c.Type)
		binary.BigEndian.PutUint32(ch[4:], uint32(8+len(c.PNG)))
		if _, err := w.Write(ch); err != nil {
			return err
		}
		if _, err := w.Write(c.PNG); err != nil {
			return err
		}
	}
	return nil
}
```

(Imports: add `"encoding/binary"` and `"io"` if absent.)

Then wire an `-icns` flag into `main()`: when set, render the icon at the sizes below with the EXISTING size-render helper genicon already uses for the ICO (read `main.go` first and reuse its render+PNG-encode path — do not duplicate drawing code), and write these chunks:

| OSType | pixels | meaning |
|---|---|---|
| `ic11` | 32 | 16pt@2x |
| `ic12` | 64 | 32pt@2x |
| `ic07` | 128 | 128pt |
| `ic13` | 256 | 128pt@2x |
| `ic08` | 256 | 256pt |
| `ic14` | 512 | 256pt@2x |
| `ic09` | 512 | 512pt |
| `ic10` | 1024 | 512pt@2x |

Flag behavior: `go run ./tools/genicon -icns -o out.icns` writes ICNS; without `-icns` the ICO path is unchanged. If the existing renderer caps below 1024px, render at its max for the larger chunks — macOS scales down gracefully; do not add a new drawing path for this.

- [ ] **Step 4: Run — expect PASS. Also regenerate nothing (the committed icon.ico must not change): `git status` shows only `tools/genicon/` modified. Commit**

```bash
git add tools/genicon/
git commit -m "feat(macos): genicon -icns emits the .app icon"
```

---

### Task 10: Info.plist + build-mac.sh + README

**Files:**
- Create: `cmd/netlogger-app/Info.plist`
- Create: `scripts/build-mac.sh`
- Modify: `README.md`

- [ ] **Step 1: Create `cmd/netlogger-app/Info.plist`**

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key>            <string>NetLogger</string>
	<key>CFBundleDisplayName</key>     <string>NetLogger</string>
	<key>CFBundleIdentifier</key>      <string>com.illtrick.netlogger</string>
	<key>CFBundleExecutable</key>      <string>netlogger</string>
	<key>CFBundlePackageType</key>     <string>APPL</string>
	<key>CFBundleShortVersionString</key> <string>1.0.0</string>
	<key>CFBundleVersion</key>         <string>1.0.0</string>
	<key>CFBundleIconFile</key>        <string>NetLogger</string>
	<key>LSMinimumSystemVersion</key>  <string>11.0</string>
	<key>NSHighResolutionCapable</key> <true/>
	<key>NSLocalNetworkUsageDescription</key>
	<string>NetLogger discovers and continuously probes other NetLogger nodes on your local network to diagnose LAN faults.</string>
</dict>
</plist>
```

(Keep `CFBundleShortVersionString` in sync with `internal/version.Version` on future releases — note this in the script header comment.)

- [ ] **Step 2: Create `scripts/build-mac.sh`** (runs ON macOS only):

```bash
#!/usr/bin/env bash
# Release build for macOS: universal binary + NetLogger.app + ad-hoc codesign.
# Requires: macOS 11+, Go 1.26+, Xcode Command Line Tools (xcode-select --install).
# Gio's darwin backend needs cgo, so this CANNOT be cross-compiled from Windows/Linux.
# Keep CFBundleShortVersionString in cmd/netlogger-app/Info.plist in sync with
# internal/version.Version when cutting a release.
set -euo pipefail
cd "$(dirname "$0")/.."

BUILD="$(git rev-parse --short HEAD)$(git diff --quiet || echo '-dirty')"
LDFLAGS="-s -w -X netlogger/internal/version.Build=${BUILD}"

# The Windows COFF resource must not be linked into a darwin binary.
rm -f cmd/netlogger-app/resource.syso

mkdir -p bin
echo "Building darwin/arm64 + darwin/amd64 (build ${BUILD})..."
CGO_ENABLED=1 GOARCH=arm64 go build -ldflags "${LDFLAGS}" -o bin/netlogger-darwin-arm64 ./cmd/netlogger-app
CGO_ENABLED=1 GOARCH=amd64 go build -ldflags "${LDFLAGS}" -o bin/netlogger-darwin-amd64 ./cmd/netlogger-app
lipo -create -output bin/netlogger-universal bin/netlogger-darwin-arm64 bin/netlogger-darwin-amd64

APP="bin/NetLogger.app"
rm -rf "${APP}"
mkdir -p "${APP}/Contents/MacOS" "${APP}/Contents/Resources"
cp cmd/netlogger-app/Info.plist "${APP}/Contents/Info.plist"
cp bin/netlogger-universal "${APP}/Contents/MacOS/netlogger"
go run ./tools/genicon -icns -o "${APP}/Contents/Resources/NetLogger.icns"

# Ad-hoc signature: keeps the firewall "Allow" decision sticky and avoids
# repeated Gatekeeper friction on the local machine.
codesign --force --deep --sign - "${APP}"

echo "Done: ${APP} (build ${BUILD})"
```

Also mark it executable in git:

```bash
git add scripts/build-mac.sh
git update-index --chmod=+x scripts/build-mac.sh
```

- [ ] **Step 3: README — add a macOS section.** In `README.md`: change the Requirements line to `**Requirements:** Windows 10/11 or macOS 11+ (beta), machines on the same L2 network (multicast reachable).`, and insert after the "Building from source" section:

```markdown
### macOS (beta)

The engine is identical; the mac build must be compiled **on a Mac** (Gio's
macOS backend uses cgo). With Go 1.26+ and Xcode Command Line Tools:

```bash
./scripts/build-mac.sh     # → bin/NetLogger.app (universal, ad-hoc signed)
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
- Tray mode is Windows-only for now; on macOS closing the window quits.
```

- [ ] **Step 4: Verify + commit**

```powershell
$env:CGO_ENABLED='0'; go build ./... ; go test ./...
```

```bash
git add cmd/netlogger-app/Info.plist scripts/build-mac.sh README.md
git commit -m "feat(macos): Info.plist, build-mac.sh, README macOS section"
```

---

### Task 11 (Phase B — ON A MAC): build, race-test, and mesh verification

No new code. Requires: a Mac (macOS 11+), Go 1.26+, Xcode CLT, same LAN as a Windows NetLogger 1.x node.

- [ ] **Step 1: Toolchain**

```bash
xcode-select --install || true   # ok if already installed
go version                        # expect go1.26+
git clone https://github.com/illtrick/netlogger && cd netlogger
```

- [ ] **Step 2: Full test suite + the race detector** (cgo is available here, so `-race` runs for the first time in this project's history — treat ANY race report as a release blocker and fix it before shipping):

```bash
go test ./...
go test -race ./internal/appcore/ ./internal/discovery/ ./internal/probe/ ./internal/store/ ./internal/iperf/ ./internal/singleton/
```

Expected: all `ok`. This also runs `singleton_unix_test.go` for real (see the Task 3 note about same-process flock semantics if it fails).

- [ ] **Step 3: Build**

```bash
./scripts/build-mac.sh
```

Expected: `Done: bin/NetLogger.app (build <hash>)`. Then `codesign -dv bin/NetLogger.app` shows `Signature=adhoc`, and `lipo -archs bin/NetLogger.app/Contents/MacOS/netlogger` prints `x86_64 arm64`.

- [ ] **Step 4: Launch + permissions**

```bash
open bin/NetLogger.app
```

Checklist:
- [ ] Local Network permission prompt appears → Allow
- [ ] Firewall "Allow incoming connections" prompt appears (if the macOS firewall is on) → Allow
- [ ] Native title bar with traffic lights; the app bar shows brand + Dashboard/Tests/Events tabs + status, and NO custom –/□/× buttons
- [ ] Window resize respects the 760×520 minimum; fullscreen (green button) works
- [ ] Footer/status shows the correct build hash
- [ ] Data dir is `~/Library/Application Support/NetLogger` (check the log line `NetLogger started; data dir …` in `netlogger.log` next to the binary or in the data dir)

- [ ] **Step 5: Mesh verification against a Windows node**

- [ ] The Windows node appears under peers within ~10 s; the Mac appears on the Windows node
- [ ] Heatmap rows fill for the Mac↔Windows link (ICMP RTT + UDP jitter/loss populate)
- [ ] Dashboard → Adapters lists the Mac's physical ports (e.g. "Ethernet en0 1 Gbps Up", "Wi-Fi en1") with counters
- [ ] Unplug/replug Ethernet (or toggle Wi-Fi): a link change event appears in Events on BOTH machines
- [ ] Events tab shows merged mesh-wide events with correct hosts/timestamps

- [ ] **Step 6: Tests suite**

- [ ] Without iperf3 installed: Speed sweep reports the clear "iperf3 not found" error, app stays healthy
- [ ] `brew install iperf3`, relaunch: Speed matrix Mac↔Windows fills both directions; Stop cancels mid-sweep
- [ ] Stress test with the Mac participating: banner + timer runs, heatmap keeps updating, Stop kills load on both ends
- [ ] Internet test run ON the Mac node (picked from the node dropdown on either machine): phases progress, grade renders
- [ ] `pmset -g assertions` while the app runs shows a `PreventUserIdleSystemSleep` assertion from `caffeinate` (if PreventSleep is enabled in settings)

- [ ] **Step 7: Lifecycle**

- [ ] Launching a second copy exits immediately ("another instance is already running" in the log)
- [ ] Closing the window quits the process (no tray on macOS); `ps aux | grep caffeinate` shows the child is gone
- [ ] Export button produces the JSON bundle in the data dir

- [ ] **Step 8: Commit any Phase-B fixes, push, and tag**

```bash
git push origin main
git tag -a v1.1.0 -m "NetLogger 1.1.0 - macOS support (beta)"
git push origin v1.1.0
```

---

## Known limitations shipped with this plan (documented, not bugs)

- No tray / background mode on macOS (close = quit). Future: NSStatusItem via purego.
- iperf3 not bundled on macOS (Homebrew or co-located binary next to the .app's `Contents/MacOS/` both work).
- `Power`/EEE fields empty on macOS — the OS does not expose them; EEE-dropout diagnosis stays a Windows-side feature.
- The .app is ad-hoc signed: other Macs will get Gatekeeper friction (right-click → Open). Real Developer ID signing/notarization is a separate release task.
- Linux is explicitly untouched: `!windows && !darwin` stubs keep today's behavior.
