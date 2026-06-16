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
	if evs := nicEvents(map[string]nicstat.NIC{}, cur); len(evs) != 0 {
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
	evs := nicEvents(prev, cur)
	if len(evs) != 3 {
		t.Fatalf("expected 3 events (status, speed, discards), got %d: %+v", len(evs), evs)
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
		case strings.Contains(e.detail, "discards rx+4"):
			sawDiscard = true
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
	if evs := nicEvents(prev, cur); len(evs) != 0 {
		t.Fatalf("unchanged adapter should be silent, got %+v", evs)
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
