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
