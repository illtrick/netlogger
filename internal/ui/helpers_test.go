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

func TestEEEText(t *testing.T) {
	if eeeText("") != "n/a" {
		t.Fatalf(`eeeText("") = %q`, eeeText(""))
	}
	if eeeText("Enabled") != "Enabled" {
		t.Fatalf(`eeeText("Enabled") = %q`, eeeText("Enabled"))
	}
}

func TestEEEIsOn(t *testing.T) {
	if !eeeIsOn("enabled") || !eeeIsOn("On") {
		t.Fatalf("expected enabled/On to be on")
	}
	if eeeIsOn("Disabled") || eeeIsOn("") {
		t.Fatalf("expected Disabled/empty to be off")
	}
}

func TestAdapterHasFaults(t *testing.T) {
	if adapterHasFaults(appcore.NICInfo{}) {
		t.Fatalf("zero deltas should not be a fault")
	}
	if !adapterHasFaults(appcore.NICInfo{RecentTxDiscards: 1}) {
		t.Fatalf("a tx discard should be a fault")
	}
}
