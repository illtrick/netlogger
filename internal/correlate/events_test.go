package correlate

import (
	"testing"

	"netlogger/internal/store"
)

func sample(seq, ts int64, dst string, lost bool) store.Sample {
	return store.Sample{Seq: seq, TSUnixUS: ts, ProbeType: "icmp", SrcHost: "ncase", DstHost: dst, Lost: lost}
}

func TestDetectEventsMergesConsecutiveLosses(t *testing.T) {
	samples := []store.Sample{
		sample(1, 100, "ryzen", false),
		sample(2, 200, "ryzen", true), // event A start
		sample(3, 300, "ryzen", true), // event A continues
		sample(4, 400, "ryzen", false),
		sample(5, 500, "ryzen", true), // event B (single)
		sample(6, 600, "ryzen", false),
	}
	ev := DetectEvents("ncase", samples)
	if len(ev) != 2 {
		t.Fatalf("want 2 events, got %d (%+v)", len(ev), ev)
	}
	if ev[0].StartUS != 200 || ev[0].EndUS != 300 || ev[0].DurationUS != 100 {
		t.Fatalf("event A bounds wrong: %+v", ev[0])
	}
	if ev[1].StartUS != 500 || ev[1].EndUS != 500 {
		t.Fatalf("event B bounds wrong: %+v", ev[1])
	}
}

func TestDetectEventsAllLostIsOneEventToEnd(t *testing.T) {
	samples := []store.Sample{
		sample(1, 100, "ryzen", true),
		sample(2, 200, "ryzen", true),
		sample(3, 300, "ryzen", true),
	}
	ev := DetectEvents("ncase", samples)
	if len(ev) != 1 {
		t.Fatalf("an all-lost series is one open-ended event, got %d", len(ev))
	}
	if ev[0].StartUS != 100 || ev[0].EndUS != 300 || ev[0].DurationUS != 200 {
		t.Fatalf("all-lost event bounds wrong: %+v", ev[0])
	}
}

func TestDetectEventsSeparatesPathsByDst(t *testing.T) {
	samples := []store.Sample{
		sample(1, 100, "ryzen", true),
		sample(2, 110, "nas", true),
	}
	ev := DetectEvents("ncase", samples)
	if len(ev) != 2 {
		t.Fatalf("want 2 events (one per dst), got %d", len(ev))
	}
}
