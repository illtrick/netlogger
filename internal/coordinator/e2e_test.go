package coordinator

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"netlogger/internal/config"
	"netlogger/internal/mesh"
	"netlogger/internal/readiness"
	"netlogger/internal/store"
)

// End-to-end: a real agent server (sync API + self-checks) is checked + pulled
// by the coordinator handlers, and the JSON reflects the live agent.
func TestEndToEndReadinessAndAgents(t *testing.T) {
	// 1. Stand up a real agent.
	s, err := store.Open(filepath.Join(t.TempDir(), "e2e.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	for i := 0; i < 3; i++ {
		_, _ = s.Insert(store.Sample{TSUnixUS: int64(i), ProbeType: "icmp", SrcHost: "ncase", DstHost: "ryzen", Direction: "rtt", RTTus: 700})
	}
	api := &mesh.AgentAPI{Store: s, NodeID: "ncase", Host: "ncase", Iperf3Version: "iperf 3.18", DataWritable: true}
	amux := http.NewServeMux()
	api.Register(amux)
	agent := httptest.NewServer(amux)
	t.Cleanup(agent.Close)
	addr := strings.TrimPrefix(agent.URL, "http://")

	node := config.Node{ID: "ncase", Type: config.NodeEndpoint, Address: addr}

	// 2. Readiness over the live agent -> all checks pass.
	rh := ReadinessHandler(readiness.NewChecker(), []config.Node{node})
	rr := httptest.NewRecorder()
	rh(rr, httptest.NewRequest(http.MethodGet, "/api/readiness", nil))
	var results []readiness.Result
	if err := json.Unmarshal(rr.Body.Bytes(), &results); err != nil {
		t.Fatalf("decode readiness: %v", err)
	}
	if len(results) != 1 || !results[0].Online || results[0].Issues != 0 {
		t.Fatalf("live agent should be online with 0 issues: %+v", results)
	}

	// 3. Pull from the agent, then /api/agents shows it online.
	agg, err := store.Open(filepath.Join(t.TempDir(), "agg.db"))
	if err != nil {
		t.Fatalf("open agg: %v", err)
	}
	t.Cleanup(func() { agg.Close() })
	p := mesh.NewPuller(agg)
	if _, err := p.PullOnce(mesh.AgentRef{ID: "ncase", BaseURL: agent.URL}); err != nil {
		t.Fatalf("pull: %v", err)
	}
	ah := AgentsHandler(p, []config.TargetRef{{ID: "ncase", Address: addr}})
	rr2 := httptest.NewRecorder()
	ah(rr2, httptest.NewRequest(http.MethodGet, "/api/agents", nil))
	var agents []AgentView
	if err := json.Unmarshal(rr2.Body.Bytes(), &agents); err != nil {
		t.Fatalf("decode agents: %v", err)
	}
	if len(agents) != 1 || !agents[0].Online {
		t.Fatalf("agent should show online after pull: %+v", agents)
	}
	if n, _ := agg.CountAgentSamples("ncase"); n != 3 {
		t.Fatalf("want 3 aggregated rows, got %d", n)
	}
}
