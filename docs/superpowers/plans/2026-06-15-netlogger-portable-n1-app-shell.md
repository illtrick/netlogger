# NetLogger Portable — N1: App Shell + Lifecycle + Permissions — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stand up a portable, self-elevating native Gio desktop app (`NetLogger.exe`) that, on a single machine, opens a window, runs the in-process engine (open SQLite store, start bundled iperf3 server, self-probe at 1 Hz), shows live status, minimizes to the taskbar cleanly, and quits cleanly — proving the whole stack (Gio + admin manifest + in-process lifecycle + portable data dir + single-instance) before any multi-node UI is built.

**Architecture:** A new entry point `cmd/netlogger-app` wires four new packages — `datadir` (portable data-dir resolution), `applog` (file logging), `singleton` (named-mutex single-instance), and `appcore` (the in-process engine controller with a thread-safe `Snapshot`) — plus `ui` (the Gio window that renders the snapshot). The existing diagnostic engine packages (`store`, `iperf`, `probe`) are reused unchanged. The existing `cmd/netlogger` service build is left untouched (retired later in N6).

**Tech Stack:** Go 1.26 (cgo-free), Gio (`gioui.org`) for the native UI, `golang.org/x/sys/windows` for the single-instance mutex, `josephspurrier/goversioninfo` to embed a `requireAdministrator` Windows manifest, existing `modernc.org/sqlite` store, existing bundled iperf3.

Reference spec: `docs/superpowers/specs/2026-06-15-netlogger-portable-design.md` (§3.1 process model, §9 build/toolchain, §10 milestone N1).

---

## File Structure

| Path | Responsibility |
|---|---|
| `internal/datadir/datadir.go` | Resolve the portable data dir: `<exeDir>/NetLogger-data` if writable, else `%LOCALAPPDATA%/NetLogger`. Pure/injectable core for tests. |
| `internal/datadir/datadir_test.go` | Tests for the resolution logic. |
| `internal/applog/applog.go` | Open a log file in the data dir and route `log` output to it (windowsgui has no console). |
| `internal/applog/applog_test.go` | Test that init creates the file and captures output. |
| `internal/singleton/singleton_windows.go` | Named-mutex single-instance acquire (Windows). |
| `internal/singleton/singleton_other.go` | No-op single-instance for non-Windows builds. |
| `internal/singleton/singleton_windows_test.go` | Test second acquire fails while first is held. |
| `internal/appcore/appcore.go` | In-process controller: `New`/`Start`/`Snapshot`/`Stop`. Owns the store, iperf server, and self-probe loop. Injectable `Ping` and `StartIperf` seams for deterministic tests. |
| `internal/appcore/appcore_test.go` | Lifecycle test with injected ping + iperf stub. |
| `internal/ui/ui.go` | Gio window + minimal status layout reading `appcore.Snapshot`. Manually verified. |
| `cmd/netlogger-app/main.go` | Entry point: data dir → log → single-instance → `appcore.Start` → `ui.Run` → `appcore.Stop`. Holds the `//go:generate` directive. |
| `cmd/netlogger-app/app.exe.manifest` | `requireAdministrator` + DPI + supportedOS manifest. |
| `cmd/netlogger-app/versioninfo.json` | goversioninfo config referencing the manifest + icon. |
| `scripts/build-app.ps1` | Generate the `.syso` and build the elevated no-console exe. |
| `.gitignore` | Ignore the generated `cmd/netlogger-app/resource.syso`. |

---

## Task 1: Add dependencies and the manifest tool

**Files:**
- Modify: `go.mod`, `go.sum` (via `go get`)

- [ ] **Step 1: Add Gio, promote x/sys and uuid to direct deps**

Run:
```bash
cd "$HOME/.claude/netlogger"
go get gioui.org@latest
go get golang.org/x/sys@latest
go get github.com/google/uuid@latest
```
Expected: `go.mod` gains `gioui.org` and moves `golang.org/x/sys` / `github.com/google/uuid` to the direct `require` block.

- [ ] **Step 2: Install the goversioninfo CLI (build-time tool)**

Run:
```bash
GOBIN="$(go env GOPATH)/bin" go install github.com/josephspurrier/goversioninfo/cmd/goversioninfo@latest
"$(go env GOPATH)/bin/goversioninfo" -? 2>&1 | head -3 || true
```
Expected: the binary installs to `$(go env GOPATH)/bin/goversioninfo(.exe)`; the help/usage prints. (It is a build tool, not a module dependency.)

- [ ] **Step 3: Verify the module still builds**

Run:
```bash
go build ./...
```
Expected: builds cleanly (no Gio code is imported yet; this just confirms the new requires resolve).

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "build: add Gio, promote x/sys + uuid to direct deps (N1)"
```

---

## Task 2: `internal/datadir` — portable data-dir resolution

**Files:**
- Create: `internal/datadir/datadir.go`
- Test: `internal/datadir/datadir_test.go`

- [ ] **Step 1: Write the failing test**

```go
package datadir

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePrefersExeDirWhenWritable(t *testing.T) {
	exeDir := t.TempDir()
	fallback := t.TempDir()
	got, err := resolve(exeDir, fallback, func(string) bool { return true })
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := filepath.Join(exeDir, "NetLogger-data")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("expected dir created: %v", err)
	}
}

func TestResolveFallsBackWhenExeDirNotWritable(t *testing.T) {
	exeDir := t.TempDir()
	fallback := t.TempDir()
	// Writable everywhere except the exe-dir candidate.
	writable := func(p string) bool { return p != filepath.Join(exeDir, "NetLogger-data") }
	got, err := resolve(exeDir, fallback, writable)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := filepath.Join(fallback, "NetLogger")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/datadir/ -run TestResolve -v`
Expected: FAIL — `undefined: resolve`.

- [ ] **Step 3: Write the implementation**

```go
// Package datadir resolves the portable data directory for the app: next to the
// exe when that location is writable, otherwise under %LOCALAPPDATA%.
package datadir

import (
	"fmt"
	"os"
	"path/filepath"
)

// Resolve returns the data dir to use, creating it if needed.
func Resolve() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate executable: %w", err)
	}
	return resolve(filepath.Dir(exe), localAppData(), probeWritable)
}

// resolve is the injectable core: prefer <exeDir>/NetLogger-data when writable,
// else <fallbackBase>/NetLogger.
func resolve(exeDir, fallbackBase string, writable func(string) bool) (string, error) {
	cand := filepath.Join(exeDir, "NetLogger-data")
	if err := os.MkdirAll(cand, 0o755); err == nil && writable(cand) {
		return cand, nil
	}
	fb := filepath.Join(fallbackBase, "NetLogger")
	if err := os.MkdirAll(fb, 0o755); err != nil {
		return "", fmt.Errorf("create fallback data dir %q: %w", fb, err)
	}
	if !writable(fb) {
		return "", fmt.Errorf("no writable data dir (tried %q and %q)", cand, fb)
	}
	return fb, nil
}

// probeWritable tests writability by creating and removing a temp file (path
// inspection is unreliable: the admin manifest disables file virtualization, so
// a Program Files write hard-fails rather than redirecting).
func probeWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".wtest-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	_ = os.Remove(name)
	return true
}

// localAppData returns %LOCALAPPDATA%, falling back to the OS temp dir.
func localAppData() string {
	if v := os.Getenv("LOCALAPPDATA"); v != "" {
		return v
	}
	return os.TempDir()
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/datadir/ -v`
Expected: PASS (both tests).

- [ ] **Step 5: Commit**

```bash
git add internal/datadir/
git commit -m "feat(datadir): portable data-dir resolution with write-probe (N1)"
```

---

## Task 3: `internal/applog` — file logging

**Files:**
- Create: `internal/applog/applog.go`
- Test: `internal/applog/applog_test.go`

- [ ] **Step 1: Write the failing test**

```go
package applog

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitCreatesFileAndCapturesOutput(t *testing.T) {
	dir := t.TempDir()
	f, err := Init(dir)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() { f.Close(); log.SetOutput(os.Stderr) }()

	log.Print("hello-netlogger")

	data, err := os.ReadFile(filepath.Join(dir, "netlogger.log"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(data), "hello-netlogger") {
		t.Fatalf("log file missing message, got: %q", string(data))
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/applog/ -v`
Expected: FAIL — `undefined: Init`.

- [ ] **Step 3: Write the implementation**

```go
// Package applog routes the standard logger to a file in the data dir, since a
// -H windowsgui build has no console to print to.
package applog

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// Init opens (appends to) <dir>/netlogger.log and routes log output to it.
// The caller closes the returned file at shutdown.
func Init(dir string) (*os.File, error) {
	path := filepath.Join(dir, "netlogger.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	log.SetOutput(f)
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	return f, nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/applog/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/applog/
git commit -m "feat(applog): route logs to a file in the data dir (N1)"
```

---

## Task 4: `internal/singleton` — single-instance mutex

**Files:**
- Create: `internal/singleton/singleton_windows.go`
- Create: `internal/singleton/singleton_other.go`
- Test: `internal/singleton/singleton_windows_test.go`

- [ ] **Step 1: Write the failing test (Windows-only)**

```go
//go:build windows

package singleton

import "testing"

func TestAcquireSecondFailsWhileHeld(t *testing.T) {
	name := "NetLoggerTest.SingleInstance.A1"
	release, ok, err := Acquire(name)
	if err != nil {
		t.Fatalf("first acquire err: %v", err)
	}
	if !ok {
		t.Fatalf("first acquire should succeed")
	}
	defer release()

	_, ok2, err := Acquire(name)
	if err != nil {
		t.Fatalf("second acquire err: %v", err)
	}
	if ok2 {
		t.Fatalf("second acquire should fail while first is held")
	}
}

func TestAcquireSucceedsAfterRelease(t *testing.T) {
	name := "NetLoggerTest.SingleInstance.A2"
	release, ok, _ := Acquire(name)
	if !ok {
		t.Fatalf("first acquire should succeed")
	}
	release()
	release2, ok2, err := Acquire(name)
	if err != nil {
		t.Fatalf("acquire after release err: %v", err)
	}
	if !ok2 {
		t.Fatalf("acquire after release should succeed")
	}
	release2()
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/singleton/ -v`
Expected: FAIL — `undefined: Acquire`.

- [ ] **Step 3: Write the Windows implementation**

```go
//go:build windows

// Package singleton enforces one running instance per machine via a named mutex.
package singleton

import "golang.org/x/sys/windows"

// Acquire creates a named mutex. If another instance already holds it, ok is
// false. The caller must call release at shutdown when ok is true.
func Acquire(name string) (release func(), ok bool, err error) {
	p, err := windows.UTF16PtrFromString(`Local\` + name)
	if err != nil {
		return nil, false, err
	}
	h, err := windows.CreateMutex(nil, false, p)
	if h == 0 {
		return nil, false, err
	}
	if err == windows.ERROR_ALREADY_EXISTS {
		windows.CloseHandle(h)
		return nil, false, nil
	}
	return func() { windows.CloseHandle(h) }, true, nil
}
```

- [ ] **Step 4: Write the non-Windows no-op**

```go
//go:build !windows

package singleton

// Acquire is a no-op on non-Windows builds (always succeeds).
func Acquire(name string) (release func(), ok bool, err error) {
	return func() {}, true, nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/singleton/ -v`
Expected: PASS (both Windows tests).

- [ ] **Step 6: Commit**

```bash
git add internal/singleton/
git commit -m "feat(singleton): named-mutex single-instance guard (N1)"
```

---

## Task 5: `internal/appcore` — in-process engine controller

**Files:**
- Create: `internal/appcore/appcore.go`
- Test: `internal/appcore/appcore_test.go`

- [ ] **Step 1: Write the failing test**

```go
package appcore

import (
	"testing"
	"time"

	"netlogger/internal/probe"
)

func TestLifecycleProducesSamplesAndStopsClean(t *testing.T) {
	dir := t.TempDir()
	a := New(dir)
	// Deterministic seams: no real ICMP, no real iperf process.
	a.Ping = func(string, time.Duration) (probe.Result, error) {
		return probe.Result{RTT: 1500 * time.Microsecond}, nil
	}
	a.StartIperf = func(string) (func(), string) {
		return func() {}, "iperf 3.21 (test)"
	}
	a.tick = 5 * time.Millisecond // fast loop for the test

	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait until at least a few samples have been recorded.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if a.Snapshot().Samples >= 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no samples produced; got %d", a.Snapshot().Samples)
		}
		time.Sleep(5 * time.Millisecond)
	}

	snap := a.Snapshot()
	if snap.Iperf3Version != "iperf 3.21 (test)" {
		t.Fatalf("iperf version = %q", snap.Iperf3Version)
	}
	if !snap.Iperf3ServerUp {
		t.Fatalf("expected iperf server up")
	}
	if snap.LastRTTms <= 0 {
		t.Fatalf("expected positive LastRTTms, got %v", snap.LastRTTms)
	}
	if snap.DBPath == "" || snap.DataDir != dir {
		t.Fatalf("unexpected paths: %+v", snap)
	}

	if err := a.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestLossReflectedInSnapshot(t *testing.T) {
	dir := t.TempDir()
	a := New(dir)
	a.Ping = func(string, time.Duration) (probe.Result, error) {
		return probe.Result{Lost: true}, nil
	}
	a.StartIperf = func(string) (func(), string) { return func() {}, "" }
	a.tick = 5 * time.Millisecond
	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()
	deadline := time.Now().Add(2 * time.Second)
	for a.Snapshot().Samples < 5 {
		if time.Now().After(deadline) {
			t.Fatalf("no samples")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := a.Snapshot().LossPct; got < 99 {
		t.Fatalf("expected ~100%% loss, got %v", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/appcore/ -v`
Expected: FAIL — `undefined: New`.

- [ ] **Step 3: Write the implementation**

```go
// Package appcore is the in-process engine controller for the portable app: it
// opens the store, starts the bundled iperf3 server, runs a self-probe loop, and
// exposes a thread-safe Snapshot the UI renders. No service, no HTTP server.
package appcore

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"netlogger/internal/iperf"
	"netlogger/internal/probe"
	"netlogger/internal/store"
)

// Snapshot is an immutable view of engine state for the UI.
type Snapshot struct {
	DataDir        string
	DBPath         string
	Iperf3Version  string
	Iperf3ServerUp bool
	StartedUnix    int64
	Samples        int
	LastRTTms      float64
	LossPct        float64
}

// App is the single-machine engine controller.
type App struct {
	dataDir string
	dbPath  string

	// Seams (defaulted in New; overridden in tests).
	Ping       func(addr string, timeout time.Duration) (probe.Result, error)
	StartIperf func(dataDir string) (stop func(), version string)
	tick       time.Duration

	store      *store.Store
	iperfStop  func()
	iperfVer   string
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	startedAt  time.Time

	mu        sync.Mutex
	samples   int
	lastRTTms float64
	recent    []bool // success flags, last N, for loss %
}

const recentWindow = 60

// New creates an App for the given (already-resolved) data dir.
func New(dataDir string) *App {
	return &App{
		dataDir:    dataDir,
		dbPath:     filepath.Join(dataDir, "netlogger.db"),
		Ping:       probe.PingICMP,
		StartIperf: defaultStartIperf,
		tick:       time.Second,
	}
}

func defaultStartIperf(dir string) (func(), string) {
	_ = iperf.Bootstrap(dir)
	ver := iperf.Version()
	srv := iperf.StartServer(0)
	return func() { srv.Stop() }, ver
}

// Start opens the store, starts iperf, and launches the probe loop.
func (a *App) Start() error {
	st, err := store.Open(a.dbPath)
	if err != nil {
		return err
	}
	a.store = st
	a.iperfStop, a.iperfVer = a.StartIperf(a.dataDir)
	a.startedAt = time.Now()

	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.wg.Add(1)
	go a.probeLoop(ctx)
	return nil
}

func (a *App) probeLoop(ctx context.Context) {
	defer a.wg.Done()
	t := time.NewTicker(a.tick)
	defer t.Stop()
	var seq int64
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			res, err := a.Ping("127.0.0.1", 2*time.Second)
			lost := err != nil || res.Lost
			seq++
			sm := store.Sample{
				Seq:       seq,
				TSUnixUS:  time.Now().UnixMicro(),
				ProbeType: "icmp",
				SrcHost:   "self",
				DstHost:   "self",
				Direction: "rtt",
				RTTus:     res.RTT.Microseconds(),
				Lost:      lost,
			}
			_, _ = a.store.Insert(sm)

			a.mu.Lock()
			a.samples++
			if !lost {
				a.lastRTTms = float64(res.RTT.Microseconds()) / 1000.0
			}
			a.recent = append(a.recent, !lost)
			if len(a.recent) > recentWindow {
				a.recent = a.recent[len(a.recent)-recentWindow:]
			}
			a.mu.Unlock()
		}
	}
}

// Snapshot returns an immutable copy of current engine state.
func (a *App) Snapshot() Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	loss := 0.0
	if n := len(a.recent); n > 0 {
		lost := 0
		for _, ok := range a.recent {
			if !ok {
				lost++
			}
		}
		loss = float64(lost) / float64(n) * 100.0
	}
	return Snapshot{
		DataDir:        a.dataDir,
		DBPath:         a.dbPath,
		Iperf3Version:  a.iperfVer,
		Iperf3ServerUp: a.iperfStop != nil,
		StartedUnix:    a.startedAt.Unix(),
		Samples:        a.samples,
		LastRTTms:      a.lastRTTms,
		LossPct:        loss,
	}
}

// Stop cancels the loop, stops iperf, and closes the store.
func (a *App) Stop() error {
	if a.cancel != nil {
		a.cancel()
	}
	a.wg.Wait()
	if a.iperfStop != nil {
		a.iperfStop()
	}
	if a.store != nil {
		return a.store.Close()
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/appcore/ -v`
Expected: PASS (both tests).

- [ ] **Step 5: Run vet + gofmt**

Run: `go vet ./internal/appcore/ && gofmt -l internal/appcore/`
Expected: no vet errors; `gofmt -l` prints nothing.

- [ ] **Step 6: Commit**

```bash
git add internal/appcore/
git commit -m "feat(appcore): in-process engine controller with snapshot (N1)"
```

---

## Task 6: `internal/ui` — minimal Gio window

> Gio UI needs a real window/event loop and is verified manually (no unit test). Keep this file thin: it only reads `appcore.Snapshot` and draws text.

**Files:**
- Create: `internal/ui/ui.go`

- [ ] **Step 1: Write the UI**

```go
// Package ui renders the portable app's native window with Gio. For N1 it shows
// a minimal live-status panel read from appcore.Snapshot.
package ui

import (
	"fmt"
	"time"

	"gioui.org/app"
	"gioui.org/font/gofont"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"netlogger/internal/appcore"
)

// Run opens the window and renders until it is closed. It returns when the user
// closes the window. onClose is called once the window is destroyed (used to
// stop the engine).
func Run(a *appcore.App) error {
	w := new(app.Window)
	w.Option(app.Title("NetLogger"), app.Size(unit.Dp(820), unit.Dp(560)))

	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))

	// Repaint ~1 Hz so the live numbers update (N1 simplicity; N2 coalesces).
	go func() {
		for {
			time.Sleep(time.Second)
			w.Invalidate()
		}
	}()

	var ops op.Ops
	for {
		switch e := w.Event().(type) {
		case app.DestroyEvent:
			return e.Err
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			layoutStatus(gtx, th, a.Snapshot())
			e.Frame(gtx.Ops)
		}
	}
}

func layoutStatus(gtx layout.Context, th *material.Theme, s appcore.Snapshot) layout.Dimensions {
	rows := []string{
		"NetLogger — portable diagnostic agent",
		"",
		fmt.Sprintf("Data dir:      %s", s.DataDir),
		fmt.Sprintf("Database:      %s", s.DBPath),
		fmt.Sprintf("iperf3:        %s (server %s)", versionOr(s.Iperf3Version), upDown(s.Iperf3ServerUp)),
		fmt.Sprintf("Self-probe:    %d samples, last RTT %.2f ms, loss %.1f%%", s.Samples, s.LastRTTms, s.LossPct),
	}
	return layout.UniformInset(unit.Dp(20)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, flexChildren(th, rows)...)
	})
}

func flexChildren(th *material.Theme, rows []string) []layout.FlexChild {
	out := make([]layout.FlexChild, 0, len(rows))
	for _, r := range rows {
		r := r
		out = append(out, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(3), Bottom: unit.Dp(3)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return material.Body1(th, r).Layout(gtx)
			})
		}))
	}
	return out
}

func versionOr(v string) string {
	if v == "" {
		return "(not available)"
	}
	return v
}

func upDown(up bool) string {
	if up {
		return "running"
	}
	return "stopped"
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/ui/`
Expected: builds cleanly. (If the Gio API for `material.NewTheme`/`text.NewShaper`/`app.Window` differs in the resolved Gio version, adjust to the version's documented event-loop API — the shape is `new(app.Window)` → `w.Event()` → `app.FrameEvent`/`app.DestroyEvent` → `e.Frame(gtx.Ops)`.)

- [ ] **Step 3: Commit**

```bash
git add internal/ui/
git commit -m "feat(ui): minimal Gio status window (N1)"
```

---

## Task 7: Entry point, manifest, and elevated build

**Files:**
- Create: `cmd/netlogger-app/main.go`
- Create: `cmd/netlogger-app/app.exe.manifest`
- Create: `cmd/netlogger-app/versioninfo.json`
- Create: `scripts/build-app.ps1`
- Modify: `.gitignore`

- [ ] **Step 1: Write the manifest**

`cmd/netlogger-app/app.exe.manifest`:
```xml
<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
  <assemblyIdentity type="win32" name="NetLogger.Portable.App" version="1.0.0.0" processorArchitecture="*"/>
  <trustInfo xmlns="urn:schemas-microsoft-com:asm.v2">
    <security>
      <requestedPrivileges xmlns="urn:schemas-microsoft-com:asm.v3">
        <requestedExecutionLevel level="requireAdministrator" uiAccess="false"/>
      </requestedPrivileges>
    </security>
  </trustInfo>
  <compatibility xmlns="urn:schemas-microsoft-com:compatibility.v1">
    <application>
      <supportedOS Id="{8e0f7a12-bfb3-4fe8-b9a5-48fd50a15a9a}"/>
    </application>
  </compatibility>
  <asmv3:application xmlns:asmv3="urn:schemas-microsoft-com:asm.v3">
    <asmv3:windowsSettings>
      <dpiAwareness xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">PerMonitorV2, system</dpiAwareness>
      <longPathAware xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">true</longPathAware>
    </asmv3:windowsSettings>
  </asmv3:application>
</assembly>
```

- [ ] **Step 2: Write the goversioninfo config**

`cmd/netlogger-app/versioninfo.json`:
```json
{
  "FixedFileInfo": { "FileVersion": {"Major":1,"Minor":0,"Patch":0,"Build":0} },
  "StringFileInfo": {
    "ProductName": "NetLogger",
    "FileDescription": "NetLogger portable network diagnostic",
    "CompanyName": "NetLogger"
  },
  "ManifestPath": "app.exe.manifest"
}
```

- [ ] **Step 3: Write the entry point**

`cmd/netlogger-app/main.go`:
```go
//go:generate goversioninfo -64 -o resource.syso

// Command netlogger-app is the portable, self-elevating native NetLogger app.
package main

import (
	"log"
	"os"

	"netlogger/internal/appcore"
	"netlogger/internal/applog"
	"netlogger/internal/datadir"
	"netlogger/internal/singleton"
	"netlogger/internal/ui"
)

func main() {
	dir, err := datadir.Resolve()
	if err != nil {
		// No window/console yet; nothing else we can do.
		os.Exit(1)
	}
	logFile, err := applog.Init(dir)
	if err == nil {
		defer logFile.Close()
	}

	release, ok, err := singleton.Acquire("NetLogger.Portable.SingleInstance")
	if err != nil {
		log.Printf("single-instance check failed: %v (continuing)", err)
	}
	if !ok {
		log.Printf("another instance is already running; exiting")
		return
	}
	defer release()

	a := appcore.New(dir)
	if err := a.Start(); err != nil {
		log.Printf("engine start failed: %v", err)
		return
	}
	defer func() {
		if err := a.Stop(); err != nil {
			log.Printf("engine stop error: %v", err)
		}
	}()

	log.Printf("NetLogger started; data dir %s", dir)
	if err := ui.Run(a); err != nil {
		log.Printf("ui exited with error: %v", err)
	}
	log.Printf("NetLogger shutting down")
}
```

- [ ] **Step 4: Ignore the generated resource**

Append to `.gitignore`:
```
# generated Windows resource (manifest/version) — produced by go generate
cmd/netlogger-app/resource.syso
```

- [ ] **Step 5: Verify a plain (unelevated, dev) build compiles**

Run:
```bash
go build -o bin/NetLogger-dev.exe ./cmd/netlogger-app
```
Expected: builds cleanly. (No `.syso` yet → not elevated; this only confirms the wiring compiles.)

- [ ] **Step 6: Write the elevated build script**

`scripts/build-app.ps1`:
```powershell
# Build the portable, self-elevating NetLogger app (no console).
$ErrorActionPreference = "Stop"
$env:Path += ";C:\Program Files\Go\bin;$(go env GOPATH)\bin"
New-Item -ItemType Directory -Force -Path bin | Out-Null

Write-Host "Generating Windows manifest resource..."
go generate ./cmd/netlogger-app

Write-Host "Building NetLogger.exe (windowsgui, elevated)..."
$env:CGO_ENABLED = "0"
go build -ldflags "-H windowsgui -s -w" -o bin/NetLogger.exe ./cmd/netlogger-app

Write-Host "Done: bin/NetLogger.exe"
```

- [ ] **Step 7: Build the elevated exe**

Run (PowerShell): `./scripts/build-app.ps1`
Expected: `cmd/netlogger-app/resource.syso` is generated; `bin/NetLogger.exe` is produced with no errors.

- [ ] **Step 8: Manual verification (the N1 acceptance gate)**

Do, and confirm each:
1. Double-click `bin/NetLogger.exe` → a **UAC prompt** appears (proves the manifest elevation). Approve it.
2. A **native window** opens titled "NetLogger" showing the data dir, database path, iperf3 version + "server running", and a self-probe line whose **sample count increases ~1/sec** and shows a small RTT with ~0% loss.
3. Confirm `NetLogger-data\netlogger.db` and `netlogger.log` exist next to the exe (or under `%LOCALAPPDATA%\NetLogger` if the exe dir is read-only).
4. Confirm an `iperf3` process is running and listening on 5201 (`Get-NetTCPConnection -State Listen -LocalPort 5201`).
5. **Minimize** the window → it goes to the taskbar; wait ~30 s; restore → the sample count has continued climbing (engine ran while minimized).
6. Launch a **second** copy → it exits immediately without a second window (single-instance).
7. **Close** the window → confirm via Task Manager that `NetLogger.exe` and its `iperf3.exe` child both exit, and nothing is left listening on 5201 (clean shutdown, no residue).

- [ ] **Step 9: Commit**

```bash
git add cmd/netlogger-app/ scripts/build-app.ps1 .gitignore
git commit -m "feat(app): portable self-elevating Gio entry point + build (N1)"
```

---

## Self-Review

**Spec coverage (N1 scope from §10 + §3.1/§9):**
- Gio window → Task 6, Task 7 (manual verify). ✓
- Embedded admin manifest → Task 7 (manifest + goversioninfo + UAC verify). ✓
- In-process engine (store + iperf server + self-probe) → Task 5. ✓
- Portable data dir (write-probe, LOCALAPPDATA fallback) → Task 2. ✓
- Minimize-to-taskbar (engine keeps running) → Task 7 step 8.5 (manual). ✓
- Clean quit (no residue) → Task 5 `Stop` + Task 7 step 8.7. ✓
- Single-instance → Task 4 + Task 7 step 8.6. ✓
- Logs to file (windowsgui) → Task 3. ✓
- cgo-free build → Task 7 (`CGO_ENABLED=0`, `-H windowsgui`). ✓

Out of N1 scope (correctly deferred): discovery/peers (N3), full UI screens + link matrix (N2), synchronized load round (N4), export (N5), retiring web/agentsvc/kardianos (N6). The `mesh`/HTTP sync API is intentionally absent in N1 (single machine).

**Placeholder scan:** No "TBD/TODO/handle errors appropriately". Every code step shows complete code; every command shows expected output. The one adaptivity note (Task 6 step 2) is bounded to a documented API shape, not a hand-wave.

**Type consistency:** `appcore.Snapshot` fields are referenced identically in `appcore_test.go`, `appcore.go`, and `ui.go` (`DataDir`, `DBPath`, `Iperf3Version`, `Iperf3ServerUp`, `Samples`, `LastRTTms`, `LossPct`). Seams `Ping`/`StartIperf`/`tick` match between test and implementation. `store.Sample` fields (`Seq`,`TSUnixUS`,`ProbeType`,`SrcHost`,`DstHost`,`Direction`,`RTTus`,`Lost`) match the real type. `probe.Result{RTT,Lost}` and `probe.PingICMP(addr,timeout)` match the real signatures. `singleton.Acquire(name) (func(), bool, error)` matches between Windows impl, no-op, test, and `main.go`.
