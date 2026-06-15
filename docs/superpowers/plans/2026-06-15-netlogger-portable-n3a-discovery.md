# NetLogger Portable — N3a: Identity + LAN Discovery — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Build the self-contained LAN auto-discovery layer: a stable per-machine identity (persistent UUID) and a `discovery` package where instances announce themselves over UDP multicast and maintain a live peer table — so two+ copies of the app find each other with zero config. (Wiring this into the engine/UI is the separate N3b plan.)

**Architecture:** A small `identity` package persists a UUID in the data dir. A `discovery` package has three layers: a pure, lock-guarded **peer table** (dedup by UUID, TTL expiry — fully unit-tested); a JSON **announce** wire record with a magic guard; and a **Service** that joins a private multicast group on every real NIC, sends a 3-burst-then-heartbeat announce, listens, and feeds the table. Socket reuse uses a build-tagged `SO_REUSEADDR` control func (Windows vs other).

**Tech Stack:** Go (cgo-free), `golang.org/x/net/ipv4` (multicast join/send/recv), `golang.org/x/sys/windows` (SO_REUSEADDR control on Windows), `github.com/google/uuid`.

Reference spec: `docs/superpowers/specs/2026-06-15-netlogger-portable-design.md` §3.2 (discovery: private `239.255.x.y` group, TTL=1, persistent-UUID identity, multi-NIC join, 3-burst + heartbeat, filter own UUID).

Design constants (one place): multicast group `239.255.74.76`, discovery UDP port `48076`, magic `"nlldisc1"`, default announce interval `3s`, default peer TTL `12s` (≈4 missed).

---

## File Structure

| Path | Responsibility |
|---|---|
| `internal/identity/identity.go` | `NodeID(dir)` — load-or-create a stable UUID persisted at `<dir>/node-id`. |
| `internal/identity/identity_test.go` | Tests: creates on first call, stable on second. |
| `internal/discovery/peer.go` | `Peer` type + the pure peer `table` (upsert/expire/list, dedup by ID, injectable clock). |
| `internal/discovery/peer_test.go` | Table tests (dedup, expiry, list). |
| `internal/discovery/message.go` | `announce` wire record + `encode`/`decode` with magic guard. |
| `internal/discovery/message_test.go` | Encode/decode roundtrip + bad-magic rejection. |
| `internal/discovery/reuseaddr_windows.go` | `reuseControl` setting `SO_REUSEADDR` before bind (Windows). |
| `internal/discovery/reuseaddr_other.go` | `reuseControl` for non-Windows (SO_REUSEADDR via x/sys/unix-free syscall). |
| `internal/discovery/service.go` | `Service`: join group per NIC, announce loop (burst+heartbeat), listen loop, `Peers()`, `Stop()` (sends bye). |
| `internal/discovery/service_test.go` | Loopback integration test: two Services discover each other. |

---

## Task 1: `internal/identity` — stable node UUID

**Files:** Create `internal/identity/identity.go`, Test `internal/identity/identity_test.go`.

- [ ] **Step 1: Write the failing test**

```go
package identity

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNodeIDCreatesAndPersists(t *testing.T) {
	dir := t.TempDir()
	id1, err := NodeID(dir)
	if err != nil {
		t.Fatalf("NodeID: %v", err)
	}
	if len(id1) < 16 {
		t.Fatalf("expected a UUID-like id, got %q", id1)
	}
	if _, err := os.Stat(filepath.Join(dir, "node-id")); err != nil {
		t.Fatalf("expected node-id file: %v", err)
	}
	id2, err := NodeID(dir)
	if err != nil {
		t.Fatalf("NodeID 2nd: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("id not stable: %q vs %q", id1, id2)
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/identity/ -v` → FAIL (undefined: NodeID).

- [ ] **Step 3: Implement**

```go
// Package identity provides a stable per-machine node id, persisted in the data
// dir so it survives restarts (discovery dedups peers by this id, not by IP).
package identity

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// NodeID returns the persisted node id from <dir>/node-id, creating it on first
// call.
func NodeID(dir string) (string, error) {
	path := filepath.Join(dir, "node-id")
	if b, err := os.ReadFile(path); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return s, nil
		}
	}
	id := uuid.NewString()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir for node-id: %w", err)
	}
	if err := os.WriteFile(path, []byte(id), 0o644); err != nil {
		return "", fmt.Errorf("write node-id: %w", err)
	}
	return id, nil
}
```

- [ ] **Step 4: Run to verify pass** — `go test ./internal/identity/ -v` → PASS. Then `gofmt -w internal/identity/ && go vet ./internal/identity/`.

- [ ] **Step 5: Commit** — `git add internal/identity/ && git commit -m "feat(identity): stable persisted node UUID (N3a)"` (with the Co-Authored-By footer).

---

## Task 2: `internal/discovery` peer table (pure)

**Files:** Create `internal/discovery/peer.go`, Test `internal/discovery/peer_test.go`.

- [ ] **Step 1: Write the failing test**

```go
package discovery

import (
	"testing"
	"time"
)

func TestTableDedupsByID(t *testing.T) {
	base := time.Unix(1000, 0)
	tbl := newTable(10*time.Second, func() time.Time { return base })
	tbl.upsert(Peer{ID: "a", Host: "h1", Addr: "10.0.0.1:8088"})
	tbl.upsert(Peer{ID: "a", Host: "h1", Addr: "10.0.0.2:8088"}) // same ID, new addr
	got := tbl.list()
	if len(got) != 1 {
		t.Fatalf("expected 1 peer after dedup, got %d", len(got))
	}
	if got[0].Addr != "10.0.0.2:8088" {
		t.Fatalf("expected addr updated to newest, got %q", got[0].Addr)
	}
}

func TestTableExpiresStalePeers(t *testing.T) {
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	tbl := newTable(10*time.Second, clock)
	tbl.upsert(Peer{ID: "a", Host: "h1", Addr: "x"})
	now = now.Add(5 * time.Second)
	tbl.upsert(Peer{ID: "b", Host: "h2", Addr: "y"}) // b is fresher
	now = now.Add(6 * time.Second)                   // a is now 11s old (>10s TTL), b is 6s
	got := tbl.list()
	if len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("expected only fresh peer b, got %+v", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/discovery/ -run TestTable -v` → FAIL.

- [ ] **Step 3: Implement `internal/discovery/peer.go`**

```go
package discovery

import (
	"sort"
	"sync"
	"time"
)

// Peer is a discovered instance on the LAN.
type Peer struct {
	ID       string    // stable node UUID
	Host     string    // advertised hostname
	Addr     string    // control endpoint host:port
	Version  string    // app version
	LastSeen time.Time // monotonic last-heard time
}

// table is the thread-safe peer table: dedup by ID, expire by TTL.
type table struct {
	ttl   time.Duration
	now   func() time.Time
	mu    sync.Mutex
	peers map[string]Peer
}

func newTable(ttl time.Duration, now func() time.Time) *table {
	return &table{ttl: ttl, now: now, peers: make(map[string]Peer)}
}

// upsert records/refreshes a peer, stamping LastSeen.
func (t *table) upsert(p Peer) {
	t.mu.Lock()
	defer t.mu.Unlock()
	p.LastSeen = t.now()
	t.peers[p.ID] = p
}

// remove deletes a peer by id (used on a "bye").
func (t *table) remove(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.peers, id)
}

// list returns the non-expired peers, sorted by Host then ID for stable order.
func (t *table) list() []Peer {
	t.mu.Lock()
	defer t.mu.Unlock()
	cutoff := t.now().Add(-t.ttl)
	out := make([]Peer, 0, len(t.peers))
	for id, p := range t.peers {
		if p.LastSeen.Before(cutoff) {
			delete(t.peers, id)
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Host != out[j].Host {
			return out[i].Host < out[j].Host
		}
		return out[i].ID < out[j].ID
	})
	return out
}
```

- [ ] **Step 4: Run to verify pass** — `go test ./internal/discovery/ -run TestTable -v` → PASS. `gofmt -w internal/discovery/ && go vet ./internal/discovery/`.

- [ ] **Step 5: Commit** — `git add internal/discovery/peer.go internal/discovery/peer_test.go && git commit -m "feat(discovery): peer table with id-dedup and TTL expiry (N3a)"`.

---

## Task 3: `internal/discovery` announce message

**Files:** Create `internal/discovery/message.go`, Test `internal/discovery/message_test.go`.

- [ ] **Step 1: Write the failing test**

```go
package discovery

import "testing"

func TestEncodeDecodeRoundTrip(t *testing.T) {
	a := announce{ID: "abc", Host: "ryzen", Port: 8088, Version: "1.0", Bye: false}
	data := encode(a)
	got, ok := decode(data)
	if !ok {
		t.Fatalf("decode failed on valid payload")
	}
	if got != a {
		t.Fatalf("roundtrip mismatch: %+v vs %+v", got, a)
	}
}

func TestDecodeRejectsForeignPayload(t *testing.T) {
	if _, ok := decode([]byte(`{"hello":"world"}`)); ok {
		t.Fatalf("expected decode to reject payload without our magic")
	}
	if _, ok := decode([]byte("garbage")); ok {
		t.Fatalf("expected decode to reject non-JSON")
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/discovery/ -run TestEncode -v` and `-run TestDecode` → FAIL.

- [ ] **Step 3: Implement `internal/discovery/message.go`**

```go
package discovery

import "encoding/json"

// magic guards our datagrams from other traffic that may land on the port.
const magic = "nlldisc1"

// announce is the multicast wire record (small JSON).
type announce struct {
	Magic   string `json:"m"`
	ID      string `json:"id"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	Version string `json:"ver"`
	Bye     bool   `json:"bye,omitempty"`
}

func encode(a announce) []byte {
	a.Magic = magic
	b, _ := json.Marshal(a)
	return b
}

// decode parses a datagram, returning ok=false unless it carries our magic.
func decode(data []byte) (announce, bool) {
	var a announce
	if err := json.Unmarshal(data, &a); err != nil {
		return announce{}, false
	}
	if a.Magic != magic {
		return announce{}, false
	}
	return a, true
}
```

- [ ] **Step 4: Run to verify pass** — `go test ./internal/discovery/ -run 'TestEncode|TestDecode' -v` → PASS. `gofmt -w internal/discovery/`.

- [ ] **Step 5: Commit** — `git add internal/discovery/message.go internal/discovery/message_test.go && git commit -m "feat(discovery): announce wire record with magic guard (N3a)"`.

---

## Task 4: SO_REUSEADDR control func (build-tagged)

**Files:** Create `internal/discovery/reuseaddr_windows.go`, `internal/discovery/reuseaddr_other.go`.

> No unit test (raw syscall); exercised by the Task 5 integration test, which binds two sockets to the same port in one process — only possible with SO_REUSEADDR.

- [ ] **Step 1: Implement `internal/discovery/reuseaddr_windows.go`**

```go
//go:build windows

package discovery

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// reuseControl sets SO_REUSEADDR before bind so multiple sockets (and multiple
// instances on one host) can share the multicast discovery port.
func reuseControl(network, address string, c syscall.RawConn) error {
	var serr error
	if err := c.Control(func(fd uintptr) {
		serr = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, windows.SO_REUSEADDR, 1)
	}); err != nil {
		return err
	}
	return serr
}
```

- [ ] **Step 2: Implement `internal/discovery/reuseaddr_other.go`**

```go
//go:build !windows

package discovery

import (
	"syscall"

	"golang.org/x/sys/unix"
)

func reuseControl(network, address string, c syscall.RawConn) error {
	var serr error
	if err := c.Control(func(fd uintptr) {
		serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
	}); err != nil {
		return err
	}
	return serr
}
```

- [ ] **Step 3: Verify it compiles (Windows path)** — `go build ./internal/discovery/`. If `golang.org/x/sys/unix` is not yet a dependency it will be fetched on the next `go mod tidy` (done in Task 5); on Windows the `_other.go` file isn't compiled, so the build passes now.

- [ ] **Step 4: Commit** — `git add internal/discovery/reuseaddr_windows.go internal/discovery/reuseaddr_other.go && git commit -m "feat(discovery): SO_REUSEADDR control for shared multicast port (N3a)"`.

---

## Task 5: `internal/discovery` Service (multicast join/announce/listen)

**Files:** Create `internal/discovery/service.go`, Test `internal/discovery/service_test.go`.

**Context:** This is the integration layer. It binds a UDP socket to the wildcard address on the discovery port with `reuseControl`, joins the multicast group on every real (up, multicast-capable, non-loopback-only) interface, sends announces (a 3-burst on Start then a heartbeat), and listens — feeding the peer `table` (skipping our own ID). `Stop` sends a "bye" and closes. The test runs two Services in one process (different IDs, same group/port via SO_REUSEADDR) and asserts each discovers the other over loopback multicast.

- [ ] **Step 1: Write the failing integration test `internal/discovery/service_test.go`**

```go
package discovery

import (
	"testing"
	"time"
)

func waitForPeer(t *testing.T, s *Service, wantID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, p := range s.Peers() {
			if p.ID == wantID {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("peer %q not discovered within timeout", wantID)
}

func TestTwoServicesDiscoverEachOther(t *testing.T) {
	cfg := Config{
		Group:    "239.255.74.76",
		Port:     48076,
		Interval: 200 * time.Millisecond,
		TTL:      3 * time.Second,
		Version:  "test",
	}
	a := cfg
	a.SelfID, a.Host, a.ControlPort = "node-a", "hostA", 18088
	b := cfg
	b.SelfID, b.Host, b.ControlPort = "node-b", "hostB", 18089

	sa := New(a)
	if err := sa.Start(); err != nil {
		t.Skipf("multicast unavailable in this environment: %v", err)
	}
	defer sa.Stop()
	sb := New(b)
	if err := sb.Start(); err != nil {
		t.Skipf("multicast unavailable in this environment: %v", err)
	}
	defer sb.Stop()

	waitForPeer(t, sa, "node-b")
	waitForPeer(t, sb, "node-a")

	// A peer's advertised control endpoint should be host:port.
	for _, p := range sa.Peers() {
		if p.ID == "node-b" && p.Addr == "" {
			t.Fatalf("expected non-empty control addr for node-b")
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/discovery/ -run TestTwoServices -v` → FAIL (undefined: Config/New/Service).

- [ ] **Step 3: Implement `internal/discovery/service.go`**

```go
package discovery

import (
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"golang.org/x/net/ipv4"
)

// Config configures a discovery Service.
type Config struct {
	SelfID      string
	Host        string
	ControlPort int
	Version     string
	Group       string        // multicast group, e.g. "239.255.74.76"
	Port        int           // discovery UDP port
	Interval    time.Duration // announce heartbeat (default 3s)
	TTL         time.Duration // peer expiry (default 12s)
}

// Service announces this instance and tracks discovered peers.
type Service struct {
	cfg   Config
	group net.IP
	tbl   *table

	conn   *net.UDPConn
	pc     *ipv4.PacketConn
	stop   chan struct{}
	wg     sync.WaitGroup
	closed sync.Once
}

// New creates a Service. Call Start to begin.
func New(cfg Config) *Service {
	if cfg.Interval <= 0 {
		cfg.Interval = 3 * time.Second
	}
	if cfg.TTL <= 0 {
		cfg.TTL = 12 * time.Second
	}
	return &Service{
		cfg:   cfg,
		group: net.ParseIP(cfg.Group),
		tbl:   newTable(cfg.TTL, time.Now),
		stop:  make(chan struct{}),
	}
}

// Start binds the socket, joins the group on all real interfaces, and launches
// the announce + listen loops.
func (s *Service) Start() error {
	lc := net.ListenConfig{Control: reuseControl}
	pktConn, err := lc.ListenPacket(ctxBackground(), "udp4", "0.0.0.0:"+strconv.Itoa(s.cfg.Port))
	if err != nil {
		return fmt.Errorf("bind discovery socket: %w", err)
	}
	udp, ok := pktConn.(*net.UDPConn)
	if !ok {
		pktConn.Close()
		return fmt.Errorf("discovery socket is not *net.UDPConn")
	}
	s.conn = udp
	s.pc = ipv4.NewPacketConn(udp)
	_ = s.pc.SetMulticastLoopback(true) // hear other instances on the same host
	_ = s.pc.SetMulticastTTL(1)         // keep strictly link-local

	gaddr := &net.UDPAddr{IP: s.group, Port: s.cfg.Port}
	joined := 0
	for _, ifi := range multicastInterfaces() {
		if err := s.pc.JoinGroup(&ifi, gaddr); err == nil {
			joined++
		}
	}
	if joined == 0 {
		s.conn.Close()
		return fmt.Errorf("could not join multicast group on any interface")
	}

	s.wg.Add(2)
	go s.announceLoop(gaddr)
	go s.listenLoop()
	return nil
}

func (s *Service) announceLoop(gaddr *net.UDPAddr) {
	defer s.wg.Done()
	msg := encode(announce{
		ID: s.cfg.SelfID, Host: s.cfg.Host, Port: s.cfg.ControlPort, Version: s.cfg.Version,
	})
	// Startup burst: 3 quick announces so peers learn us fast despite UDP loss.
	for i := 0; i < 3; i++ {
		s.sendTo(gaddr, msg)
		select {
		case <-s.stop:
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
	t := time.NewTicker(s.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			s.sendTo(gaddr, msg)
		}
	}
}

func (s *Service) sendTo(gaddr *net.UDPAddr, msg []byte) {
	// Send out every real interface so all subnets hear us.
	for _, ifi := range multicastInterfaces() {
		_ = s.pc.SetMulticastInterface(&ifi)
		_, _ = s.pc.WriteTo(msg, nil, gaddr)
	}
}

func (s *Service) listenLoop() {
	defer s.wg.Done()
	buf := make([]byte, 2048)
	for {
		select {
		case <-s.stop:
			return
		default:
		}
		_ = s.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, src, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			continue // timeout or closed; loop re-checks stop
		}
		a, ok := decode(buf[:n])
		if !ok || a.ID == s.cfg.SelfID {
			continue // foreign payload or our own echo
		}
		if a.Bye {
			s.tbl.remove(a.ID)
			continue
		}
		// Prefer the datagram's source IP over the self-reported host.
		host := a.Host
		ip := src.IP.String()
		s.tbl.upsert(Peer{
			ID:      a.ID,
			Host:    host,
			Addr:    net.JoinHostPort(ip, strconv.Itoa(a.Port)),
			Version: a.Version,
		})
	}
}

// Peers returns the currently-live discovered peers (excludes self and expired).
func (s *Service) Peers() []Peer { return s.tbl.list() }

// Stop announces a bye, stops the loops, and closes the socket.
func (s *Service) Stop() error {
	s.closed.Do(func() {
		// Best-effort bye so peers drop us quickly.
		if s.pc != nil {
			bye := encode(announce{ID: s.cfg.SelfID, Bye: true})
			s.sendTo(&net.UDPAddr{IP: s.group, Port: s.cfg.Port}, bye)
		}
		close(s.stop)
		if s.conn != nil {
			s.conn.Close()
		}
		s.wg.Wait()
	})
	return nil
}

// multicastInterfaces returns up, multicast-capable, non-pure-loopback NICs.
func multicastInterfaces() []net.Interface {
	all, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []net.Interface
	for _, ifi := range all {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagMulticast == 0 {
			continue
		}
		out = append(out, ifi)
	}
	return out
}
```

Also add a tiny helper at the bottom of `service.go` (kept separate so the import set is obvious):

```go
import "context"

func ctxBackground() context.Context { return context.Background() }
```

(Combine the two import blocks into one; `context` joins the existing imports. The helper exists only to keep `ListenConfig.ListenPacket`'s context call readable.)

- [ ] **Step 4: tidy modules + run the test**

Run:
```
go mod tidy
go test ./internal/discovery/ -run TestTwoServices -v
```
Expected: PASS — both services discover each other. (If the dev box blocks multicast entirely, the test will `t.Skip` with the bind error; on a normal Windows desktop loopback multicast works, so expect a real PASS. Note `go mod tidy` promotes `golang.org/x/net` and adds `golang.org/x/sys/unix` usage for the non-Windows file.)

- [ ] **Step 5: run the whole discovery package + vet/fmt**

Run: `go test -count=1 ./internal/discovery/ ./internal/identity/ && go vet ./internal/discovery/ && gofmt -l internal/discovery internal/identity`
Expected: all PASS, vet clean, gofmt prints nothing.

- [ ] **Step 6: Commit** — `git add internal/discovery/service.go internal/discovery/service_test.go go.mod go.sum && git commit -m "feat(discovery): multicast announce/listen Service with loopback test (N3a)"`.

---

## Self-Review

**Spec coverage (§3.2 discovery):**
- Private multicast group `239.255.74.76` + TTL=1 → Task 5 (`SetMulticastTTL(1)`, group const). ✓
- Persistent UUID identity → Task 1. ✓
- Dedup by UUID, prefer source IP over self-reported → Task 2 (table by ID) + Task 5 (`src.IP` in upsert). ✓
- 3-burst then heartbeat → Task 5 `announceLoop`. ✓
- Graceful "bye" on shutdown → Task 5 `Stop`. ✓
- Multi-NIC: wildcard bind + JoinGroup per real NIC + SetMulticastInterface per send → Task 5 + Task 4 (SO_REUSEADDR). ✓
- Filter own UUID on receive → Task 5 (`a.ID == s.cfg.SelfID`). ✓
- TTL expiry of stale peers → Task 2 (`list` cutoff). ✓

Out of N3a scope (N3b): wiring discovery into `appcore`, the in-process HTTP sync API server, peer-driven probe/pull/offset loops, the elevated firewall rule for the discovery/control ports, `Snapshot.Peers`, and the UI peer list. Manual add-by-IP (spec §3.2) is also N3b (it feeds the same peer set without multicast).

**Placeholder scan:** none. Every step has complete code + exact commands.

**Type consistency:** `Peer{ID,Host,Addr,Version,LastSeen}`, `announce{Magic,ID,Host,Port,Version,Bye}`, `Config{SelfID,Host,ControlPort,Version,Group,Port,Interval,TTL}`, `newTable(ttl, now)`, `encode/decode`, `reuseControl(network,address,RawConn)`, `New(Config) *Service`, `Service.Start()/Peers()/Stop()` are referenced identically across tasks and tests.
