package iperf

import (
	"os"
	"path/filepath"
	"testing"
)

// TestVersionConsistentWithAvailable guards the contract that the readiness
// check (Version) and the runner (Available/binary) agree about whether iperf3
// exists — they must use the same resolution (co-located preferred over PATH).
func TestVersionConsistentWithAvailable(t *testing.T) {
	if Available() != (Version() != "") {
		t.Fatalf("Available()=%v but Version()=%q — detection paths disagree", Available(), Version())
	}
}

func TestParseTCP(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "tcp.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	res, err := Parse(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(res.Intervals) != 2 {
		t.Fatalf("want 2 intervals, got %d", len(res.Intervals))
	}
	if res.Intervals[1].Retransmits != 87 || res.Intervals[1].RTTus != 38000 {
		t.Fatalf("interval 2 fields wrong: %+v", res.Intervals[1])
	}
	if res.SumRetransmits != 87 {
		t.Fatalf("want 87 total retransmits, got %d", res.SumRetransmits)
	}
	if res.Intervals[1].BitsPerSecond >= res.Intervals[0].BitsPerSecond {
		t.Fatal("expected a throughput drop between intervals")
	}
}

func TestParseUDP(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "udp.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	res, err := Parse(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.UDPLostPercent != 11.6 || res.UDPJitterMs != 14.2 {
		t.Fatalf("UDP summary wrong: lost=%v jitter=%v", res.UDPLostPercent, res.UDPJitterMs)
	}
	if len(res.Intervals) != 2 {
		t.Fatalf("want 2 intervals, got %d", len(res.Intervals))
	}
	if res.Intervals[1].LostPercent != 11.6 || res.Intervals[1].JitterMs != 14.2 {
		t.Fatalf("per-interval UDP fields wrong: %+v", res.Intervals[1])
	}
}

func TestParseErrorField(t *testing.T) {
	_, err := Parse([]byte(`{"error":"unable to connect to server"}`))
	if err == nil {
		t.Fatal("an iperf3 error payload must surface as an error")
	}
}
