package appcore

import (
	"testing"
	"time"

	"netlogger/internal/probe"
)

func TestLifecycleProducesSamplesAndStopsClean(t *testing.T) {
	dir := t.TempDir()
	a := New(dir)
	// Deterministic seams: no real ICMP, no real iperf process.
	a.Ping = func(string, time.Duration) (probe.Result, error) {
		return probe.Result{RTT: 1500 * time.Microsecond}, nil
	}
	a.StartIperf = func(string) (func(), string) {
		return func() {}, "iperf 3.21 (test)"
	}
	a.tick = 5 * time.Millisecond // fast loop for the test

	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if a.Snapshot().Samples >= 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no samples produced; got %d", a.Snapshot().Samples)
		}
		time.Sleep(5 * time.Millisecond)
	}

	snap := a.Snapshot()
	if snap.Iperf3Version != "iperf 3.21 (test)" {
		t.Fatalf("iperf version = %q", snap.Iperf3Version)
	}
	if !snap.Iperf3ServerUp {
		t.Fatalf("expected iperf server up")
	}
	if snap.LastRTTms <= 0 {
		t.Fatalf("expected positive LastRTTms, got %v", snap.LastRTTms)
	}
	if snap.DBPath == "" || snap.DataDir != dir {
		t.Fatalf("unexpected paths: %+v", snap)
	}

	if err := a.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestLossReflectedInSnapshot(t *testing.T) {
	dir := t.TempDir()
	a := New(dir)
	a.Ping = func(string, time.Duration) (probe.Result, error) {
		return probe.Result{Lost: true}, nil
	}
	a.StartIperf = func(string) (func(), string) { return func() {}, "" }
	a.tick = 5 * time.Millisecond
	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()
	deadline := time.Now().Add(2 * time.Second)
	for a.Snapshot().Samples < 5 {
		if time.Now().After(deadline) {
			t.Fatalf("no samples")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := a.Snapshot().LossPct; got < 99 {
		t.Fatalf("expected ~100%% loss, got %v", got)
	}
}
