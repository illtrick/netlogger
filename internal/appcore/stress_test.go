package appcore

import "testing"

func TestMeshTargets(t *testing.T) {
	self := PeerInfo{ID: "self", Host: "ryzen", Addr: "10.0.0.1:8088"}
	peers := []PeerInfo{
		{ID: "p", Host: "proj", Addr: "10.0.0.2:8088"},
		{ID: "s", Host: "sarah", Addr: "10.0.0.3:8088"},
	}
	m := meshTargets(self, peers)
	if len(m["self"]) != 2 || len(m["p"]) != 2 || len(m["s"]) != 2 {
		t.Fatalf("each node should target 2 others: %+v", m)
	}
	for _, ts := range m {
		for _, tg := range ts {
			if tg == "10.0.0.1:8088" {
				t.Fatalf("target still has control port: %v", tg)
			}
		}
	}
}

func TestStressAbortPredicate(t *testing.T) {
	if shouldAbort(2) {
		t.Fatalf("2 consecutive errors should not abort")
	}
	if !shouldAbort(3) {
		t.Fatalf("3 consecutive errors should abort")
	}
}

func TestStressStartDelay(t *testing.T) {
	if d := startDelay(1000, 1500); d != 500 {
		t.Fatalf("future delay = %d us, want 500", d)
	}
	if d := startDelay(2000, 1500); d != 0 {
		t.Fatalf("past delay should clamp to 0, got %d", d)
	}
}
