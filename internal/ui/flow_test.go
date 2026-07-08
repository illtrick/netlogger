package ui

import (
	"testing"

	"netlogger/internal/appcore"
)

func TestFlowCellsBothDirections(t *testing.T) {
	// A "both" sweep: run[A,B] and run[B,A] each measure up + down.
	cells := map[string]appcore.SpeedResult{
		flowKey("A", "B"): {UpMbit: 100, DownMbit: 90, Retransmits: 3, RTTms: 1.2},
		flowKey("B", "A"): {UpMbit: 200, DownMbit: 80},
	}
	f := flowCells(cells)
	ab := f[flowKey("A", "B")]
	if ab.Mbit != 100 { // run[A,B].up is the primary for flow A→B
		t.Fatalf("flow A→B primary = %v, want 100", ab.Mbit)
	}
	if ab.Confirm != 80 { // run[B,A].down mirrors flow A→B
		t.Fatalf("flow A→B confirm = %v, want 80", ab.Confirm)
	}
	if ab.Retr != 3 || ab.RTTms != 1.2 {
		t.Fatalf("flow A→B retr/rtt not attached: %+v", ab)
	}
	ba := f[flowKey("B", "A")]
	if ba.Mbit != 200 || ba.Confirm != 90 {
		t.Fatalf("flow B→A = %+v, want primary 200 confirm 90", ba)
	}
}

func TestFlowCellsErrorPropagation(t *testing.T) {
	// One run errors, its mirror measures both flows → no Err, real rates win.
	cells := map[string]appcore.SpeedResult{
		flowKey("A", "B"): {Err: "server busy"},
		flowKey("B", "A"): {UpMbit: 200, DownMbit: 80},
	}
	f := flowCells(cells)
	if ab := f[flowKey("A", "B")]; ab.Mbit != 80 || ab.Err != "" {
		t.Fatalf("mirror should measure flow A→B despite the error: %+v", ab)
	}
	if ba := f[flowKey("B", "A")]; ba.Mbit != 200 || ba.Err != "" {
		t.Fatalf("flow B→A should be measured: %+v", ba)
	}

	// Both runs error → both flows carry the error and no rate.
	only := map[string]appcore.SpeedResult{flowKey("A", "B"): {Err: "x"}}
	g := flowCells(only)
	if g[flowKey("A", "B")].Err != "x" || g[flowKey("B", "A")].Err != "x" {
		t.Fatalf("errored run should mark both flows: %+v", g)
	}
	if g[flowKey("A", "B")].Mbit != 0 {
		t.Fatalf("errored flow should have no rate")
	}
}

func TestAsymmetric(t *testing.T) {
	if asymmetric(100, 100) {
		t.Fatalf("equal rates are not asymmetric")
	}
	if !asymmetric(100, 70) {
		t.Fatalf("70 vs 100 should be asymmetric")
	}
	if asymmetric(100, 85) {
		t.Fatalf("85 vs 100 is within 80%% — not asymmetric")
	}
	if asymmetric(0, 50) {
		t.Fatalf("an unmeasured direction is never asymmetric")
	}
}

func TestLinkPctAndBucket(t *testing.T) {
	if got := linkPct(850, 1000, 1000); got < 0.849 || got > 0.851 {
		t.Fatalf("linkPct 850/1000 = %v, want 0.85", got)
	}
	if got := linkPct(940, 2500, 1000); got < 0.939 || got > 0.941 {
		t.Fatalf("linkPct uses the slower endpoint (1000): %v", got)
	}
	if linkPct(500, 0, 1000) != -1 {
		t.Fatalf("unknown endpoint → -1")
	}
	if pctBucket(0.9) != colGood || pctBucket(0.6) != colWatch || pctBucket(0.3) != colBad {
		t.Fatalf("pctBucket thresholds wrong")
	}
}
