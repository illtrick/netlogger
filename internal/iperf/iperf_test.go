package iperf

import (
	"os"
	"path/filepath"
	"testing"
)

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

func TestParseErrorField(t *testing.T) {
	_, err := Parse([]byte(`{"error":"unable to connect to server"}`))
	if err == nil {
		t.Fatal("an iperf3 error payload must surface as an error")
	}
}
