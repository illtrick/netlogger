package config

import (
	"path/filepath"
	"testing"
)

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

func TestWriteStarterCreatesLoadableConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "network.yaml")
	if err := WriteStarter(path, "ryzen", "127.0.0.1:8088"); err != nil {
		t.Fatalf("write: %v", err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("load starter: %v", err)
	}
	n, ok := c.Node("ryzen")
	if !ok || n.Role != "coordinator" || n.Address != "127.0.0.1:8088" {
		t.Fatalf("starter node wrong: %+v ok=%v", n, ok)
	}
	// Second call must not clobber an existing file.
	if err := WriteStarter(path, "other", "127.0.0.1:9999"); err != nil {
		t.Fatalf("second write: %v", err)
	}
	c2, _ := Load(path)
	if _, ok := c2.Node("ryzen"); !ok {
		t.Fatal("existing config was overwritten")
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
