package appcore

import "testing"

func TestLinkStateHysteresis(t *testing.T) {
	s := &linkState{}
	// one lossy sample: not yet degraded (needs 2 consecutive)
	if ch, _ := s.step(2.0); ch {
		t.Fatalf("should not flip on first lossy sample")
	}
	// second consecutive lossy: now degraded
	ch, deg := s.step(2.0)
	if !ch || !deg {
		t.Fatalf("expected degraded transition, ch=%v deg=%v", ch, deg)
	}
	// staying lossy: no further change
	if ch, _ := s.step(2.0); ch {
		t.Fatalf("no change while staying degraded")
	}
	// clean sample below exit threshold: recovers
	ch, deg = s.step(0.0)
	if !ch || deg {
		t.Fatalf("expected recovery, ch=%v deg=%v", ch, deg)
	}
}

func TestLinkStateIgnoresMinorLoss(t *testing.T) {
	s := &linkState{}
	if ch, _ := s.step(0.5); ch { // below enter threshold (1.0)
		t.Fatalf("0.5%% should not degrade")
	}
	if ch, _ := s.step(0.5); ch {
		t.Fatalf("still should not degrade")
	}
}
