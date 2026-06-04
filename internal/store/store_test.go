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
