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

func TestPowerText(t *testing.T) {
	if powerText("") != "none" {
		t.Fatalf(`powerText("") = %q`, powerText(""))
	}
	if powerText("Green Ethernet=Enabled") != "Green Ethernet=Enabled" {
		t.Fatalf(`powerText(prop) = %q`, powerText("Green Ethernet=Enabled"))
	}
}

func TestPowerSavingOn(t *testing.T) {
	// any enabled token (even when another is disabled) flags the row
	if !powerSavingOn("Energy-Efficient Ethernet=Disabled; Green Ethernet=Enabled") {
		t.Fatalf("a mix containing an Enabled prop should be on")
	}
	if !powerSavingOn("Energy Detect=On") {
		t.Fatalf(`"On" should count as enabled`)
	}
	if powerSavingOn("Energy-Efficient Ethernet=Disabled; Gigabit Lite=Disabled") {
		t.Fatalf("all-disabled should be off")
	}
	if powerSavingOn("") {
		t.Fatalf("empty should be off")
	}
}

func TestEventLine(t *testing.T) {
	now := int64(1_000_000_000) // 1000s in micros
	e := appcore.MergedEvent{Host: "ryzen", UnixMicro: now - 125_000_000, Detail: "Ethernet link Down"}
	if got := eventLine(e, now); got != "2m 5s ago  ryzen: Ethernet link Down" {
		t.Fatalf("eventLine = %q", got)
	}
	// a clock that ran backwards shouldn't produce a negative age
	future := appcore.MergedEvent{Host: "pc", UnixMicro: now + 5_000_000, Detail: "x"}
	if got := eventLine(future, now); got != "0s ago  pc: x" {
		t.Fatalf("future event = %q", got)
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
