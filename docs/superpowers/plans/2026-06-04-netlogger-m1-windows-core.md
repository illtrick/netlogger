# NetLogger M1 — Windows Core Vertical Slice — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A single Go binary that, on Windows, loads a network config file, runs ICMP + isochronous-UDP probes, persists samples to a local SQLite (WAL) store, runs as a self-installing Windows Service, and serves a minimal status page — the vertical slice that proves the probe→store→service→GUI spine on the priority platform.

**Architecture:** One Go module (`netlogger`). `internal/` packages each own one responsibility (config, clock, store, probe, web, service); `cmd/netlogger` is the CLI entrypoint that wires them. Probes write `Sample` rows to a local WAL SQLite DB. The agent runs under `kardianos/service` (Windows SCM) and serves an embedded SPA showing this host's status.

**Tech Stack:** Go 1.22+, `modernc.org/sqlite` (pure-Go SQLite, no cgo → trivial cross-compile), `github.com/prometheus-community/pro-bing` (ICMP via `IcmpSendEcho`, no admin), `github.com/kardianos/service` (Windows Service), `gopkg.in/yaml.v3` (config). Tests use Go's `testing` package against loopback.

**Spec reference:** `docs/superpowers/specs/2026-06-04-netlogger-design.md` — M1 in §12, store §8, probes §5, config file §2a.

---

### Task 0: Toolchain + project scaffold

**Files:**
- Create: `go.mod`, `.gitignore`, `internal/version/version.go`, `cmd/netlogger/main.go`

- [ ] **Step 1: Install Go (if missing) and verify**

Run (PowerShell):
```powershell
if (-not (Get-Command go -ErrorAction SilentlyContinue)) { winget install --id GoLang.Go --silent --accept-source-agreements --accept-package-agreements }
```
Then open a NEW shell and run: `go version`
Expected: `go version go1.22` or newer. (If `go` still isn't found, add `C:\Program Files\Go\bin` to PATH and reopen the shell.)

- [ ] **Step 2: Initialize git and Go module**

Run from `C:\Users\natha\.claude\netlogger`:
```powershell
git init
go mod init netlogger
```
Expected: creates `.git/` and `go.mod` with `module netlogger`.

- [ ] **Step 3: Add `.gitignore`**

Create `.gitignore`:
```gitignore
# build output
/bin/
*.exe
# local data / databases
*.db
*.db-wal
*.db-shm
data/
# go
/vendor/
```

- [ ] **Step 4: Add the version package**

Create `internal/version/version.go`:
```go
// Package version holds the build version string for NetLogger.
package version

// Version is the NetLogger release identifier.
const Version = "0.1.0-m1"
```

- [ ] **Step 5: Add a minimal main that prints the version**

Create `cmd/netlogger/main.go`:
```go
package main

import (
	"fmt"
	"os"

	"netlogger/internal/version"
)

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "version" {
		fmt.Println("netlogger", version.Version)
		return
	}
	fmt.Println("netlogger", version.Version, "- use a subcommand (version)")
}
```

- [ ] **Step 6: Build and run**

Run:
```powershell
go build -o bin/netlogger.exe ./cmd/netlogger
./bin/netlogger.exe version
```
Expected: `netlogger 0.1.0-m1`

- [ ] **Step 7: Commit**

```powershell
git add -A
git commit -m "chore: scaffold netlogger Go module, version, CLI skeleton"
```

---

### Task 1: Network config loader

**Files:**
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`
- Create (fixture): `internal/config/testdata/network.yaml`

- [ ] **Step 1: Write the failing test**

Create `internal/config/testdata/network.yaml`:
```yaml
nodes:
  - id: router
    type: router
    label: "Router"
    model: "TP-Link BE9300"
  - id: switch1
    type: switch
    label: "Switch 1"
    model: "Tenda TEM2010F"
    managed: false
  - id: ryzen
    type: endpoint
    label: "Ryzen"
    nic: "Killer E3100G"
    link_speed: "2.5G"
    role: coordinator
links:
  - [router, switch1]
  - [switch1, ryzen]
```

Create `internal/config/config_test.go`:
```go
package config

import "testing"

func TestLoadValid(t *testing.T) {
	c, err := Load("testdata/network.yaml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.Nodes) != 3 {
		t.Fatalf("want 3 nodes, got %d", len(c.Nodes))
	}
	if len(c.Links) != 2 {
		t.Fatalf("want 2 links, got %d", len(c.Links))
	}
	n, ok := c.Node("ryzen")
	if !ok {
		t.Fatal("ryzen node not found")
	}
	if n.Role != "coordinator" || n.LinkSpeed != "2.5G" {
		t.Fatalf("ryzen fields wrong: %+v", n)
	}
}

func TestValidateRejectsUnknownLinkNode(t *testing.T) {
	c := &Config{
		Nodes: []Node{{ID: "a", Type: NodeEndpoint}},
		Links: [][]string{{"a", "ghost"}},
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for link to unknown node, got nil")
	}
}

func TestValidateRejectsBadLinkArity(t *testing.T) {
	c := &Config{
		Nodes: []Node{{ID: "a", Type: NodeEndpoint}},
		Links: [][]string{{"a"}},
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for 1-element link, got nil")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/`
Expected: FAIL — `undefined: Load`, `undefined: Config`, etc.

- [ ] **Step 3: Add the yaml dependency**

Run: `go get gopkg.in/yaml.v3`
Expected: adds `gopkg.in/yaml.v3` to `go.mod`.

- [ ] **Step 4: Write the implementation**

Create `internal/config/config.go`:
```go
// Package config loads the user-supplied network topology + inventory file.
// model/nic are labels only — the tool applies no behavior keyed off them.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// NodeType is the role a node plays in the topology.
type NodeType string

const (
	NodeModem    NodeType = "modem"
	NodeRouter   NodeType = "router"
	NodeSwitch   NodeType = "switch"
	NodePassive  NodeType = "passive"
	NodeEndpoint NodeType = "endpoint"
	NodeCloud    NodeType = "cloud"
)

// Node is one element of the network (a device, switch, or passive segment).
type Node struct {
	ID        string   `yaml:"id"`
	Type      NodeType `yaml:"type"`
	Label     string   `yaml:"label"`
	Model     string   `yaml:"model,omitempty"`
	NIC       string   `yaml:"nic,omitempty"`
	Address   string   `yaml:"address,omitempty"`
	LinkSpeed string   `yaml:"link_speed,omitempty"`
	Role      string   `yaml:"role,omitempty"`
	ClockRes  string   `yaml:"clock_res,omitempty"`
	Managed   bool     `yaml:"managed,omitempty"`
}

// Config is the whole network config file: nodes plus their links.
type Config struct {
	Nodes []Node     `yaml:"nodes"`
	Links [][]string `yaml:"links"`
}

// Load reads and validates a network config file from path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Validate checks IDs are unique and every link references known nodes.
func (c *Config) Validate() error {
	ids := make(map[string]bool, len(c.Nodes))
	for _, n := range c.Nodes {
		if n.ID == "" {
			return fmt.Errorf("node with empty id")
		}
		if ids[n.ID] {
			return fmt.Errorf("duplicate node id %q", n.ID)
		}
		ids[n.ID] = true
	}
	for _, l := range c.Links {
		if len(l) != 2 {
			return fmt.Errorf("link must have exactly 2 endpoints, got %v", l)
		}
		for _, id := range l {
			if !ids[id] {
				return fmt.Errorf("link references unknown node %q", id)
			}
		}
	}
	return nil
}

// Node returns the node with the given id, if present.
func (c *Config) Node(id string) (Node, bool) {
	for _, n := range c.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return Node{}, false
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/config/`
Expected: PASS (ok netlogger/internal/config)

- [ ] **Step 6: Commit**

```powershell
git add -A
git commit -m "feat: network config file loader with validation"
```

---

### Task 2: Monotonic-derived UTC clock

**Files:**
- Create: `internal/clock/clock.go`
- Test: `internal/clock/clock_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/clock/clock_test.go`:
```go
package clock

import "testing"

func TestSystemClockIsPositiveMicros(t *testing.T) {
	var c Clock = System{}
	got := c.NowUnixMicro()
	// Sanity: after 2020-01-01 in microseconds.
	if got < 1_577_836_800_000_000 {
		t.Fatalf("NowUnixMicro too small: %d", got)
	}
}

func TestFixedClockReturnsSetValue(t *testing.T) {
	var c Clock = Fixed{Micros: 42}
	if got := c.NowUnixMicro(); got != 42 {
		t.Fatalf("want 42, got %d", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/clock/`
Expected: FAIL — `undefined: Clock`

- [ ] **Step 3: Write the implementation**

Create `internal/clock/clock.go`:
```go
// Package clock provides UTC microsecond timestamps and a fake for tests.
package clock

import "time"

// Clock yields the current time as Unix epoch microseconds (UTC).
type Clock interface {
	NowUnixMicro() int64
}

// System is the real clock. time.Now carries a monotonic reading internally,
// so intervals computed from successive values are immune to wall-clock steps.
type System struct{}

// NowUnixMicro returns the current UTC time in epoch microseconds.
func (System) NowUnixMicro() int64 { return time.Now().UTC().UnixMicro() }

// Fixed is a deterministic clock for tests.
type Fixed struct{ Micros int64 }

// NowUnixMicro returns the fixed value.
func (f Fixed) NowUnixMicro() int64 { return f.Micros }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/clock/`
Expected: PASS

- [ ] **Step 5: Commit**

```powershell
git add -A
git commit -m "feat: UTC microsecond clock with test fake"
```

---

### Task 3: SQLite store (WAL) with schema + insert/since

**Files:**
- Create: `internal/store/store.go`
- Test: `internal/store/store_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/store/store_test.go`:
```go
package store

import (
	"path/filepath"
	"testing"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenEnablesWAL(t *testing.T) {
	s := openTemp(t)
	var mode string
	if err := s.DB().QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Fatalf("want wal, got %q", mode)
	}
}

func TestInsertAndSince(t *testing.T) {
	s := openTemp(t)
	good := Sample{TSUnixUS: 1000, ProbeType: "icmp", SrcHost: "a", DstHost: "b", Direction: "rtt", RTTus: 1500, Lost: false}
	lost := Sample{TSUnixUS: 2000, ProbeType: "icmp", SrcHost: "a", DstHost: "c", Direction: "rtt", Lost: true}
	seq1, err := s.Insert(good)
	if err != nil {
		t.Fatalf("insert good: %v", err)
	}
	if _, err := s.Insert(lost); err != nil {
		t.Fatalf("insert lost: %v", err)
	}

	rows, err := s.Since(0, 100)
	if err != nil {
		t.Fatalf("since: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
	if rows[0].RTTus != 1500 || rows[0].Lost {
		t.Fatalf("good row wrong: %+v", rows[0])
	}
	if !rows[1].Lost || rows[1].RTTus != 0 {
		t.Fatalf("lost row should have Lost=true, RTTus=0 (NULL): %+v", rows[1])
	}

	// Since(seq1) must exclude the first row.
	rows2, err := s.Since(seq1, 100)
	if err != nil {
		t.Fatalf("since seq1: %v", err)
	}
	if len(rows2) != 1 || rows2[0].DstHost != "c" {
		t.Fatalf("since(seq1) want only row c, got %+v", rows2)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/`
Expected: FAIL — `undefined: Store`

- [ ] **Step 3: Add the sqlite driver**

Run: `go get modernc.org/sqlite`
Expected: adds `modernc.org/sqlite` to `go.mod`.

- [ ] **Step 4: Write the implementation**

Create `internal/store/store.go`:
```go
// Package store is the local, append-mostly SQLite (WAL) sample store.
// It is the agent's source of truth; the coordinator later syncs from it.
package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Sample is one probe measurement. A lost probe has Lost=true and RTTus=0
// (persisted as NULL — never a sentinel, per spec §8).
type Sample struct {
	Seq       int64
	TSUnixUS  int64
	ProbeType string // "icmp" | "udp_iso" | "tcp_connect"
	SrcHost   string
	DstHost   string
	Direction string // "up" | "down" | "rtt"
	RTTus     int64  // microseconds; 0 when Lost
	JitterUS  int64
	Lost      bool
}

// Store wraps the SQLite database.
type Store struct{ db *sql.DB }

// DB exposes the underlying handle (used by tests and the sync layer).
func (s *Store) DB() *sql.DB { return s.db }

const schema = `
CREATE TABLE IF NOT EXISTS probe_samples (
  seq        INTEGER PRIMARY KEY AUTOINCREMENT,
  ts_unix_us INTEGER NOT NULL,
  probe_type TEXT NOT NULL,
  src_host   TEXT NOT NULL,
  dst_host   TEXT NOT NULL,
  direction  TEXT,
  rtt_us     INTEGER,
  jitter_us  INTEGER,
  lost       INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_probe_ts ON probe_samples(ts_unix_us);
CREATE INDEX IF NOT EXISTS idx_probe_target_ts ON probe_samples(dst_host, ts_unix_us);
`

var pragmas = []string{
	"PRAGMA journal_mode=WAL",
	"PRAGMA synchronous=NORMAL",
	"PRAGMA busy_timeout=5000",
	"PRAGMA journal_size_limit=67108864",
	"PRAGMA temp_store=MEMORY",
	"PRAGMA auto_vacuum=INCREMENTAL",
}

// Open opens (creating if needed) the database at path with the spec PRAGMAs
// and schema applied.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("pragma %q: %w", p, err)
		}
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Insert writes one sample and returns its assigned seq.
func (s *Store) Insert(sm Sample) (int64, error) {
	var rtt, jitter any
	if sm.Lost {
		rtt = nil // NULL for loss
	} else {
		rtt = sm.RTTus
	}
	if sm.JitterUS != 0 {
		jitter = sm.JitterUS
	}
	lost := 0
	if sm.Lost {
		lost = 1
	}
	res, err := s.db.Exec(
		`INSERT INTO probe_samples (ts_unix_us,probe_type,src_host,dst_host,direction,rtt_us,jitter_us,lost)
		 VALUES (?,?,?,?,?,?,?,?)`,
		sm.TSUnixUS, sm.ProbeType, sm.SrcHost, sm.DstHost, sm.Direction, rtt, jitter, lost)
	if err != nil {
		return 0, fmt.Errorf("insert sample: %w", err)
	}
	return res.LastInsertId()
}

// Since returns up to limit samples with seq greater than afterSeq, in order.
func (s *Store) Since(afterSeq int64, limit int) ([]Sample, error) {
	rows, err := s.db.Query(
		`SELECT seq,ts_unix_us,probe_type,src_host,dst_host,
		        COALESCE(direction,''),COALESCE(rtt_us,0),COALESCE(jitter_us,0),lost
		 FROM probe_samples WHERE seq > ? ORDER BY seq LIMIT ?`,
		afterSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("query since: %w", err)
	}
	defer rows.Close()
	var out []Sample
	for rows.Next() {
		var sm Sample
		var lostInt int
		if err := rows.Scan(&sm.Seq, &sm.TSUnixUS, &sm.ProbeType, &sm.SrcHost,
			&sm.DstHost, &sm.Direction, &sm.RTTus, &sm.JitterUS, &lostInt); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		sm.Lost = lostInt == 1
		out = append(out, sm)
	}
	return out, rows.Err()
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/store/`
Expected: PASS

- [ ] **Step 6: Commit**

```powershell
git add -A
git commit -m "feat: WAL SQLite store with insert and cursor-based Since"
```

---

### Task 4: ICMP probe (pro-bing)

**Files:**
- Create: `internal/probe/icmp.go`
- Test: `internal/probe/icmp_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/probe/icmp_test.go`:
```go
package probe

import (
	"testing"
	"time"
)

// Loopback should reply immediately; this verifies the happy path end-to-end.
func TestPingICMPLoopback(t *testing.T) {
	res, err := PingICMP("127.0.0.1", 2*time.Second)
	if err != nil {
		t.Skipf("ICMP not permitted in this environment: %v", err)
	}
	if res.Lost {
		t.Fatalf("loopback ping reported lost")
	}
	if res.RTT < 0 {
		t.Fatalf("negative RTT: %v", res.RTT)
	}
}

// An unroutable TEST-NET-1 address should time out -> Lost, no error.
func TestPingICMPTimeoutIsLost(t *testing.T) {
	res, err := PingICMP("192.0.2.1", 500*time.Millisecond)
	if err != nil {
		t.Skipf("ICMP not permitted: %v", err)
	}
	if !res.Lost {
		t.Fatalf("want Lost=true for unreachable host, got %+v", res)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/probe/`
Expected: FAIL — `undefined: PingICMP`

- [ ] **Step 3: Add the pro-bing dependency**

Run: `go get github.com/prometheus-community/pro-bing`
Expected: adds `github.com/prometheus-community/pro-bing` to `go.mod`.

- [ ] **Step 4: Write the implementation**

Create `internal/probe/icmp.go`:
```go
// Package probe implements the network probes (ICMP baseline, isochronous UDP).
package probe

import (
	"time"

	probing "github.com/prometheus-community/pro-bing"
)

// Result is the outcome of a single ICMP probe.
type Result struct {
	RTT  time.Duration
	Lost bool
}

// PingICMP sends one ICMP echo to addr and returns its RTT, or Lost=true on
// timeout. On Windows, privileged mode uses IcmpSendEcho and needs no admin.
func PingICMP(addr string, timeout time.Duration) (Result, error) {
	pinger, err := probing.NewPinger(addr)
	if err != nil {
		return Result{}, err
	}
	pinger.Count = 1
	pinger.Timeout = timeout
	pinger.SetPrivileged(true)
	if err := pinger.Run(); err != nil {
		return Result{}, err
	}
	st := pinger.Statistics()
	if st.PacketsRecv == 0 {
		return Result{Lost: true}, nil
	}
	return Result{RTT: st.AvgRtt}, nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/probe/`
Expected: PASS (or SKIP if the environment forbids ICMP — acceptable; verify manually in Task 8).

- [ ] **Step 6: Commit**

```powershell
git add -A
git commit -m "feat: ICMP probe via pro-bing (unprivileged on Windows)"
```

---

### Task 5: Isochronous UDP probe + echo target

**Files:**
- Create: `internal/probe/udp.go`
- Test: `internal/probe/udp_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/probe/udp_test.go`:
```go
package probe

import (
	"testing"
	"time"
)

func TestProbeUDPLoopbackNoLoss(t *testing.T) {
	echo, err := StartUDPEcho("127.0.0.1:0")
	if err != nil {
		t.Fatalf("start echo: %v", err)
	}
	defer echo.Close()

	stats, err := ProbeUDP(echo.Addr(), 5, 5*time.Millisecond, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if stats.Sent != 5 {
		t.Fatalf("want Sent=5, got %d", stats.Sent)
	}
	if stats.Received != 5 {
		t.Fatalf("want Received=5 over loopback, got %d", stats.Received)
	}
	if stats.LossPct != 0 {
		t.Fatalf("want 0%% loss, got %.1f", stats.LossPct)
	}
}

func TestProbeUDPNoServerIsFullLoss(t *testing.T) {
	// Nothing listening on this port -> all packets lost.
	stats, err := ProbeUDP("127.0.0.1:59999", 4, 5*time.Millisecond, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if stats.Received != 0 || stats.LossPct != 100 {
		t.Fatalf("want full loss, got %+v", stats)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/probe/ -run UDP`
Expected: FAIL — `undefined: StartUDPEcho`

- [ ] **Step 3: Write the implementation**

Create `internal/probe/udp.go`:
```go
package probe

import (
	"encoding/binary"
	"net"
	"time"
)

// UDPStats summarizes one isochronous UDP probe run.
type UDPStats struct {
	Sent     int
	Received int
	LossPct  float64
	AvgRTT   time.Duration
	Jitter   time.Duration // mean abs diff of consecutive RTTs (IPDV)
}

// UDPEcho is a minimal UDP echo server: the probe target.
type UDPEcho struct{ conn *net.UDPConn }

// StartUDPEcho binds addr (e.g. "127.0.0.1:0") and echoes every packet back.
func StartUDPEcho(addr string) (*UDPEcho, error) {
	ua, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp", ua)
	if err != nil {
		return nil, err
	}
	e := &UDPEcho{conn: conn}
	go e.serve()
	return e, nil
}

// Addr returns the bound address (host:port).
func (e *UDPEcho) Addr() string { return e.conn.LocalAddr().String() }

// Close stops the echo server.
func (e *UDPEcho) Close() error { return e.conn.Close() }

func (e *UDPEcho) serve() {
	buf := make([]byte, 2048)
	for {
		n, raddr, err := e.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		_, _ = e.conn.WriteToUDP(buf[:n], raddr)
	}
}

// ProbeUDP sends count packets at a fixed interval (isochronous — it does not
// wait for replies between sends) and measures loss and jitter from the echoes.
func ProbeUDP(target string, count int, interval, timeout time.Duration) (UDPStats, error) {
	raddr, err := net.ResolveUDPAddr("udp", target)
	if err != nil {
		return UDPStats{}, err
	}
	conn, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		return UDPStats{}, err
	}
	defer conn.Close()

	sendTimes := make([]time.Time, count)
	received := make([]bool, count)
	var rtts []time.Duration
	done := make(chan struct{})

	go func() {
		buf := make([]byte, 64)
		for {
			_ = conn.SetReadDeadline(time.Now().Add(timeout))
			n, err := conn.Read(buf)
			if err != nil {
				close(done)
				return
			}
			if n >= 4 {
				seq := int(binary.BigEndian.Uint32(buf[:4]))
				if seq >= 0 && seq < count && !received[seq] {
					received[seq] = true
					rtts = append(rtts, time.Since(sendTimes[seq]))
				}
			}
		}
	}()

	pkt := make([]byte, 16)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for i := 0; i < count; i++ {
		binary.BigEndian.PutUint32(pkt[:4], uint32(i))
		sendTimes[i] = time.Now()
		_, _ = conn.Write(pkt)
		if i < count-1 {
			<-ticker.C
		}
	}

	time.Sleep(timeout)            // let stragglers arrive
	_ = conn.SetReadDeadline(time.Now()) // unblock the reader
	<-done                         // reader has stopped; safe to read results

	recv := 0
	for _, r := range received {
		if r {
			recv++
		}
	}
	stats := UDPStats{
		Sent:     count,
		Received: recv,
		LossPct:  float64(count-recv) / float64(count) * 100,
	}
	if len(rtts) > 0 {
		var sum time.Duration
		for _, r := range rtts {
			sum += r
		}
		stats.AvgRTT = sum / time.Duration(len(rtts))
		if len(rtts) > 1 {
			var jsum time.Duration
			for i := 1; i < len(rtts); i++ {
				d := rtts[i] - rtts[i-1]
				if d < 0 {
					d = -d
				}
				jsum += d
			}
			stats.Jitter = jsum / time.Duration(len(rtts)-1)
		}
	}
	return stats, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/probe/`
Expected: PASS

- [ ] **Step 5: Run the race detector (catches the reader/writer sync)**

Run: `go test -race ./internal/probe/ -run UDP`
Expected: PASS, no race reported.

- [ ] **Step 6: Commit**

```powershell
git add -A
git commit -m "feat: isochronous UDP probe with echo target (loss + jitter)"
```

---

### Task 6: Probe runner (probe → store)

**Files:**
- Create: `internal/probe/runner.go`
- Test: `internal/probe/runner_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/probe/runner_test.go`:
```go
package probe

import (
	"path/filepath"
	"testing"
	"time"

	"netlogger/internal/clock"
	"netlogger/internal/store"
)

func TestRunnerTickWritesSamples(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	// Fake pinger: "good" replies fast, "bad" is lost.
	fake := func(addr string, _ time.Duration) (Result, error) {
		if addr == "good" {
			return Result{RTT: 1200 * time.Microsecond}, nil
		}
		return Result{Lost: true}, nil
	}

	r := &Runner{
		Store:   s,
		Clock:   clock.Fixed{Micros: 5000},
		Src:     "self",
		Targets: []string{"good", "bad"},
		Ping:    fake,
		Timeout: time.Second,
	}
	if err := r.Tick(); err != nil {
		t.Fatalf("tick: %v", err)
	}

	rows, err := s.Since(0, 100)
	if err != nil {
		t.Fatalf("since: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 samples, got %d", len(rows))
	}
	byDst := map[string]store.Sample{}
	for _, r := range rows {
		byDst[r.DstHost] = r
	}
	if g := byDst["good"]; g.Lost || g.RTTus != 1200 || g.ProbeType != "icmp" {
		t.Fatalf("good sample wrong: %+v", g)
	}
	if b := byDst["bad"]; !b.Lost {
		t.Fatalf("bad sample should be Lost: %+v", byDst["bad"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/probe/ -run Runner`
Expected: FAIL — `undefined: Runner`

- [ ] **Step 3: Write the implementation**

Create `internal/probe/runner.go`:
```go
package probe

import (
	"time"

	"netlogger/internal/clock"
	"netlogger/internal/store"
)

// PingFunc abstracts the ICMP probe so the runner is testable with a fake.
type PingFunc func(addr string, timeout time.Duration) (Result, error)

// Runner probes a set of targets and writes one sample per target per Tick.
type Runner struct {
	Store   *store.Store
	Clock   clock.Clock
	Src     string
	Targets []string
	Ping    PingFunc
	Timeout time.Duration
}

// Tick probes every target once and persists the results.
func (r *Runner) Tick() error {
	for _, target := range r.Targets {
		res, err := r.Ping(target, r.Timeout)
		sm := store.Sample{
			TSUnixUS:  r.Clock.NowUnixMicro(),
			ProbeType: "icmp",
			SrcHost:   r.Src,
			DstHost:   target,
			Direction: "rtt",
		}
		if err != nil || res.Lost {
			sm.Lost = true
		} else {
			sm.RTTus = res.RTT.Microseconds()
		}
		if _, err := r.Store.Insert(sm); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/probe/`
Expected: PASS

- [ ] **Step 5: Commit**

```powershell
git add -A
git commit -m "feat: probe runner persisting ICMP results to the store"
```

---

### Task 7: Web server + embedded status page

**Files:**
- Create: `internal/web/web.go`, `internal/web/static/index.html`
- Test: `internal/web/web_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/web/web_test.go`:
```go
package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"netlogger/internal/version"
)

func TestStatusEndpoint(t *testing.T) {
	srv := &Server{Host: "ryzen", ServiceState: "running"}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status code: %d", rr.Code)
	}
	var got Status
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Host != "ryzen" || got.ServiceState != "running" || got.Version != version.Version {
		t.Fatalf("bad status payload: %+v", got)
	}
}

func TestServesIndex(t *testing.T) {
	srv := &Server{Host: "ryzen", ServiceState: "running"}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status code: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "NetLogger") {
		t.Fatalf("index did not render NetLogger title")
	}
}
```

- [ ] **Step 2: Create the embedded page**

Create `internal/web/static/index.html`:
```html
<!DOCTYPE html>
<html lang="en">
<head><meta charset="utf-8"><title>NetLogger</title>
<style>body{font-family:system-ui;background:#0d1117;color:#e6edf3;padding:2rem}
.k{color:#677483}.v{font-family:monospace}</style></head>
<body>
  <h1>NetLogger <span class="v" id="ver"></span></h1>
  <p><span class="k">host:</span> <span class="v" id="host"></span></p>
  <p><span class="k">service:</span> <span class="v" id="svc"></span></p>
<script>
fetch('/api/status').then(r=>r.json()).then(s=>{
  document.getElementById('ver').textContent=s.version;
  document.getElementById('host').textContent=s.host;
  document.getElementById('svc').textContent=s.service_state;
});
</script>
</body>
</html>
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/web/`
Expected: FAIL — `undefined: Server`

- [ ] **Step 4: Write the implementation**

Create `internal/web/web.go`:
```go
// Package web serves the embedded status SPA and a status JSON endpoint.
package web

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"

	"netlogger/internal/version"
)

//go:embed static/*
var content embed.FS

// Status is the JSON payload for /api/status.
type Status struct {
	Host         string `json:"host"`
	Version      string `json:"version"`
	ServiceState string `json:"service_state"`
}

// Server holds the live state shown on the status page.
type Server struct {
	Host         string
	ServiceState string
}

// Handler returns the HTTP handler (status API + embedded static files).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Status{
			Host:         s.Host,
			Version:      version.Version,
			ServiceState: s.ServiceState,
		})
	})
	sub, err := fs.Sub(content, "static")
	if err != nil {
		panic(err)
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))
	return mux
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/web/`
Expected: PASS

- [ ] **Step 6: Commit**

```powershell
git add -A
git commit -m "feat: web server with embedded status page and /api/status"
```

---

### Task 8: CLI + Windows service wiring (manual verification)

**Files:**
- Create: `internal/agentsvc/agentsvc.go`
- Modify: `cmd/netlogger/main.go` (replace Task 0 stub)

- [ ] **Step 1: Add the service dependency**

Run: `go get github.com/kardianos/service`
Expected: adds `github.com/kardianos/service` to `go.mod`.

- [ ] **Step 2: Write the agent service program**

Create `internal/agentsvc/agentsvc.go`:
```go
// Package agentsvc wires the probe runner + web server into a kardianos service.
package agentsvc

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/kardianos/service"

	"netlogger/internal/clock"
	"netlogger/internal/probe"
	"netlogger/internal/store"
	"netlogger/internal/web"
)

// Program is the long-running agent: probe loop + status web server.
type Program struct {
	DBPath string
	Listen string // e.g. "127.0.0.1:8088"

	store  *store.Store
	srv    *http.Server
	cancel context.CancelFunc
}

// Start is called by the service manager; it must not block.
func (p *Program) Start(s service.Service) error {
	st, err := store.Open(p.DBPath)
	if err != nil {
		return err
	}
	p.store = st

	host, _ := os.Hostname()
	ws := &web.Server{Host: host, ServiceState: "running"}
	p.srv = &http.Server{Addr: p.Listen, Handler: ws.Handler()}

	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel

	go p.srv.ListenAndServe()
	go p.loop(ctx, host)
	return nil
}

func (p *Program) loop(ctx context.Context, host string) {
	runner := &probe.Runner{
		Store:   p.store,
		Clock:   clock.System{},
		Src:     host,
		Targets: []string{"127.0.0.1"}, // M1: self-ping proof of life; targets come from config in M2+
		Ping:    probe.PingICMP,
		Timeout: 2 * time.Second,
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = runner.Tick()
		}
	}
}

// Stop is called by the service manager on shutdown.
func (p *Program) Stop(s service.Service) error {
	if p.cancel != nil {
		p.cancel()
	}
	if p.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = p.srv.Shutdown(ctx)
	}
	if p.store != nil {
		_ = p.store.Close()
	}
	return nil
}
```

- [ ] **Step 3: Rewrite main.go with subcommands**

Replace `cmd/netlogger/main.go` entirely:
```go
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kardianos/service"

	"netlogger/internal/agentsvc"
	"netlogger/internal/version"
)

func dataDir() string {
	if pd := os.Getenv("ProgramData"); pd != "" {
		return filepath.Join(pd, "NetLogger")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".netlogger")
}

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "version" {
		fmt.Println("netlogger", version.Version)
		return
	}

	dir := dataDir()
	_ = os.MkdirAll(dir, 0o755)

	prog := &agentsvc.Program{
		DBPath: filepath.Join(dir, "netlogger.db"),
		Listen: "127.0.0.1:8088",
	}
	svcConfig := &service.Config{
		Name:        "NetLogger",
		DisplayName: "NetLogger Agent",
		Description: "NetLogger network diagnostic agent.",
	}
	s, err := service.New(prog, svcConfig)
	if err != nil {
		fmt.Fprintln(os.Stderr, "service init:", err)
		os.Exit(1)
	}

	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "install", "uninstall", "start", "stop":
			if err := service.Control(s, os.Args[1]); err != nil {
				fmt.Fprintln(os.Stderr, os.Args[1], "failed:", err)
				os.Exit(1)
			}
			fmt.Println("netlogger:", os.Args[1], "ok")
			return
		case "run":
			// run in foreground (Ctrl+C to stop)
		default:
			fmt.Fprintln(os.Stderr, "usage: netlogger [version|install|uninstall|start|stop|run]")
			os.Exit(2)
		}
	}

	if err := s.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "run:", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 4: Build the whole project**

Run: `go build -o bin/netlogger.exe ./cmd/netlogger`
Expected: builds with no errors.

- [ ] **Step 5: Run the full test suite**

Run: `go test ./...`
Expected: all packages PASS (probe ICMP tests may SKIP if the env forbids ICMP).

- [ ] **Step 6: Manual verification — foreground run**

Run in one shell: `./bin/netlogger.exe run`
In a browser open `http://127.0.0.1:8088/` → shows "NetLogger 0.1.0-m1", host, service "running".
Then `Ctrl+C`. Confirm a `netlogger.db` exists under `%ProgramData%\NetLogger\` with rows:
```powershell
# optional: confirm DB file exists
Test-Path "$env:ProgramData\NetLogger\netlogger.db"
```
Expected: `True`, and the page rendered.

- [ ] **Step 7: Manual verification — Windows Service (elevated shell)**

In an **Administrator** PowerShell:
```powershell
./bin/netlogger.exe install
./bin/netlogger.exe start
Get-Service NetLogger    # Status should be Running
Start-Sleep -Seconds 3
Invoke-RestMethod http://127.0.0.1:8088/api/status   # host/version/service_state
./bin/netlogger.exe stop
./bin/netlogger.exe uninstall
```
Expected: service installs, runs, `/api/status` returns JSON, stops and uninstalls cleanly.

- [ ] **Step 8: Commit**

```powershell
git add -A
git commit -m "feat: CLI + Windows service wiring; agent probe loop + status server"
```

---

## Done criteria for M1

- `go test ./...` passes.
- `netlogger.exe run` serves the status page and writes ICMP samples to a WAL SQLite DB under `%ProgramData%\NetLogger`.
- `netlogger.exe install/start/stop/uninstall` manage a real Windows Service.
- All work committed in small, green increments.

This is the vertical spine. M2 adds the coordinator role, resilient sync, configuration-readiness checks, and the multi-host mesh; targets move from the hardcoded `127.0.0.1` to the config file from Task 1.
