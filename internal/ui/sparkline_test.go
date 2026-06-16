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
