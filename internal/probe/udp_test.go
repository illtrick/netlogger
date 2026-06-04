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

func TestProbeUDPComputesSaneRTTAndJitter(t *testing.T) {
	echo, err := StartUDPEcho("127.0.0.1:0")
	if err != nil {
		t.Fatalf("start echo: %v", err)
	}
	defer echo.Close()

	stats, err := ProbeUDP(echo.Addr(), 8, 5*time.Millisecond, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if stats.Received != 8 {
		t.Fatalf("want 8 received, got %d", stats.Received)
	}
	// RTT is computed from the in-packet send timestamp; over loopback it must
	// be small but non-negative, and jitter (IPDV) must be non-negative.
	if stats.AvgRTT < 0 || stats.AvgRTT > 200*time.Millisecond {
		t.Fatalf("AvgRTT out of sane range: %v", stats.AvgRTT)
	}
	if stats.Jitter < 0 {
		t.Fatalf("jitter must be non-negative, got %v", stats.Jitter)
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
