package appcore

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMeshWarning(t *testing.T) {
	self := buildID{Version: "1.1.0", Build: "abc123", Platform: "windows/amd64"}

	// Identical build everywhere → no warning.
	reps := map[string]LinkReport{
		"b": {NodeID: "b", Host: "ryzen", Version: "1.1.0", Platform: "windows/amd64", Build: "abc123"},
	}
	if w := meshWarning(self, reps); w != "" {
		t.Fatalf("identical mesh should not warn: %q", w)
	}

	// THE CROSS-PLATFORM CASE: same version, different OS, different build hash
	// (a Mac joining a Windows mesh) → NOT a mismatch.
	reps["b"] = LinkReport{NodeID: "b", Host: "macbook", Version: "1.1.0", Platform: "darwin/arm64", Build: "def456"}
	if w := meshWarning(self, reps); w != "" {
		t.Fatalf("same version on another platform must not warn, got %q", w)
	}

	// A peer on an older release → version-mismatch warning naming it + the target.
	reps["b"] = LinkReport{NodeID: "b", Host: "htpc", Version: "1.0.0", Platform: "windows/amd64", Build: "old999"}
	w := meshWarning(self, reps)
	if w == "" || !strings.Contains(w, "version mismatch") || !strings.Contains(w, "htpc runs 1.0.0") || !strings.Contains(w, "Update every node to 1.1.0") {
		t.Fatalf("expected version-mismatch warning, got %q", w)
	}

	// A peer too old to report a version at all → treated as older, still warned.
	reps["b"] = LinkReport{NodeID: "b", Host: "htpc", Build: "veryold"}
	w = meshWarning(self, reps)
	if w == "" || !strings.Contains(w, "htpc runs an older build") {
		t.Fatalf("empty-version peer should warn as older, got %q", w)
	}

	// Same version, SAME platform, different commit → build-skew nudge (incomplete rollout).
	reps["b"] = LinkReport{NodeID: "b", Host: "ncase", Version: "1.1.0", Platform: "windows/amd64", Build: "stale77"}
	w = meshWarning(self, reps)
	if w == "" || !strings.Contains(w, "build skew") || !strings.Contains(w, "ncase (stale77)") {
		t.Fatalf("expected build-skew warning, got %q", w)
	}

	// Version mismatch dominates a co-occurring same-platform build skew.
	reps = map[string]LinkReport{
		"old": {NodeID: "old", Host: "htpc", Version: "1.0.0", Platform: "windows/amd64", Build: "old999"},
		"skew": {NodeID: "skew", Host: "ncase", Version: "1.1.0", Platform: "windows/amd64", Build: "stale77"},
	}
	if w := meshWarning(self, reps); !strings.Contains(w, "version mismatch") {
		t.Fatalf("version mismatch should dominate build skew, got %q", w)
	}
}

func TestRefreshSelfAddrTracksIPChange(t *testing.T) {
	ip := "192.168.0.128"
	a := &App{primaryIP: func() string { return ip }}
	a.refreshSelfAddr()
	a.mu.Lock()
	first := a.selfAddr
	a.mu.Unlock()
	if first != "192.168.0.128:8088" {
		t.Fatalf("selfAddr = %q", first)
	}
	ip = "192.168.0.127" // DHCP moved us — the next refresh must follow
	a.refreshSelfAddr()
	a.mu.Lock()
	second := a.selfAddr
	a.mu.Unlock()
	if second != "192.168.0.127:8088" {
		t.Fatalf("selfAddr stale after IP change: %q", second)
	}
	// No route at all → loopback fallback (local API only).
	a.primaryIP = func() string { return "" }
	a.refreshSelfAddr()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.selfAddr != "127.0.0.1:8088" {
		t.Fatalf("no-route fallback = %q", a.selfAddr)
	}
}

func TestReachWarning(t *testing.T) {
	self := "me"
	// Every measuring peer reports total loss toward us → warn.
	reps := map[string]LinkReport{
		"a": {NodeID: "a", Links: []LinkStat{{PeerID: self, LossPct: 100}}},
		"b": {NodeID: "b", Links: []LinkStat{{PeerID: self, LossPct: 99.5}, {PeerID: "a", LossPct: 0}}},
	}
	if w := reachWarning(self, reps); w == "" {
		t.Fatalf("all-peers-dead-toward-self should warn")
	}
	// One peer reaches us fine → no warning (their problem, not our inbound).
	reps["b"] = LinkReport{NodeID: "b", Links: []LinkStat{{PeerID: self, LossPct: 0}}}
	if w := reachWarning(self, reps); w != "" {
		t.Fatalf("a reachable path should clear the warning, got %q", w)
	}
	// Nothing measures us yet (fresh mesh) → silent.
	if w := reachWarning(self, map[string]LinkReport{"a": {NodeID: "a"}}); w != "" {
		t.Fatalf("no measurements should stay silent, got %q", w)
	}
	if w := reachWarning(self, nil); w != "" {
		t.Fatalf("no reports should stay silent, got %q", w)
	}
}

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

func TestParseLinkSpeedMbit(t *testing.T) {
	cases := map[string]int{
		"2.5 Gbps": 2500, "1 Gbps": 1000, "100 Mbps": 100,
		"10 Gbps": 10000, "": 0, "autoselect": 0,
	}
	for in, want := range cases {
		if got := parseLinkSpeedMbit(in); got != want {
			t.Errorf("parseLinkSpeedMbit(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestLinkReportCarriesLinkSpeed(t *testing.T) {
	rep := LinkReport{NodeID: "a", Host: "hostA", LinkSpeedMbit: 2500, Links: []LinkStat{{PeerID: "b"}}}
	mux := http.NewServeMux()
	mux.Handle("/api/links", linksHandler(func() LinkReport { return rep }))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	got, err := fetchLinks(http.DefaultClient, srv.URL)
	if err != nil {
		t.Fatalf("fetchLinks: %v", err)
	}
	if got.LinkSpeedMbit != 2500 {
		t.Fatalf("link speed lost in round-trip: %d", got.LinkSpeedMbit)
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
