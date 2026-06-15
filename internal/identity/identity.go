// Package identity provides a stable per-machine node id, persisted in the data
// dir so it survives restarts (discovery dedups peers by this id, not by IP).
package identity

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// NodeID returns the persisted node id from <dir>/node-id, creating it on first
// call.
func NodeID(dir string) (string, error) {
	path := filepath.Join(dir, "node-id")
	if b, err := os.ReadFile(path); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return s, nil
		}
	}
	id := uuid.NewString()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir for node-id: %w", err)
	}
	if err := os.WriteFile(path, []byte(id), 0o644); err != nil {
		return "", fmt.Errorf("write node-id: %w", err)
	}
	return id, nil
}
