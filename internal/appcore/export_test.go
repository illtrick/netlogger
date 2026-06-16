package appcore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"netlogger/internal/discovery"
	"netlogger/internal/nicstat"
	"netlogger/internal/probe"
	"netlogger/internal/version"
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

// The export must be self-sufficient for cross-machine analysis: it carries this
// machine's build, a skew warning when a peer differs, the peer's build, and the
// merged mesh-wide event timeline (host-tagged), not just local store rows.
func TestExportCarriesBuildAndMeshEvents(t *testing.T) {
	dir := t.TempDir()
	a := New(dir)
	a.CollectNICs = func() []nicstat.NIC { return nil }
	a.Ping = func(string, time.Duration) (probe.Result, error) { return probe.Result{RTT: time.Millisecond}, nil }
	a.StartIperf = func(string) (func(), string) { return func() {}, "" }
	a.FetchLinks = func(string) (LinkReport, error) {
		return LinkReport{NodeID: "peer1", Host: "ProjectorPC", Build: "peerbuild9"}, nil
	}
	a.FetchEvents = func(string) ([]EventInfo, error) {
		return []EventInfo{{UnixMicro: 123, Online: false, Detail: "link down"}}, nil
	}
	a.Discovery = fakeLister{peers: []discovery.Peer{{ID: "peer1", Host: "ProjectorPC", Addr: "127.0.0.1:1"}}}
	a.tick = 5 * time.Millisecond
	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for {
		b := a.Export(999)
		peerBuild := len(b.Peers) == 1 && b.Peers[0].Build == "peerbuild9"
		meshTagged := false
		for _, e := range b.MeshEvents {
			if e.Host == "ProjectorPC" && e.Detail == "link down" {
				meshTagged = true
			}
		}
		if b.Build == version.Build && b.BuildWarning != "" && peerBuild && meshTagged {
			break
		}
		if time.Now().After(deadline) {
			b := a.Export(999)
			t.Fatalf("export not self-sufficient: build=%q warn=%q peers=%+v mesh=%+v",
				b.Build, b.BuildWarning, b.Peers, b.MeshEvents)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
