package ui

import (
	"strings"
	"testing"
	"time"
)

func TestGradeSubLineOmitsPhantomZeros(t *testing.T) {
	if got := gradeSubLine(1415, 0, 0); got != "RPM 1415" {
		t.Fatalf("unmeasured jitter/loss must be omitted, got %q", got)
	}
	if got := gradeSubLine(1415, 2, 0); !strings.Contains(got, "jitter 2 ms") {
		t.Fatalf("jitter should show when > 0, got %q", got)
	}
	if got := gradeSubLine(1415, 0, 0.5); !strings.Contains(got, "loss 0.5%") {
		t.Fatalf("loss should show when > 0, got %q", got)
	}
}

func TestInternetProvenanceNamesNodeAndServer(t *testing.T) {
	at := time.Date(2026, 1, 2, 15, 4, 0, 0, time.UTC)
	got := internetProvenance(at, "ryzen", "Los Angeles (Clouvider)")
	if !strings.Contains(got, "on ryzen") || !strings.Contains(got, "Los Angeles (Clouvider)") {
		t.Fatalf("provenance missing node/server: %q", got)
	}
	if internetProvenance(time.Time{}, "ryzen", "x") != "" {
		t.Fatalf("zero time should yield no provenance")
	}
}

func TestNextTab(t *testing.T) {
	if nextTab(navDashboard, navTests) != navTests {
		t.Fatalf("explicit select should win")
	}
	if tabLabel(navTests) != "Tests" || tabLabel(navEvents) != "Events" || tabLabel(navDashboard) != "Dashboard" {
		t.Fatalf("tab labels wrong")
	}
}

func TestMatrixCellStyle(t *testing.T) {
	if matrixCellColor(950) != colGood || matrixCellColor(600) != colWatch || matrixCellColor(100) != colBad {
		t.Fatalf("cell colors wrong")
	}
	if matrixCellText(-1) != "—" || matrixCellText(941) != "941 Mb/s" {
		t.Fatalf("cell text wrong: %q %q", matrixCellText(-1), matrixCellText(941))
	}
}

func TestFmtRate(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0 Mb/s"},
		{-1, "0 Mb/s"},
		{941, "941 Mb/s"},
		{999.4, "999 Mb/s"},
		{2372, "2.37 Gb/s"},
		{9860, "9.86 Gb/s"},
		{123989, "124.0 Gb/s"}, // the loopback-bug magnitude, now at least honest
	}
	for _, c := range cases {
		if got := fmtRate(c.in); got != c.want {
			t.Errorf("fmtRate(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestStressLoadColor(t *testing.T) {
	if stressHealthColor(true) != colBad {
		t.Fatalf("aborted link should be red")
	}
	if stressHealthColor(false) != colGood {
		t.Fatalf("healthy link should be green")
	}
}

func TestSubViewLabel(t *testing.T) {
	if subLabel(0) != "Speed (LAN)" || subLabel(1) != "Stress" || subLabel(2) != "Internet" {
		t.Fatalf("sub-view labels wrong")
	}
}

func TestSeriesColorsAvoidSeverity(t *testing.T) {
	for i, c := range seriesColors {
		if c == colBad || c == colWatch || c == colGood {
			t.Errorf("seriesColors[%d] reuses a severity color", i)
		}
	}
}

func TestCoarseAge(t *testing.T) {
	if coarseAge(42_000_000) != "42s ago" || coarseAge(310_000_000) != "5m ago" || coarseAge(7_300_000_000) != "2h ago" {
		t.Fatalf("coarse ages wrong: %s %s %s", coarseAge(42_000_000), coarseAge(310_000_000), coarseAge(7_300_000_000))
	}
	if coarseAge(-5) != "0s ago" {
		t.Fatalf("negative age should clamp")
	}
}
