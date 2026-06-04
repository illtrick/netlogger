package correlate

import "testing"

// noOffset gives every agent a zero offset and a small fixed uncertainty.
func noOffset(string) (int64, int64) { return 0, 5 }

func TestCorrelateSimultaneousAcrossAgents(t *testing.T) {
	events := []Event{
		{AgentID: "ncase", Dst: "ryzen", StartUS: 1000, EndUS: 1100, DurationUS: 100},
		{AgentID: "nas", Dst: "ryzen", StartUS: 1050, EndUS: 1150, DurationUS: 100},
	}
	groups := Correlate(events, noOffset)
	if len(groups) != 1 {
		t.Fatalf("overlapping events should form 1 group, got %d", len(groups))
	}
	if !groups[0].Simultaneous {
		t.Fatal("group spanning 2 agents should be simultaneous (shared device)")
	}
}

func TestCorrelateIndependentWhenDisjoint(t *testing.T) {
	events := []Event{
		{AgentID: "ncase", Dst: "ryzen", StartUS: 1000, EndUS: 1100, DurationUS: 100},
		{AgentID: "nas", Dst: "ryzen", StartUS: 9000, EndUS: 9100, DurationUS: 100},
	}
	groups := Correlate(events, noOffset)
	if len(groups) != 2 {
		t.Fatalf("disjoint events should be 2 groups, got %d", len(groups))
	}
	for _, g := range groups {
		if g.Simultaneous {
			t.Fatal("single-agent group must not be simultaneous")
		}
	}
}

// zeroOffset gives every agent a zero offset and zero uncertainty, so intervals
// equal [Start,End] exactly — lets us control overlap precisely.
func zeroOffset(string) (int64, int64) { return 0, 0 }

func TestCorrelateChainBridgedByDistinctAgentIsSimultaneous(t *testing.T) {
	// A(ncase) and C(projectorpc) do NOT overlap, but B(nas) overlaps both —
	// so two distinct agents ARE concurrent at two instants. Peak = 2.
	events := []Event{
		{AgentID: "ncase", StartUS: 1000, EndUS: 1100},
		{AgentID: "nas", StartUS: 1050, EndUS: 3000},
		{AgentID: "projectorpc", StartUS: 2900, EndUS: 3000},
	}
	groups := Correlate(events, zeroOffset)
	if len(groups) != 1 {
		t.Fatalf("want 1 connected group, got %d", len(groups))
	}
	if !groups[0].Simultaneous || groups[0].PeakAgents != 2 {
		t.Fatalf("want simultaneous with peak 2, got %+v", groups[0])
	}
}

func TestCorrelateSameAgentChainNotSimultaneous(t *testing.T) {
	// A chain of overlapping events all from ONE agent: never two distinct
	// agents concurrent, so NOT a shared-device (simultaneous) verdict.
	events := []Event{
		{AgentID: "ncase", StartUS: 1000, EndUS: 1100},
		{AgentID: "ncase", StartUS: 1050, EndUS: 1200},
		{AgentID: "ncase", StartUS: 1150, EndUS: 1300},
	}
	groups := Correlate(events, zeroOffset)
	if len(groups) != 1 {
		t.Fatalf("want 1 group, got %d", len(groups))
	}
	if groups[0].Simultaneous || groups[0].PeakAgents != 1 {
		t.Fatalf("single-agent chain must not be simultaneous: %+v", groups[0])
	}
}

func TestCorrelateEmptyReturnsNonNilSlice(t *testing.T) {
	groups := Correlate(nil, noOffset)
	if groups == nil {
		t.Fatal("Correlate(nil) must return a non-nil (empty) slice so JSON is [] not null")
	}
	if len(groups) != 0 {
		t.Fatalf("want 0 groups, got %d", len(groups))
	}
}

func TestCorrelateOffsetAlignsClocks(t *testing.T) {
	// nas clock is +2000us ahead; after correction the events line up.
	off := func(id string) (int64, int64) {
		if id == "nas" {
			return 2000, 50
		}
		return 0, 50
	}
	events := []Event{
		{AgentID: "ncase", Dst: "ryzen", StartUS: 1000, EndUS: 1100},
		{AgentID: "nas", Dst: "ryzen", StartUS: 3000, EndUS: 3100}, // local; -2000 => 1000
	}
	groups := Correlate(events, off)
	if len(groups) != 1 || !groups[0].Simultaneous {
		t.Fatalf("offset-corrected events should correlate: %+v", groups)
	}
}
