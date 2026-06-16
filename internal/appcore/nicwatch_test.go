package appcore

import (
	"strings"
	"testing"

	"netlogger/internal/nicstat"
)

func TestNICEventsFirstPollSilent(t *testing.T) {
	// No prior poll → no events, even with nonzero counters (they are lifetime
	// totals, not a delta).
	cur := []nicstat.NIC{{Name: "Ethernet", Status: "Up", LinkSpeed: "2.5 Gbps", RxDiscards: 999}}
	if evs := nicEvents(map[string]nicstat.NIC{}, cur, map[string]bool{}); len(evs) != 0 {
		t.Fatalf("first poll should be silent, got %+v", evs)
	}
}

func TestNICEventsDetectsChanges(t *testing.T) {
	prev := map[string]nicstat.NIC{
		"Ethernet": {Name: "Ethernet", Status: "Up", LinkSpeed: "2.5 Gbps", RxDiscards: 10, TxErrors: 5},
	}
	cur := []nicstat.NIC{
		{Name: "Ethernet", Status: "Disconnected", LinkSpeed: "1 Gbps", RxDiscards: 14, TxErrors: 5},
	}
	evs := nicEvents(prev, cur, map[string]bool{})
	if len(evs) != 3 {
		t.Fatalf("expected 3 events (status, speed, discards-began), got %d: %+v", len(evs), evs)
	}
	var sawStatus, sawSpeed, sawDiscard bool
	for _, e := range evs {
		switch {
		case strings.Contains(e.detail, "link Disconnected"):
			sawStatus = true
			if e.online {
				t.Fatalf("status-down event should be offline: %+v", e)
			}
		case strings.Contains(e.detail, "link speed 2.5 Gbps → 1 Gbps"):
			sawSpeed = true
		case strings.Contains(e.detail, "discards/errors began (rx+4"):
			sawDiscard = true
			if e.online {
				t.Fatalf("discard-begin event should be offline (degradation): %+v", e)
			}
		}
	}
	if !sawStatus || !sawSpeed || !sawDiscard {
		t.Fatalf("missing an event kind: status=%v speed=%v discard=%v (%+v)", sawStatus, sawSpeed, sawDiscard, evs)
	}
}

func TestNICEventsSteadyStateSilent(t *testing.T) {
	prev := map[string]nicstat.NIC{
		"Ethernet": {Name: "Ethernet", Status: "Up", LinkSpeed: "2.5 Gbps", RxDiscards: 10},
	}
	cur := []nicstat.NIC{{Name: "Ethernet", Status: "Up", LinkSpeed: "2.5 Gbps", RxDiscards: 10}}
	if evs := nicEvents(prev, cur, map[string]bool{}); len(evs) != 0 {
		t.Fatalf("unchanged adapter should be silent, got %+v", evs)
	}
}

// A sustained discard stream must fire exactly one "began" and one "cleared"
// event across the whole episode — not one per poll (ring-flood guard).
func TestNICEventsDiscardEdgeTriggered(t *testing.T) {
	name := "Ethernet"
	mk := func(rxd int64) []nicstat.NIC {
		return []nicstat.NIC{{Name: name, Status: "Up", LinkSpeed: "2.5 Gbps", RxDiscards: rxd}}
	}
	prev := map[string]nicstat.NIC{name: {Name: name, Status: "Up", LinkSpeed: "2.5 Gbps", RxDiscards: 0}}
	discarding := map[string]bool{}

	// poll 1: 0 → 5, rising edge → one "began"
	cur := mk(5)
	evs := nicEvents(prev, cur, discarding)
	if len(evs) != 1 || !strings.Contains(evs[0].detail, "began") || !discarding[name] {
		t.Fatalf("rising edge: want 1 began + state set, got %+v (state=%v)", evs, discarding[name])
	}
	prev[name] = cur[0]

	// poll 2: 5 → 11, still lossy → SILENT (no repeat)
	cur = mk(11)
	if evs := nicEvents(prev, cur, discarding); len(evs) != 0 {
		t.Fatalf("sustained discards should be silent, got %+v", evs)
	}
	prev[name] = cur[0]

	// poll 3: 11 → 11, delta 0 → falling edge → one "cleared"
	cur = mk(11)
	evs = nicEvents(prev, cur, discarding)
	if len(evs) != 1 || !strings.Contains(evs[0].detail, "cleared") || discarding[name] {
		t.Fatalf("falling edge: want 1 cleared + state reset, got %+v (state=%v)", evs, discarding[name])
	}
}

func TestRecordEventRingBounds(t *testing.T) {
	a := New(t.TempDir()) // store nil (not started) → recordEvent only touches the ring
	for i := 0; i < eventRingCap+25; i++ {
		a.recordEvent(true, "e")
	}
	got := a.recentEvents()
	if len(got) != eventRingCap {
		t.Fatalf("ring should cap at %d, got %d", eventRingCap, len(got))
	}
}
