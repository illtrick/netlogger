package appcore

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCollapseMachine(t *testing.T) {
	rows := []HeatRow{
		{Label: "Gateway", Loss: []float64{0, 0, -1}},
		{Label: "NIC link", Loss: []float64{-1, 100, -1}},
		{Label: "ProjectorPC", Loss: []float64{0, 100, -1}},
	}
	m := collapseMachine("ryzen", rows, 3)
	if m.Sev[0] != 0 || m.Detail[0] != "" { // all links clean → green, no detail
		t.Fatalf("bucket0 should be clean: sev=%v det=%q", m.Sev[0], m.Detail[0])
	}
	if m.Sev[1] != 100 { // NIC reset + peer 100% → worst = 100
		t.Fatalf("bucket1 sev should be 100: %v", m.Sev[1])
	}
	if !strings.Contains(m.Detail[1], "NIC link reset") || !strings.Contains(m.Detail[1], "ProjectorPC 100%") {
		t.Fatalf("bucket1 detail should name the problems by type: %q", m.Detail[1])
	}
	if m.Sev[2] != -1 { // no link reported → no data
		t.Fatalf("bucket2 should be no-data: %v", m.Sev[2])
	}
}

func TestResampleRowsShiftAndZoom(t *testing.T) {
	rows := []HeatRow{{Label: "Gateway", Loss: []float64{0, 50, 100}}} // buckets at 1000,1010,1020 (pb=10)
	// shift: same bucket size, window advanced by one bucket → drop oldest, live edge -1
	got := resampleRows(rows, 1000, 10, 1010, 10, 3)[0].Loss
	if got[0] != 50 || got[1] != 100 || got[2] != -1 {
		t.Fatalf("shift resample wrong: %+v", got)
	}
	// zoom out: 20s buckets from 1000 → each new bucket merges two old (max)
	got = resampleRows(rows, 1000, 10, 1000, 20, 2)[0].Loss
	if got[0] != 50 || got[1] != 100 { // [1000,1020)→max(0,50)=50 ; [1020,1040)→max(100)=100
		t.Fatalf("zoom-out resample wrong: %+v", got)
	}
}

func TestLossBucketsHandlerRoundTrip(t *testing.T) {
	want := HeatView{FromUnix: 5, BucketSec: 60, Buckets: 1, Rows: []HeatRow{{Label: "Gateway", Loss: []float64{12}}}}
	mux := http.NewServeMux()
	mux.Handle("/api/lossbuckets", lossBucketsHandler(func(from, to int64, bucket int) HeatView { return want }))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	got, err := fetchLossBuckets(http.DefaultClient, srv.URL, 5, 65, 60)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got.Buckets != 1 || len(got.Rows) != 1 || got.Rows[0].Label != "Gateway" || got.Rows[0].Loss[0] != 12 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}
