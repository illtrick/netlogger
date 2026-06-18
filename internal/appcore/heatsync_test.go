package appcore

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMergeHeatLabelsAndOrders(t *testing.T) {
	local := HeatView{FromUnix: 100, BucketSec: 60, Buckets: 2, Rows: []HeatRow{
		{Label: "Gateway", Loss: []float64{0, 50}},
	}}
	peers := map[string][]HeatRow{
		"sarah-pc": {{Label: "ProjectorPC", Loss: []float64{0, 0}}},
	}
	v := mergeHeat("ryzen", local, peers)
	if v.Buckets != 2 || len(v.Rows) != 2 {
		t.Fatalf("want 2 rows over 2 buckets: %+v", v)
	}
	if v.Rows[0].Label != "ryzen · Gateway" { // self first, host-prefixed
		t.Fatalf("self row mislabeled: %q", v.Rows[0].Label)
	}
	if v.Rows[1].Label != "sarah-pc · ProjectorPC" || v.Rows[1].Loss[1] != 0 {
		t.Fatalf("peer row mislabeled: %+v", v.Rows[1])
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
