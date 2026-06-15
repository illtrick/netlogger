package discovery

import (
	"testing"
	"time"
)

func TestTableDedupsByID(t *testing.T) {
	base := time.Unix(1000, 0)
	tbl := newTable(10*time.Second, func() time.Time { return base })
	tbl.upsert(Peer{ID: "a", Host: "h1", Addr: "10.0.0.1:8088"})
	tbl.upsert(Peer{ID: "a", Host: "h1", Addr: "10.0.0.2:8088"})
	got := tbl.list()
	if len(got) != 1 {
		t.Fatalf("expected 1 peer after dedup, got %d", len(got))
	}
	if got[0].Addr != "10.0.0.2:8088" {
		t.Fatalf("expected addr updated to newest, got %q", got[0].Addr)
	}
}

func TestTableExpiresStalePeers(t *testing.T) {
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	tbl := newTable(10*time.Second, clock)
	tbl.upsert(Peer{ID: "a", Host: "h1", Addr: "x"})
	now = now.Add(5 * time.Second)
	tbl.upsert(Peer{ID: "b", Host: "h2", Addr: "y"})
	now = now.Add(6 * time.Second)
	got := tbl.list()
	if len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("expected only fresh peer b, got %+v", got)
	}
}
