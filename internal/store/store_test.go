package store

import (
	"path/filepath"
	"testing"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestLossBuckets(t *testing.T) {
	s := openTemp(t)
	base := int64(1_000_000_000)
	us := func(sec int) int64 { return base + int64(sec)*1_000_000 }
	mk := func(sec int, dst string, lost bool) {
		if _, err := s.Insert(Sample{TSUnixUS: us(sec), ProbeType: "udp_iso", SrcHost: "me", DstHost: dst, Lost: lost}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	// bucket 0 (sec 0–9): peerA 1 lost + 1 ok → 50%
	mk(0, "peerA", true)
	mk(1, "peerA", false)
	// bucket 1 (sec 10–19): peerA 2 ok → 0%
	mk(10, "peerA", false)
	mk(11, "peerA", false)
	// peerB only in bucket 0, all clean
	mk(2, "peerB", false)
	// a gateway icmp sample should be included; a self icmp sample must be excluded
	if _, err := s.Insert(Sample{TSUnixUS: us(0), ProbeType: "icmp", SrcHost: "me", DstHost: "__gateway__", Lost: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Insert(Sample{TSUnixUS: us(0), ProbeType: "icmp", SrcHost: "me", DstHost: "self", Lost: true}); err != nil {
		t.Fatal(err)
	}

	res, err := s.LossBuckets(us(0), us(20), 10) // two 10s buckets
	if err != nil {
		t.Fatalf("LossBuckets: %v", err)
	}
	if got := res["peerA"]; len(got) != 2 || got[0] != 50 || got[1] != 0 {
		t.Fatalf("peerA buckets wrong: %+v", got)
	}
	if got := res["peerB"]; got[0] != 0 || got[1] != -1 { // bucket 1 has no data → -1
		t.Fatalf("peerB buckets wrong: %+v", got)
	}
	if got := res["__gateway__"]; got[0] != 100 {
		t.Fatalf("gateway should be included at 100%%: %+v", got)
	}
	if _, ok := res["self"]; ok {
		t.Fatalf("self icmp must be excluded from the heatmap")
	}
}

func TestOpenEnablesWAL(t *testing.T) {
	s := openTemp(t)
	var mode string
	if err := s.DB().QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Fatalf("want wal, got %q", mode)
	}
}

func TestOpenSetsWalAutocheckpoint(t *testing.T) {
	s := openTemp(t)
	var pages int
	if err := s.DB().QueryRow("PRAGMA wal_autocheckpoint").Scan(&pages); err != nil {
		t.Fatalf("query wal_autocheckpoint: %v", err)
	}
	if pages != 1000 {
		t.Fatalf("want wal_autocheckpoint=1000, got %d", pages)
	}
}

func TestInsertAndSince(t *testing.T) {
	s := openTemp(t)
	good := Sample{TSUnixUS: 1000, ProbeType: "icmp", SrcHost: "a", DstHost: "b", Direction: "rtt", RTTus: 1500, Lost: false}
	lost := Sample{TSUnixUS: 2000, ProbeType: "icmp", SrcHost: "a", DstHost: "c", Direction: "rtt", Lost: true}
	seq1, err := s.Insert(good)
	if err != nil {
		t.Fatalf("insert good: %v", err)
	}
	if _, err := s.Insert(lost); err != nil {
		t.Fatalf("insert lost: %v", err)
	}

	rows, err := s.Since(0, 100)
	if err != nil {
		t.Fatalf("since: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
	if rows[0].RTTus != 1500 || rows[0].Lost {
		t.Fatalf("good row wrong: %+v", rows[0])
	}
	if !rows[1].Lost || rows[1].RTTus != 0 {
		t.Fatalf("lost row should have Lost=true, RTTus=0 (NULL): %+v", rows[1])
	}

	// Since(seq1) must exclude the first row.
	rows2, err := s.Since(seq1, 100)
	if err != nil {
		t.Fatalf("since seq1: %v", err)
	}
	if len(rows2) != 1 || rows2[0].DstHost != "c" {
		t.Fatalf("since(seq1) want only row c, got %+v", rows2)
	}
}
