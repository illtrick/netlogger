package config

import "testing"

func sampleConfig() *Config {
	return &Config{
		Nodes: []Node{
			{ID: "ryzen", Type: NodeEndpoint, Label: "Ryzen", Address: "127.0.0.1:8088", Role: "coordinator"},
			{ID: "ncase", Type: NodeEndpoint, Label: "NCASE", Address: "127.0.0.1:8089"},
			{ID: "switch1", Type: NodeSwitch, Label: "Switch 1"}, // no address -> not a probe/pull target
		},
	}
}

func TestResolveReturnsSelfAndPeers(t *testing.T) {
	c := sampleConfig()
	self, peers, err := c.Resolve("ryzen")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if self.ID != "ryzen" {
		t.Fatalf("self wrong: %+v", self)
	}
	if len(peers) != 1 || peers[0].ID != "ncase" || peers[0].Address != "127.0.0.1:8089" {
		t.Fatalf("peers wrong: %+v", peers)
	}
}

func TestResolveUnknownNode(t *testing.T) {
	if _, _, err := sampleConfig().Resolve("ghost"); err == nil {
		t.Fatal("expected error for unknown node id")
	}
}

func TestTargetRefHelpers(t *testing.T) {
	tr := TargetRef{ID: "ncase", Address: "127.0.0.1:8089"}
	if tr.BaseURL() != "http://127.0.0.1:8089" {
		t.Fatalf("BaseURL wrong: %s", tr.BaseURL())
	}
	if tr.ProbeHost() != "127.0.0.1" {
		t.Fatalf("ProbeHost wrong: %s", tr.ProbeHost())
	}
}

func TestAddressedNodesIncludesAllWithAddress(t *testing.T) {
	got := sampleConfig().AddressedNodes()
	if len(got) != 2 {
		t.Fatalf("want 2 addressed nodes, got %d (%+v)", len(got), got)
	}
}
