package appcore

import (
	"testing"
	"time"

	"netlogger/internal/discovery"
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

func TestStopIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	a := New(dir)
	a.Ping = func(string, time.Duration) (probe.Result, error) {
		return probe.Result{RTT: time.Millisecond}, nil
	}
	a.StartIperf = func(string) (func(), string) { return func() {}, "v" }
	a.tick = 5 * time.Millisecond
	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := a.Stop(); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	if err := a.Stop(); err != nil {
		t.Fatalf("second Stop should be a no-op returning nil, got: %v", err)
	}
}

func TestServerDownWhenIperfUnavailable(t *testing.T) {
	dir := t.TempDir()
	a := New(dir)
	a.Ping = func(string, time.Duration) (probe.Result, error) {
		return probe.Result{RTT: time.Millisecond}, nil
	}
	a.StartIperf = func(string) (func(), string) { return nil, "" } // no binary -> nil stop
	a.tick = 5 * time.Millisecond
	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()
	if a.Snapshot().Iperf3ServerUp {
		t.Fatalf("expected Iperf3ServerUp=false when iperf3 server is unavailable")
	}
}

func TestSnapshotBeforeStart(t *testing.T) {
	dir := t.TempDir()
	a := New(dir)
	s := a.Snapshot()
	if s.Samples != 0 || s.LossPct != 0 || s.Iperf3ServerUp {
		t.Fatalf("expected zero-value snapshot before Start, got %+v", s)
	}
	if s.DataDir != dir || s.DBPath == "" {
		t.Fatalf("expected paths populated, got %+v", s)
	}
}

type fakeLister struct{ peers []discovery.Peer }

func (f fakeLister) Peers() []discovery.Peer { return f.peers }

func TestSnapshotExposesDiscoveredPeers(t *testing.T) {
	dir := t.TempDir()
	a := New(dir)
	a.Ping = func(string, time.Duration) (probe.Result, error) {
		return probe.Result{RTT: time.Millisecond}, nil
	}
	a.StartIperf = func(string) (func(), string) { return func() {}, "" }
	a.Discovery = fakeLister{peers: []discovery.Peer{
		{ID: "p1", Host: "h1", Addr: "10.0.0.1:8088", Version: "v"},
	}}
	a.tick = 5 * time.Millisecond
	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()
	snap := a.Snapshot()
	if len(snap.Peers) != 1 || snap.Peers[0].ID != "p1" || snap.Peers[0].Addr != "10.0.0.1:8088" {
		t.Fatalf("expected discovered peer in snapshot, got %+v", snap.Peers)
	}
}

func TestSnapshotShowsPerPeerRTTAndLoss(t *testing.T) {
	dir := t.TempDir()
	a := New(dir)
	a.Ping = func(addr string, _ time.Duration) (probe.Result, error) {
		return probe.Result{RTT: 2 * time.Millisecond}, nil
	}
	a.StartIperf = func(string) (func(), string) { return func() {}, "" }
	a.Discovery = fakeLister{peers: []discovery.Peer{
		{ID: "p1", Host: "h1", Addr: "10.0.0.1:8088"},
	}}
	a.tick = 5 * time.Millisecond
	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()
	deadline := time.Now().Add(2 * time.Second)
	for {
		ps := a.Snapshot().Peers
		if len(ps) == 1 && ps[0].RTTms > 0 {
			if ps[0].LossPct != 0 {
				t.Fatalf("expected 0%% loss, got %v", ps[0].LossPct)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("peer RTT not populated; got %+v", a.Snapshot().Peers)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
