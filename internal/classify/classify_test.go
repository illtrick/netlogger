package classify

import "testing"

func TestBufferbloatSmoothRampNoLoss(t *testing.T) {
	// latency ramps smoothly far above baseline, then no loss => bufferbloat
	got := BufferbloatVsFault(2.0, []float64{5, 20, 60, 110, 140}, false)
	if got != "bufferbloat" {
		t.Fatalf("want bufferbloat, got %q", got)
	}
}

func TestFaultAbruptLoss(t *testing.T) {
	// latency stays low but there is loss => hardware/link fault
	got := BufferbloatVsFault(2.0, []float64{2, 3, 2, 3, 2}, true)
	if got != "fault" {
		t.Fatalf("want fault, got %q", got)
	}
}

func TestInconclusiveWhenQuiet(t *testing.T) {
	got := BufferbloatVsFault(2.0, []float64{2, 3, 2}, false)
	if got != "inconclusive" {
		t.Fatalf("want inconclusive, got %q", got)
	}
}

func TestLANvsWAN(t *testing.T) {
	if LANvsWAN(true, true) != "lan" {
		t.Fatal("gateway failure => LAN-side")
	}
	if LANvsWAN(false, true) != "wan" {
		t.Fatal("only external failure => WAN-side")
	}
	if LANvsWAN(false, false) != "unknown" {
		t.Fatal("no reference failure => unknown")
	}
}
