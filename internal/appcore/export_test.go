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
	"netlogger/internal/store"
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

func TestExportIncludesStressHistory(t *testing.T) {
	dir := t.TempDir()
	a := New(dir)
	st, err := store.Open(filepath.Join(dir, "e.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	a.store = st
	a.RecordStressRun(600, 6, 200, "tcp", "ryzen", 38, 0)
	b := a.Export(100)
	if len(b.StressTests) != 1 || b.StressTests[0].Kind != "stress" {
		t.Fatalf("export missing stress history: %+v", b.StressTests)
	}
}

func TestLossHeatLabelsAndOrders(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir + "/h.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	base := int64(2_000_000_000) // µs
	us := func(sec int) int64 { return base + int64(sec)*1_000_000 }
	// peer "peerX" lossy in bucket 0; gateway clean
	_, _ = st.Insert(store.Sample{TSUnixUS: us(0), ProbeType: "udp_iso", DstHost: "peerX", Lost: true})
	_, _ = st.Insert(store.Sample{TSUnixUS: us(1), ProbeType: "udp_iso", DstHost: "peerX", Lost: false})
	_, _ = st.Insert(store.Sample{TSUnixUS: us(0), ProbeType: "icmp", DstHost: "__gateway__", Lost: false})

	a := New(dir)
	a.store = st
	a.Discovery = fakeLister{peers: []discovery.Peer{{ID: "peerX", Host: "ProjectorPC"}}}

	v := a.LossHeat(base/1_000_000, base/1_000_000+20, 10)
	if v.Buckets != 2 || len(v.Rows) != 2 {
		t.Fatalf("want 2 buckets, 2 rows: %+v", v)
	}
	if v.Rows[0].Label != "Gateway" { // gateway ordered first
		t.Fatalf("first row should be Gateway: %+v", v.Rows)
	}
	if v.Rows[1].Label != "ProjectorPC" || v.Rows[1].Loss[0] != 50 {
		t.Fatalf("peer row mislabeled or wrong loss: %+v", v.Rows[1])
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
