package appcore

import (
	"path/filepath"
	"testing"

	"netlogger/internal/store"
)

func TestSweepSummarySlowestPair(t *testing.T) {
	nodes := []SpeedNode{{ID: "a", Host: "ryzen"}, {ID: "b", Host: "nas"}, {ID: "c", Host: "proj"}}
	cells := map[string]SpeedResult{
		speedKey("a", "b"): {DownMbit: 235, UpMbit: 240},
		speedKey("a", "c"): {DownMbit: 941, UpMbit: 887},
		speedKey("b", "c"): {Err: "server busy"}, // failed pairs don't count
	}
	sum, ok := sweepSummary(nodes, cells, SpeedReq{Direction: "both", DurationS: 10, Streams: 4})
	if !ok {
		t.Fatalf("expected a summary")
	}
	if sum.Label != "2/3 pairs" {
		t.Fatalf("label = %q, want 2/3 pairs", sum.Label)
	}
	if sum.DownMbit != 235 || sum.Detail != "slowest ryzen → nas · both · 10s · 4 streams" {
		t.Fatalf("slowest = %v %q", sum.DownMbit, sum.Detail)
	}
}

func TestSweepSummaryNothingSucceeded(t *testing.T) {
	if _, ok := sweepSummary(nil, map[string]SpeedResult{"a\x00b": {Err: "x"}}, SpeedReq{}); ok {
		t.Fatalf("all-failed sweep should not record")
	}
}

func TestSweepConfigLine(t *testing.T) {
	if got := SweepConfigLine(SpeedReq{}); got != "both · 0s · 1 streams" {
		t.Fatalf("defaults = %q", got)
	}
	if got := SweepConfigLine(SpeedReq{Direction: "bidir", DurationS: 30, Streams: 8}); got != "bidir · 30s · 8 streams" {
		t.Fatalf("bidir = %q", got)
	}
	if got := SweepConfigLine(SpeedReq{Direction: "down", DurationS: 10, Streams: 1}); got != "down · 10s · 1 streams" {
		t.Fatalf("down = %q", got)
	}
}

func TestTestResultRoundTrip(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	for i, r := range []store.TestResult{
		{TSUnixUS: 100, Kind: "internet", Label: "LibreSpeed · LA", DownMbit: 581, UpMbit: 332, Detail: "A"},
		{TSUnixUS: 200, Kind: "internet", Label: "LibreSpeed · LA", DownMbit: 600, UpMbit: 340, Detail: "B"},
		{TSUnixUS: 150, Kind: "sweep", Label: "6/6 pairs", DownMbit: 235, UpMbit: 240, Detail: "slowest a → b"},
	} {
		if err := st.InsertTestResult(r); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	net, err := st.TestResults("internet", 10)
	if err != nil || len(net) != 2 {
		t.Fatalf("internet rows = %d (%v), want 2", len(net), err)
	}
	if net[0].TSUnixUS != 200 { // newest first
		t.Fatalf("order wrong: first ts = %d", net[0].TSUnixUS)
	}
	sweep, _ := st.TestResults("sweep", 10)
	if len(sweep) != 1 || sweep[0].Detail != "slowest a → b" {
		t.Fatalf("sweep row wrong: %+v", sweep)
	}
}
