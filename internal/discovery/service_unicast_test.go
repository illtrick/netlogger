package discovery

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"
)

// rawSock binds a specific loopback address on the discovery port (coexists
// with the service's wildcard bind via SO_REUSEADDR) so a test can play the
// role of a remote node.
func rawSock(t *testing.T, addr string) *net.UDPConn {
	t.Helper()
	lc := net.ListenConfig{Control: reuseControl}
	pc, err := lc.ListenPacket(context.Background(), "udp4", addr)
	if err != nil {
		t.Skipf("cannot bind test socket %s: %v", addr, err)
	}
	t.Cleanup(func() { pc.Close() })
	return pc.(*net.UDPConn)
}

func startService(t *testing.T, port int) *Service {
	t.Helper()
	s := New(Config{
		SelfID: "self-node", Host: "selfhost", ControlPort: 19099, Version: "test",
		Group: "239.255.74.76", Port: port,
		Interval: 200 * time.Millisecond, TTL: 3 * time.Second,
	})
	if err := s.Start(); err != nil {
		t.Skipf("multicast unavailable in this environment: %v", err)
	}
	t.Cleanup(func() { s.Stop() })
	return s
}

// A plain unicast announce (no multicast involved) must be accepted — this is
// what makes discovery work on networks that filter multicast.
func TestUnicastAnnounceAccepted(t *testing.T) {
	const port = 48181
	s := startService(t, port)

	sender := rawSock(t, "127.0.0.1:0") // ephemeral send-only socket
	msg := encode(announce{ID: "uni-node", Host: "otherhost", IP: "10.9.9.9", Port: 1234, Version: "test"})
	dst := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}
	for i := 0; i < 3; i++ {
		if _, err := sender.WriteToUDP(msg, dst); err != nil {
			t.Fatalf("send: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	waitForPeer(t, s, "uni-node")
	for _, p := range s.Peers() {
		if p.ID == "uni-node" && p.Addr != net.JoinHostPort("10.9.9.9", strconv.Itoa(1234)) {
			t.Fatalf("peer addr = %q, want self-reported IP", p.Addr)
		}
	}
}

// Hearing a (non-reply) announce must produce exactly one rate-limited
// unicast reply — the path that completes discovery when multicast is
// one-way (Wi-Fi APs filtering group traffic toward wireless clients).
func TestHandleAnnounceReplies(t *testing.T) {
	s := New(Config{
		SelfID: "self-node", Host: "selfhost", ControlPort: 19099, Version: "test",
		Group: "239.255.74.76", Port: 48182,
		Interval: 150 * time.Millisecond, TTL: 3 * time.Second,
	})
	msg := encode(announce{ID: "far-node", Host: "farhost", IP: "10.1.2.3", Port: 2345, Version: "test"})

	reply := s.handleDatagram(msg, "10.1.2.3")
	if reply == nil {
		t.Fatal("no reply to a heard announce")
	}
	a, ok := decode(reply)
	if !ok || a.ID != "self-node" || !a.Reply {
		t.Fatalf("reply = %+v ok=%v, want self announce with reply flag", a, ok)
	}

	// Same peer again inside the interval: rate-limited, no second reply.
	if r := s.handleDatagram(msg, "10.1.2.3"); r != nil {
		t.Fatal("replied twice within the interval")
	}
	// After the interval it replies again (steady-state one per interval).
	time.Sleep(200 * time.Millisecond)
	if r := s.handleDatagram(msg, "10.1.2.3"); r == nil {
		t.Fatal("no reply after the rate-limit interval elapsed")
	}
	// The peer was learned regardless.
	found := false
	for _, p := range s.Peers() {
		if p.ID == "far-node" && p.Addr == "10.1.2.3:2345" {
			found = true
		}
	}
	if !found {
		t.Fatalf("far-node not in table: %+v", s.Peers())
	}
}

// Replies must not be answered (loop prevention), but still count as peers.
func TestHandleReplyNotAnswered(t *testing.T) {
	s := New(Config{
		SelfID: "self-node", Host: "selfhost", ControlPort: 19099, Version: "test",
		Group: "239.255.74.76", Port: 48183,
		Interval: 150 * time.Millisecond, TTL: 3 * time.Second,
	})
	msg := encode(announce{ID: "quiet-node", Host: "quiethost", IP: "10.4.5.6", Port: 3456, Version: "test", Reply: true})
	if r := s.handleDatagram(msg, "10.4.5.6"); r != nil {
		t.Fatal("service answered a reply — loop prevention broken")
	}
	found := false
	for _, p := range s.Peers() {
		if p.ID == "quiet-node" {
			found = true
		}
	}
	if !found {
		t.Fatal("reply announce should still register the peer")
	}
}

func TestBroadcastFromAddrs(t *testing.T) {
	_, n1, _ := net.ParseCIDR("192.168.1.42/24")
	n1.IP = net.ParseIP("192.168.1.42") // ParseCIDR masks the IP; keep the host part
	_, n2, _ := net.ParseCIDR("10.0.0.7/30")
	n2.IP = net.ParseIP("10.0.0.7")
	got := broadcastFromAddrs([]net.Addr{n1, n2, &net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(64, 128)}})
	if len(got) != 2 {
		t.Fatalf("broadcasts = %v, want 2 IPv4 entries", got)
	}
	if got[0].String() != "192.168.1.255" {
		t.Errorf("bcast[0] = %s, want 192.168.1.255", got[0])
	}
	if got[1].String() != "10.0.0.7" { // /30: 10.0.0.4 network → broadcast 10.0.0.7
		t.Errorf("bcast[1] = %s, want 10.0.0.7", got[1])
	}
}

func TestAnnounceReplyFlagRoundTrip(t *testing.T) {
	b := encode(announce{ID: "x", Host: "h", Port: 1, Version: "v", Reply: true})
	a, ok := decode(b)
	if !ok || !a.Reply {
		t.Fatalf("reply flag lost: %+v ok=%v", a, ok)
	}
	// Old-style announce without the flag decodes with Reply=false.
	b2 := []byte(`{"m":"nlldisc1","id":"y","host":"h2","port":2,"ver":"v"}`)
	a2, ok := decode(b2)
	if !ok || a2.Reply {
		t.Fatalf("legacy announce mis-decoded: %+v ok=%v", a2, ok)
	}
}
