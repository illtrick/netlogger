package score

import (
	"testing"

	"netlogger/internal/config"
)

// chain: ryzen - switch2 - switch1 - ncase  (a small linear topology)
func chainConfig() *config.Config {
	return &config.Config{
		Nodes: []config.Node{
			{ID: "ryzen", Type: config.NodeEndpoint, Label: "Ryzen", Address: "127.0.0.1:1"},
			{ID: "switch2", Type: config.NodeSwitch, Label: "Switch 2"},
			{ID: "switch1", Type: config.NodeSwitch, Label: "Switch 1"},
			{ID: "ncase", Type: config.NodeEndpoint, Label: "NCASE", Address: "127.0.0.1:2"},
		},
		Links: [][]string{{"ryzen", "switch2"}, {"switch2", "switch1"}, {"switch1", "ncase"}},
	}
}

func find(cs []Component, id string) Component {
	for _, c := range cs {
		if c.ID == id {
			return c
		}
	}
	return Component{}
}

func TestPathBetweenEndpoints(t *testing.T) {
	g := buildGraph(chainConfig())
	path := g.path("ryzen", "ncase")
	want := []string{"ryzen", "switch2", "switch1", "ncase"}
	if len(path) != len(want) {
		t.Fatalf("path %v != %v", path, want)
	}
	for i := range want {
		if path[i] != want[i] {
			t.Fatalf("path %v != %v", path, want)
		}
	}
}

func TestScoreSharedSwitchPoorWhenPathFails(t *testing.T) {
	cfg := chainConfig()
	tested := map[string]bool{key("ryzen", "ncase"): true}
	failing := map[string]bool{key("ryzen", "ncase"): true}
	cs := Score(cfg, tested, failing)

	if h := find(cs, "switch1").Health; h != "poor" {
		t.Fatalf("switch1 should be poor, got %q", h)
	}
	if h := find(cs, "switch2").Health; h != "poor" {
		t.Fatalf("switch2 should be poor, got %q", h)
	}
}

func TestScoreCleanPathIsGoodAndUntestedIsUntested(t *testing.T) {
	cfg := chainConfig()
	tested := map[string]bool{key("ryzen", "ncase"): true}
	failing := map[string]bool{} // none failing
	cs := Score(cfg, tested, failing)

	if h := find(cs, "switch1").Health; h != "good" {
		t.Fatalf("switch1 on a clean tested path should be good, got %q", h)
	}
	cfg.Nodes = append(cfg.Nodes, config.Node{ID: "lonely", Type: config.NodeEndpoint})
	cs = Score(cfg, tested, failing)
	if h := find(cs, "lonely").Health; h != "untested" {
		t.Fatalf("lonely node should be untested, got %q", h)
	}
}

// star: four endpoints (e1..e4) all hang off a single hub switch, so all 6
// endpoint pairs cross the hub.
func starConfig() *config.Config {
	c := &config.Config{Nodes: []config.Node{{ID: "hub", Type: config.NodeSwitch, Label: "Hub"}}}
	for _, id := range []string{"e1", "e2", "e3", "e4"} {
		c.Nodes = append(c.Nodes, config.Node{ID: id, Type: config.NodeEndpoint, Label: id, Address: "127.0.0.1:1"})
		c.Links = append(c.Links, []string{id, "hub"})
	}
	return c
}

func TestScoreFairAndCoverageTiers(t *testing.T) {
	cfg := starConfig()
	pairs := []string{
		key("e1", "e2"), key("e1", "e3"), key("e1", "e4"),
		key("e2", "e3"), key("e2", "e4"), key("e3", "e4"),
	}
	tested := map[string]bool{}
	for _, p := range pairs {
		tested[p] = true
	}
	failing := map[string]bool{key("e1", "e2"): true, key("e1", "e3"): true}

	cs := Score(cfg, tested, failing)

	hub := find(cs, "hub")
	if hub.Health != "fair" { // 2 of 6 crossing paths fail -> mixed -> fair
		t.Fatalf("hub should be fair, got %q (%+v)", hub.Health, hub)
	}
	if hub.Coverage != "thorough" { // 6 tested paths cross it
		t.Fatalf("hub coverage should be thorough, got %q", hub.Coverage)
	}
	// e4 is on 3 tested paths, none failing -> good / partial.
	e4 := find(cs, "e4")
	if e4.Health != "good" || e4.Coverage != "partial" {
		t.Fatalf("e4 should be good/partial, got %s/%s", e4.Health, e4.Coverage)
	}
}
