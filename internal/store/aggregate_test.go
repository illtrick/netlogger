package store

import (
	"path/filepath"
	"testing"
)

func TestUpsertIsIdempotent(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "agg.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	sm := Sample{Seq: 7, TSUnixUS: 100, ProbeType: "icmp", SrcHost: "ncase", DstHost: "ryzen", Direction: "rtt", RTTus: 900}

	if err := s.Upsert("ncase", sm); err != nil {
		t.Fatalf("upsert 1: %v", err)
	}
	// Same (agent_id, seq) again -> must be a no-op, not an error or a dup.
	if err := s.Upsert("ncase", sm); err != nil {
		t.Fatalf("upsert 2: %v", err)
	}
	n, err := s.CountAgentSamples("ncase")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 row after duplicate upsert, got %d", n)
	}
}

func TestCursorRoundTrip(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "cur.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	// Unknown agent -> cursor 0.
	got, err := s.Cursor("ncase")
	if err != nil {
		t.Fatalf("cursor: %v", err)
	}
	if got != 0 {
		t.Fatalf("want 0 for new agent, got %d", got)
	}
	if err := s.SetCursor("ncase", 42); err != nil {
		t.Fatalf("set: %v", err)
	}
	if got, _ := s.Cursor("ncase"); got != 42 {
		t.Fatalf("want 42, got %d", got)
	}
	// Overwrites.
	if err := s.SetCursor("ncase", 99); err != nil {
		t.Fatalf("set2: %v", err)
	}
	if got, _ := s.Cursor("ncase"); got != 99 {
		t.Fatalf("want 99, got %d", got)
	}
}

func TestAgentSamplesAll(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "all.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	for i := 1; i <= 3; i++ {
		_ = s.Upsert("ncase", Sample{Seq: int64(i), TSUnixUS: int64(i * 10), ProbeType: "icmp", SrcHost: "ncase", DstHost: "ryzen", Lost: i == 2})
	}
	rows, err := s.AgentSamplesAll("ncase")
	if err != nil {
		t.Fatalf("all: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("want 3, got %d", len(rows))
	}
	if !rows[1].Lost || rows[1].Seq != 2 {
		t.Fatalf("row 2 should be the lost one: %+v", rows[1])
	}
}

func TestConnectivityEvents(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "conn.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	if err := s.InsertConnectivityEvent(1000, "nas", false, "timeout"); err != nil {
		t.Fatalf("insert offline: %v", err)
	}
	if err := s.InsertConnectivityEvent(2000, "nas", true, ""); err != nil {
		t.Fatalf("insert online: %v", err)
	}
	evs, err := s.ConnectivityEvents("nas")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(evs) != 2 || evs[0].Online || !evs[1].Online {
		t.Fatalf("connectivity events wrong: %+v", evs)
	}
	if evs[0].Detail != "timeout" {
		t.Fatalf("offline detail lost: %+v", evs[0])
	}
}
