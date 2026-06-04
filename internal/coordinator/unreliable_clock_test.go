package coordinator

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"netlogger/internal/correlate"
	"netlogger/internal/mesh"
	"netlogger/internal/store"
)

// An agent whose clock was measured and deemed UNRELIABLE (clamped) must be
// excluded from correlation — it cannot fabricate a shared-device verdict by
// overlapping a reliable agent (spec §6).
func TestCorrelationExcludesUnreliableClock(t *testing.T) {
	agg, err := store.Open(filepath.Join(t.TempDir(), "agg.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { agg.Close() })

	// Both agents drop at the same instant -> would correlate as simultaneous
	// if both were trusted.
	_ = agg.Upsert("ncase", store.Sample{Seq: 1, TSUnixUS: 1000, ProbeType: "icmp", SrcHost: "ncase", DstHost: "ryzen", Lost: true})
	_ = agg.Upsert("nas", store.Sample{Seq: 1, TSUnixUS: 1000, ProbeType: "icmp", SrcHost: "nas", DstHost: "ryzen", Lost: true})

	offsets := mesh.NewOffsets()
	offsets.Set("ncase", mesh.Offset{OffsetUS: 0, RTTus: 200, Reliable: true})
	offsets.Set("nas", mesh.Offset{OffsetUS: 90_000_000, RTTus: 200, Reliable: false}) // clamped

	h := CorrelationHandler(agg, []string{"ncase", "nas"}, offsets)
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/api/correlation", nil))

	var groups []correlate.Group
	if err := json.Unmarshal(rr.Body.Bytes(), &groups); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, g := range groups {
		for _, e := range g.Events {
			if e.AgentID == "nas" {
				t.Fatalf("unreliable-clock agent nas must be excluded from correlation: %+v", groups)
			}
		}
		if g.Simultaneous {
			t.Fatalf("no simultaneous group should form once nas is excluded: %+v", groups)
		}
	}
}
