package appcore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestExportBundleAndWrite(t *testing.T) {
	b := ExportBundle{
		GeneratedUnix:    100,
		NodeID:           "n1",
		Host:             "hostA",
		SessionUptimeSec: 42,
		Peers: []PeerInfo{
			{ID: "p1", Host: "h1", UDPLossPct: 1.0, DropEpisodes: 5},
		},
	}
	path, err := WriteExport(t.TempDir(), b)
	if err != nil {
		t.Fatalf("WriteExport: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var got ExportBundle
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.NodeID != "n1" || len(got.Peers) != 1 || got.Peers[0].DropEpisodes != 5 {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
	if filepath.Ext(path) != ".json" {
		t.Fatalf("expected .json file, got %s", path)
	}
}
