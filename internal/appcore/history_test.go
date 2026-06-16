package appcore

import "testing"

func TestHistRingCapsAndOrders(t *testing.T) {
	r := newHistRing(3)
	r.push(1)
	r.push(2)
	r.push(3)
	r.push(4)
	got := r.values()
	if len(got) != 3 || got[0] != 2 || got[2] != 4 {
		t.Fatalf("expected [2 3 4], got %v", got)
	}
}

func TestHistRingPartial(t *testing.T) {
	r := newHistRing(5)
	r.push(7)
	r.push(8)
	got := r.values()
	if len(got) != 2 || got[0] != 7 || got[1] != 8 {
		t.Fatalf("expected [7 8], got %v", got)
	}
}
