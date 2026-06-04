package mesh

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// timeServerWithOffset serves /api/time as if the agent clock is ahead by skew.
func timeServerWithOffset(t *testing.T, skew time.Duration) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/time", func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().Add(skew).UTC().UnixMicro()
		_ = json.NewEncoder(w).Encode(TimePair{T2UnixUS: now, T3UnixUS: now})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestMeasureOffsetRecoversSkew(t *testing.T) {
	srv := timeServerWithOffset(t, 250*time.Millisecond)
	off, err := MeasureOffset(srv.Client(), srv.URL, 8)
	if err != nil {
		t.Fatalf("measure: %v", err)
	}
	if !off.Reliable {
		t.Fatal("offset should be reliable for a 250ms skew")
	}
	got := off.OffsetUS
	if got < 150_000 || got > 350_000 {
		t.Fatalf("offset %dus not near +250000us", got)
	}
	if off.RTTus < 0 {
		t.Fatalf("negative RTT: %d", off.RTTus)
	}
}

func TestHalfUncRoundsUpAndGuardsNegative(t *testing.T) {
	// Rounds up so the uncertainty band is never understated (spec §6).
	if got := (Offset{RTTus: 201}).HalfUncUS(); got != 101 {
		t.Fatalf("want 101 (round up), got %d", got)
	}
	if got := (Offset{RTTus: 200}).HalfUncUS(); got != 100 {
		t.Fatalf("want 100, got %d", got)
	}
	// A negative delta (clock jitter / granularity) must never widen-inward.
	if got := (Offset{RTTus: -4}).HalfUncUS(); got != 0 {
		t.Fatalf("negative RTT must yield 0 half-uncertainty, got %d", got)
	}
}

func TestMeasureOffsetClampsAbsurd(t *testing.T) {
	srv := timeServerWithOffset(t, 60*time.Second)
	off, err := MeasureOffset(srv.Client(), srv.URL, 4)
	if err != nil {
		t.Fatalf("measure: %v", err)
	}
	if off.Reliable {
		t.Fatal("a 60s offset must be marked unreliable (clamped)")
	}
}
