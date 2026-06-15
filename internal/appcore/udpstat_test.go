package appcore

import (
	"testing"
	"time"

	"netlogger/internal/probe"
)

func TestUDPStatRecordsLatestAndCountsEpisodes(t *testing.T) {
	s := &udpStat{}
	s.record(probe.UDPStats{Received: 200, Sent: 200, LossPct: 0, AvgRTT: 800 * time.Microsecond, Jitter: 120 * time.Microsecond})
	s.record(probe.UDPStats{Received: 197, Sent: 200, LossPct: 1.5, AvgRTT: 2 * time.Millisecond, Jitter: 600 * time.Microsecond})

	rtt, jitter, loss, episodes := s.read()
	if rtt != 2.0 {
		t.Fatalf("rtt = %v, want 2.0", rtt)
	}
	if jitter < 0.59 || jitter > 0.61 {
		t.Fatalf("jitter = %v, want ~0.6", jitter)
	}
	if loss != 1.5 {
		t.Fatalf("loss = %v, want 1.5", loss)
	}
	if episodes != 1 {
		t.Fatalf("episodes = %d, want 1", episodes)
	}
}

func TestUDPStatEmpty(t *testing.T) {
	s := &udpStat{}
	rtt, jitter, loss, episodes := s.read()
	if rtt != 0 || jitter != 0 || loss != 0 || episodes != 0 {
		t.Fatalf("expected zeroes, got %v/%v/%v/%d", rtt, jitter, loss, episodes)
	}
}
