package appcore

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAssembleMatrixCombinesAllReports(t *testing.T) {
	own := LinkReport{NodeID: "a", Host: "hostA", Links: []LinkStat{
		{PeerID: "b", RTTms: 1.0, JitterMs: 0.2, LossPct: 0, Drops: 0},
	}}
	peer := LinkReport{NodeID: "b", Host: "hostB", Links: []LinkStat{
		{PeerID: "a", RTTms: 1.1, JitterMs: 0.3, LossPct: 2.0, Drops: 5},
	}}
	m := assembleMatrix(own, map[string]LinkReport{"b": peer})

	if len(m.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d: %+v", len(m.Nodes), m.Nodes)
	}
	ab, ok := m.Cell("a", "b")
	if !ok || ab.LossPct != 0 || ab.RTTms != 1.0 {
		t.Fatalf("a->b cell wrong: %+v ok=%v", ab, ok)
	}
	ba, ok := m.Cell("b", "a")
	if !ok || ba.LossPct != 2.0 || ba.Drops != 5 {
		t.Fatalf("b->a cell wrong: %+v ok=%v", ba, ok)
	}
	if _, ok := m.Cell("a", "a"); ok {
		t.Fatalf("diagonal a->a should have no cell")
	}
}

func TestAssembleMatrixNodesSortedByHost(t *testing.T) {
	own := LinkReport{NodeID: "z", Host: "zebra"}
	peers := map[string]LinkReport{
		"m": {NodeID: "m", Host: "alpha"},
		"n": {NodeID: "n", Host: "mike"},
	}
	m := assembleMatrix(own, peers)
	if m.Nodes[0].Host != "alpha" || m.Nodes[1].Host != "mike" || m.Nodes[2].Host != "zebra" {
		t.Fatalf("nodes not sorted by host: %+v", m.Nodes)
	}
}

func TestLinksHandlerServesReport(t *testing.T) {
	rep := LinkReport{NodeID: "a", Host: "hostA", Links: []LinkStat{{PeerID: "b", RTTms: 1}}}
	mux := http.NewServeMux()
	mux.Handle("/api/links", linksHandler(func() LinkReport { return rep }))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	got, err := fetchLinks(http.DefaultClient, srv.URL)
	if err != nil {
		t.Fatalf("fetchLinks: %v", err)
	}
	if got.NodeID != "a" || len(got.Links) != 1 || got.Links[0].PeerID != "b" {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
}

func TestFetchLinksErrorsOnBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()
	if _, err := fetchLinks(http.DefaultClient, srv.URL); err == nil {
		t.Fatalf("expected error on 500")
	}
}
