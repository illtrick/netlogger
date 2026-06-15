package appcore

import "testing"

func TestPeerStatRTTandLoss(t *testing.T) {
	s := &peerStat{}
	s.record(true, 1.5)
	s.record(true, 2.5)
	s.record(false, 0)
	rtt, loss := s.read()
	if rtt != 2.5 {
		t.Fatalf("expected lastRTT 2.5 (last successful), got %v", rtt)
	}
	if loss < 33.0 || loss > 34.0 {
		t.Fatalf("expected ~33.3%% loss over 3 samples, got %v", loss)
	}
}

func TestPeerStatEmpty(t *testing.T) {
	s := &peerStat{}
	rtt, loss := s.read()
	if rtt != 0 || loss != 0 {
		t.Fatalf("expected zero rtt/loss when empty, got %v/%v", rtt, loss)
	}
}

func TestPeerStatWindowBounded(t *testing.T) {
	s := &peerStat{}
	for i := 0; i < recentWindow+50; i++ {
		s.record(true, 1.0)
	}
	if got := len(s.recent); got != recentWindow {
		t.Fatalf("expected recent capped at %d, got %d", recentWindow, got)
	}
}
