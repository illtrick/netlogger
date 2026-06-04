package config

import "testing"

func TestLoadValid(t *testing.T) {
	c, err := Load("testdata/network.yaml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.Nodes) != 3 {
		t.Fatalf("want 3 nodes, got %d", len(c.Nodes))
	}
	if len(c.Links) != 2 {
		t.Fatalf("want 2 links, got %d", len(c.Links))
	}
	n, ok := c.Node("ryzen")
	if !ok {
		t.Fatal("ryzen node not found")
	}
	if n.Role != "coordinator" || n.LinkSpeed != "2.5G" {
		t.Fatalf("ryzen fields wrong: %+v", n)
	}
}

func TestValidateRejectsUnknownLinkNode(t *testing.T) {
	c := &Config{
		Nodes: []Node{{ID: "a", Type: NodeEndpoint}},
		Links: [][]string{{"a", "ghost"}},
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for link to unknown node, got nil")
	}
}

func TestValidateRejectsBadLinkArity(t *testing.T) {
	c := &Config{
		Nodes: []Node{{ID: "a", Type: NodeEndpoint}},
		Links: [][]string{{"a"}},
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for 1-element link, got nil")
	}
}
