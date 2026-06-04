package mesh

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"netlogger/internal/store"
)

// liveAgent spins up a real agent HTTP server backed by a store with n samples.
func liveAgent(t *testing.T, n int) *httptest.Server {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "live.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	for i := 0; i < n; i++ {
		if _, err := s.Insert(store.Sample{TSUnixUS: int64(i), ProbeType: "icmp", SrcHost: "ncase", DstHost: "ryzen", Direction: "rtt", RTTus: 800}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	api := &AgentAPI{Store: s, NodeID: "ncase", Host: "ncase"}
	mux := http.NewServeMux()
	api.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newAgg(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "agg.db"))
	if err != nil {
		t.Fatalf("open agg: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPullOnceAggregatesAndAdvancesCursor(t *testing.T) {
	srv := liveAgent(t, 3)
	agg := newAgg(t)
	p := NewPuller(agg)

	ref := AgentRef{ID: "ncase", BaseURL: srv.URL}
	got, err := p.PullOnce(ref)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if got != 3 {
		t.Fatalf("want 3 pulled, got %d", got)
	}
	if n, _ := agg.CountAgentSamples("ncase"); n != 3 {
		t.Fatalf("want 3 aggregated rows, got %d", n)
	}
	if c, _ := agg.Cursor("ncase"); c != 3 {
		t.Fatalf("want cursor 3, got %d", c)
	}
	if st := p.State("ncase"); !st.Online {
		t.Fatalf("agent should be online after a good pull")
	}
}

func TestPullIsIdempotentAcrossRepeatsAndOverlap(t *testing.T) {
	srv := liveAgent(t, 3)
	agg := newAgg(t)
	p := NewPuller(agg)
	ref := AgentRef{ID: "ncase", BaseURL: srv.URL}

	if _, err := p.PullOnce(ref); err != nil {
		t.Fatalf("pull1: %v", err)
	}
	// Second pull: cursor is 3, nothing new.
	if got, _ := p.PullOnce(ref); got != 0 {
		t.Fatalf("second pull should fetch 0, got %d", got)
	}
	// Force re-read of already-seen rows (simulate a backfill overlap).
	if err := agg.SetCursor("ncase", 1); err != nil {
		t.Fatalf("rewind cursor: %v", err)
	}
	if _, err := p.PullOnce(ref); err != nil {
		t.Fatalf("overlap pull: %v", err)
	}
	// Idempotent: still exactly 3 rows, no duplicates.
	if n, _ := agg.CountAgentSamples("ncase"); n != 3 {
		t.Fatalf("want 3 rows after overlap, got %d", n)
	}
}

func TestPullResumesAfterCoordinatorRestart(t *testing.T) {
	// A live agent we can keep writing to.
	agentStore, err := store.Open(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatalf("open agent: %v", err)
	}
	t.Cleanup(func() { agentStore.Close() })
	ins := func(ts int64) {
		if _, err := agentStore.Insert(store.Sample{TSUnixUS: ts, ProbeType: "icmp", SrcHost: "ncase", DstHost: "ryzen", RTTus: 700}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	for i := 0; i < 3; i++ {
		ins(int64(i))
	}
	api := &AgentAPI{Store: agentStore, NodeID: "ncase", Host: "ncase"}
	mux := http.NewServeMux()
	api.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	ref := AgentRef{ID: "ncase", BaseURL: srv.URL}

	aggPath := filepath.Join(t.TempDir(), "agg.db")

	// Coordinator #1 pulls the first 3, advancing the durable cursor, then exits.
	agg1, err := store.Open(aggPath)
	if err != nil {
		t.Fatalf("open agg1: %v", err)
	}
	if got, _ := NewPuller(agg1).PullOnce(ref); got != 3 {
		t.Fatalf("first pull want 3, got %d", got)
	}
	agg1.Close() // coordinator shuts down

	// Agent keeps recording while the coordinator is down.
	ins(100)
	ins(101)

	// Coordinator #2: fresh puller on the SAME agg DB (cursor read back from disk).
	agg2, err := store.Open(aggPath)
	if err != nil {
		t.Fatalf("open agg2: %v", err)
	}
	t.Cleanup(func() { agg2.Close() })
	p2 := NewPuller(agg2)
	if got, _ := p2.PullOnce(ref); got != 2 {
		t.Fatalf("resume should fetch only the 2 new rows, got %d", got)
	}
	if total, _ := agg2.CountAgentSamples("ncase"); total != 5 {
		t.Fatalf("want 5 total rows after resume, got %d (no dups)", total)
	}
	if c, _ := agg2.Cursor("ncase"); c != 5 {
		t.Fatalf("cursor should be 5, got %d", c)
	}
	if got, _ := p2.PullOnce(ref); got != 0 {
		t.Fatalf("a follow-up pull should fetch 0, got %d", got)
	}
}

func TestPullRecordsConnectivityTransitions(t *testing.T) {
	srv := liveAgent(t, 2)
	agg := newAgg(t)
	p := NewPuller(agg)
	ref := AgentRef{ID: "ncase", BaseURL: srv.URL}

	if _, err := p.PullOnce(ref); err != nil { // first success -> online event
		t.Fatalf("pull online: %v", err)
	}
	srv.Close()                  // agent goes away
	_, _ = p.PullOnce(ref)       // failure -> offline event

	evs, err := agg.ConnectivityEvents("ncase")
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(evs) != 2 || !evs[0].Online || evs[1].Online {
		t.Fatalf("want [online, offline] transitions, got %+v", evs)
	}
}

func TestPullMarksOfflineOnError(t *testing.T) {
	agg := newAgg(t)
	p := NewPuller(agg)
	// Unreachable URL.
	if _, err := p.PullOnce(AgentRef{ID: "dead", BaseURL: "http://127.0.0.1:1"}); err == nil {
		t.Fatal("expected error pulling from unreachable agent")
	}
	if st := p.State("dead"); st.Online {
		t.Fatal("agent should be marked offline after a failed pull")
	}
	if st := p.State("dead"); st.LastErr == "" {
		t.Fatal("offline state should carry LastErr")
	}
}
