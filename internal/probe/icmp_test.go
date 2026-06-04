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
