package appcore

import "testing"

func TestTestNotesOverlap(t *testing.T) {
	a := &App{}
	// A window spanning 100s..200s (in microseconds).
	a.testWindows = []*testWindow{{fromUS: 100_000_000, toUS: 200_000_000, label: "stress test"}}

	// Grid: from 100s, 10s buckets, 20 buckets (100s..300s).
	notes := a.testNotes(100, 10, 20)

	if notes[0] != "stress test" {
		t.Fatalf("bucket 0 (100-110s) should note the window, got %q", notes[0])
	}
	if notes[9] != "stress test" {
		t.Fatalf("bucket 9 (190-200s) should still note the window, got %q", notes[9])
	}
	if notes[12] != "" {
		t.Fatalf("bucket 12 (220-230s) is after the window, want empty, got %q", notes[12])
	}
}

func TestMarkTestSpanFinalizeClampsEnd(t *testing.T) {
	a := &App{}
	done := a.markTestSpan("speed test", 60_000) // planned 60s out
	if len(a.testWindows) != 1 {
		t.Fatalf("expected one window, got %d", len(a.testWindows))
	}
	plannedEnd := a.testWindows[0].toUS
	done() // clamps end to ~now, which is well before planned end
	if a.testWindows[0].toUS >= plannedEnd {
		t.Fatalf("finalize should pull the end earlier than the planned end")
	}
}
