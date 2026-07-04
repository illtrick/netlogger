package discovery

import (
	"net"
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

	for _, p := range sa.Peers() {
		if p.ID == "node-b" && p.Addr == "" {
			t.Fatalf("expected non-empty control addr for node-b")
		}
	}
}

func TestIsSelfAnnounce(t *testing.T) {
	selfIPs := map[string]bool{"192.168.0.154": true, "127.0.0.1": true}
	cases := []struct {
		name          string
		a             announce
		src, id, host string
		wantSelf      bool
	}{
		{"same node id", announce{ID: "me"}, "1.2.3.4", "me", "ryzen", true},
		{"second local identity (our host + our ip)", announce{ID: "other", Host: "ryzen", IP: "192.168.0.154"}, "192.168.0.154", "me", "ryzen", true},
		{"same host but different machine ip", announce{ID: "other", Host: "ryzen", IP: "192.168.0.9"}, "192.168.0.9", "me", "ryzen", false},
		{"distinct-host same-machine (loopback test)", announce{ID: "node-b", Host: "hostB", IP: "192.168.0.154"}, "192.168.0.154", "node-a", "hostA", false},
		{"real peer", announce{ID: "p", Host: "laptop", IP: "192.168.0.181"}, "192.168.0.181", "me", "ryzen", false},
	}
	for _, c := range cases {
		if got := isSelfAnnounce(c.a, c.src, c.id, c.host, selfIPs); got != c.wantSelf {
			t.Errorf("%s: isSelfAnnounce = %v, want %v", c.name, got, c.wantSelf)
		}
	}
}

func TestNewAppliesDefaults(t *testing.T) {
	s := New(Config{Group: "239.255.74.76", Port: 48076})
	if s.cfg.Interval != 3*time.Second {
		t.Fatalf("default Interval = %v, want 3s", s.cfg.Interval)
	}
	if s.cfg.TTL != 12*time.Second {
		t.Fatalf("default TTL = %v, want 12s", s.cfg.TTL)
	}
}

func TestStopBeforeStartIsSafe(t *testing.T) {
	s := New(Config{Group: "239.255.74.76", Port: 48076})
	if err := s.Stop(); err != nil {
		t.Fatalf("Stop before Start should be safe, got %v", err)
	}
}

func TestPeersEmptyOnFreshService(t *testing.T) {
	s := New(Config{Group: "239.255.74.76", Port: 48076})
	if got := s.Peers(); len(got) != 0 {
		t.Fatalf("expected no peers on fresh service, got %d", len(got))
	}
}

func TestPrimaryIPNonEmpty(t *testing.T) {
	ip := primaryIP()
	if ip == "" {
		t.Skip("no outbound route available in this environment")
	}
	if net.ParseIP(ip) == nil {
		t.Fatalf("primaryIP returned a non-IP: %q", ip)
	}
}
