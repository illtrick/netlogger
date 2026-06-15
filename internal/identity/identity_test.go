package identity

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNodeIDCreatesAndPersists(t *testing.T) {
	dir := t.TempDir()
	id1, err := NodeID(dir)
	if err != nil {
		t.Fatalf("NodeID: %v", err)
	}
	if len(id1) < 16 {
		t.Fatalf("expected a UUID-like id, got %q", id1)
	}
	if _, err := os.Stat(filepath.Join(dir, "node-id")); err != nil {
		t.Fatalf("expected node-id file: %v", err)
	}
	id2, err := NodeID(dir)
	if err != nil {
		t.Fatalf("NodeID 2nd: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("id not stable: %q vs %q", id1, id2)
	}
}
