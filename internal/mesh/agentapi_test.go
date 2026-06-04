package mesh

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"netlogger/internal/store"
)

func newAgentWithSamples(t *testing.T) (*AgentAPI, *store.Store) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	for i := 0; i < 3; i++ {
		if _, err := s.Insert(store.Sample{TSUnixUS: int64(100 + i), ProbeType: "icmp", SrcHost: "ncase", DstHost: "ryzen", Direction: "rtt", RTTus: 900}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	return &AgentAPI{Store: s, NodeID: "ncase", Host: "ncase-host"}, s
}

func TestAgentInfo(t *testing.T) {
	api, _ := newAgentWithSamples(t)
	rr := httptest.NewRecorder()
	api.Info(rr, httptest.NewRequest(http.MethodGet, "/api/info", nil))
	if rr.Code != 200 {
		t.Fatalf("code %d", rr.Code)
	}
	var info Info
	if err := json.Unmarshal(rr.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if info.NodeID != "ncase" || info.Host != "ncase-host" || info.TimeUnixUS == 0 {
		t.Fatalf("bad info: %+v", info)
	}
}

func TestAgentSamplesSince(t *testing.T) {
	api, _ := newAgentWithSamples(t)

	// since=0 -> all 3
	rr := httptest.NewRecorder()
	api.Samples(rr, httptest.NewRequest(http.MethodGet, "/api/samples?since=0&limit=100", nil))
	var all []store.Sample
	if err := json.Unmarshal(rr.Body.Bytes(), &all); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("want 3, got %d", len(all))
	}

	// since=2 -> only seq 3
	rr2 := httptest.NewRecorder()
	api.Samples(rr2, httptest.NewRequest(http.MethodGet, "/api/samples?since=2&limit=100", nil))
	var rest []store.Sample
	if err := json.Unmarshal(rr2.Body.Bytes(), &rest); err != nil {
		t.Fatalf("decode2: %v", err)
	}
	if len(rest) != 1 || rest[0].Seq != 3 {
		t.Fatalf("want only seq 3, got %+v", rest)
	}
}

func TestAgentInfoReportsSelfChecks(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "i.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	api := &AgentAPI{Store: s, NodeID: "ncase", Host: "h", Iperf3Version: "iperf 3.18", DataWritable: true}

	mux := http.NewServeMux()
	api.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	info, err := FetchInfo(srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if info.Iperf3Version != "iperf 3.18" || !info.DataWritable {
		t.Fatalf("self-checks not reported: %+v", info)
	}
}
