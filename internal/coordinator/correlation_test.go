package coordinator

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"netlogger/internal/config"
	"netlogger/internal/score"
	"netlogger/internal/store"
)

func TestComponentsHandlerScoresFromAggregatedSamples(t *testing.T) {
	agg, err := store.Open(filepath.Join(t.TempDir(), "agg.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { agg.Close() })
	// ncase -> ryzen path, one failing sample.
	_ = agg.Upsert("ncase", store.Sample{Seq: 1, TSUnixUS: 100, ProbeType: "icmp", SrcHost: "ncase", DstHost: "ryzen", Lost: true})

	cfg := &config.Config{
		Nodes: []config.Node{
			{ID: "ncase", Type: config.NodeEndpoint, Label: "NCASE", Address: "127.0.0.1:1"},
			{ID: "switch1", Type: config.NodeSwitch, Label: "Switch 1"},
			{ID: "ryzen", Type: config.NodeEndpoint, Label: "Ryzen", Address: "127.0.0.1:2"},
		},
		Links: [][]string{{"ncase", "switch1"}, {"switch1", "ryzen"}},
	}

	h := ComponentsHandler(agg, cfg)
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/api/components", nil))
	var comps []score.Component
	if err := json.Unmarshal(rr.Body.Bytes(), &comps); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var sw score.Component
	for _, c := range comps {
		if c.ID == "switch1" {
			sw = c
		}
	}
	if sw.Health != "poor" {
		t.Fatalf("switch1 should be poor from the failing path, got %q (%+v)", sw.Health, comps)
	}
}
