package ui

import "testing"

func TestNormalizeScalesToUnit(t *testing.T) {
	pts := normalize([]float64{0, 5, 10})
	if len(pts) != 3 || pts[0] != 0 || pts[2] != 1 {
		t.Fatalf("expected 0..1, got %v", pts)
	}
	if pts[1] < 0.49 || pts[1] > 0.51 {
		t.Fatalf("midpoint = %v, want ~0.5", pts[1])
	}
}

func TestChartBounds(t *testing.T) {
	if lo, hi := chartBounds(nil, 0); lo != 0 || hi != 1 {
		t.Fatalf("empty → (%v,%v), want (0,1)", lo, hi)
	}
	if lo, hi := chartBounds([][]float64{{100, 180, 120}}, 200); lo != 0 || hi != 210 {
		t.Fatalf("data 180 floor 200 → (%v,%v), want (0,210)", lo, hi)
	}
	if lo, hi := chartBounds([][]float64{{500, 950}}, 200); lo != 0 || hi < 997 || hi > 998 {
		t.Fatalf("data 950 floor 200 → (%v,%v), want (0,~997.5)", lo, hi)
	}
}

func TestNormalizeFlatAndEmpty(t *testing.T) {
	if got := normalize(nil); got != nil {
		t.Fatalf("nil -> %v", got)
	}
	flat := normalize([]float64{3, 3, 3})
	for _, v := range flat {
		if v != 0.5 {
			t.Fatalf("flat series should map to 0.5, got %v", flat)
		}
	}
}
