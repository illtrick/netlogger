package ui

import (
	"testing"

	"netlogger/internal/appcore"
)

func TestFmtDuration(t *testing.T) {
	if fmtDuration(45) != "45s" || fmtDuration(125) != "2m 5s" || fmtDuration(3725) != "1h 2m" {
		t.Fatalf("durations: %q %q %q", fmtDuration(45), fmtDuration(125), fmtDuration(3725))
	}
}

func TestOverallStatus(t *testing.T) {
	if s, _ := overallStatus(appcore.Snapshot{}); s != "ALL HEALTHY" {
		t.Fatalf("empty = %q", s)
	}
	if s, _ := overallStatus(appcore.Snapshot{Peers: []appcore.PeerInfo{{UDPLossPct: 2}}}); s != "DEGRADED" {
		t.Fatalf("lossy = %q", s)
	}
}
