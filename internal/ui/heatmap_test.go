package ui

import "testing"

func TestPanElements(t *testing.T) {
	// drag right by one cell width → scroll back one element (negative)
	if got := panElements(10, 10); got != -1 {
		t.Fatalf("one-cell drag right = %v, want -1", got)
	}
	// drag left → scroll forward
	if got := panElements(-20, 10); got != 2 {
		t.Fatalf("two-cell drag left = %v, want 2", got)
	}
	// fractional
	if got := panElements(5, 10); got != -0.5 {
		t.Fatalf("half-cell = %v, want -0.5", got)
	}
	if got := panElements(7, 0); got != 0 {
		t.Fatalf("zero cell width should be safe, got %v", got)
	}
}
