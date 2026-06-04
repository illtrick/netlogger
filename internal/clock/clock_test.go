package clock

import "testing"

func TestSystemClockIsPositiveMicros(t *testing.T) {
	var c Clock = System{}
	got := c.NowUnixMicro()
	// Sanity: after 2020-01-01 in microseconds.
	if got < 1_577_836_800_000_000 {
		t.Fatalf("NowUnixMicro too small: %d", got)
	}
}

func TestFixedClockReturnsSetValue(t *testing.T) {
	var c Clock = Fixed{Micros: 42}
	if got := c.NowUnixMicro(); got != 42 {
		t.Fatalf("want 42, got %d", got)
	}
}
