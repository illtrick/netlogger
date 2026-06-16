package appcore

import (
	"testing"
	"time"

	"netlogger/internal/appsettings"
	"netlogger/internal/discovery"
	"netlogger/internal/nicstat"
	"netlogger/internal/probe"
	"netlogger/internal/store"
)

func TestLifecycleProducesSamplesAndStopsClean(t *testing.T) {
	dir := t.TempDir()
	a := New(dir)
	a.CollectNICs = func() []nicstat.NIC { return nil }
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
	a.CollectNICs = func() []nicstat.NIC { return nil }
	a.Ping = func(string, time.Duration) (probe.Result, error) {
		return probe.Result{Lost: true}, nil
	}
	a.StartIperf = func(string) (func(), string) { return func() {}, "" }
	a.FetchLinks = func(string) (LinkReport, error) { return LinkReport{}, nil }
	a.FetchEvents = func(string) ([]EventInfo, error) { return nil, nil }
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
	a.CollectNICs = func() []nicstat.NIC { return nil }
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
	a.CollectNICs = func() []nicstat.NIC { return nil }
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
	a.CollectNICs = func() []nicstat.NIC { return nil }
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
	a.CollectNICs = func() []nicstat.NIC { return nil }
	a.Ping = func(string, time.Duration) (probe.Result, error) {
		return probe.Result{RTT: time.Millisecond}, nil
	}
	a.StartIperf = func(string) (func(), string) { return func() {}, "" }
	a.FetchLinks = func(string) (LinkReport, error) { return LinkReport{}, nil }
	a.FetchEvents = func(string) ([]EventInfo, error) { return nil, nil }
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
	a.CollectNICs = func() []nicstat.NIC { return nil }
	a.Ping = func(addr string, _ time.Duration) (probe.Result, error) {
		return probe.Result{RTT: 2 * time.Millisecond}, nil
	}
	a.StartIperf = func(string) (func(), string) { return func() {}, "" }
	a.FetchLinks = func(string) (LinkReport, error) { return LinkReport{}, nil }
	a.FetchEvents = func(string) ([]EventInfo, error) { return nil, nil }
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

func TestSnapshotShowsGateway(t *testing.T) {
	dir := t.TempDir()
	a := New(dir)
	a.CollectNICs = func() []nicstat.NIC { return nil }
	a.Ping = func(addr string, _ time.Duration) (probe.Result, error) {
		return probe.Result{RTT: 3 * time.Millisecond}, nil
	}
	a.StartIperf = func(string) (func(), string) { return func() {}, "" }
	a.FetchLinks = func(string) (LinkReport, error) { return LinkReport{}, nil }
	a.FetchEvents = func(string) ([]EventInfo, error) { return nil, nil }
	a.Discovery = fakeLister{}
	a.GatewayIP = "192.168.0.1"
	a.tick = 5 * time.Millisecond
	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()
	deadline := time.Now().Add(2 * time.Second)
	for {
		s := a.Snapshot()
		if s.GatewayIP == "192.168.0.1" && s.GatewayRTTms > 0 {
			if s.GatewayLossPct != 0 {
				t.Fatalf("gateway loss = %v, want 0", s.GatewayLossPct)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("gateway not populated; got %+v", a.Snapshot())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSnapshotAssemblesMatrixFromPeerReports(t *testing.T) {
	dir := t.TempDir()
	a := New(dir)
	a.CollectNICs = func() []nicstat.NIC { return nil }
	a.Ping = func(string, time.Duration) (probe.Result, error) { return probe.Result{RTT: time.Millisecond}, nil }
	a.StartIperf = func(string) (func(), string) { return func() {}, "" }
	a.FetchLinks = func(string) (LinkReport, error) { return LinkReport{}, nil }
	a.FetchEvents = func(string) ([]EventInfo, error) { return nil, nil }
	a.ProbeUDP = func(string, int, time.Duration, time.Duration) (probe.UDPStats, error) {
		return probe.UDPStats{Sent: 200, Received: 200, AvgRTT: time.Millisecond, Jitter: 200 * time.Microsecond}, nil
	}
	a.Discovery = fakeLister{peers: []discovery.Peer{{ID: "b", Host: "hostB", Addr: "10.0.0.2:8088"}}}
	a.FetchLinks = func(addr string) (LinkReport, error) {
		return LinkReport{NodeID: "b", Host: "hostB", Links: []LinkStat{{PeerID: a.NodeID(), RTTms: 1.2, LossPct: 3.0, Drops: 4}}}, nil
	}
	a.tick = 5 * time.Millisecond
	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()
	deadline := time.Now().Add(2 * time.Second)
	for {
		m := a.Snapshot().Matrix
		if c, ok := m.Cell("b", a.NodeID()); ok && c.LossPct == 3.0 {
			if len(m.Nodes) < 2 {
				t.Fatalf("expected >=2 nodes, got %d", len(m.Nodes))
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("matrix not assembled; got %+v", a.Snapshot().Matrix)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSnapshotShowsUDPJitterAndLoss(t *testing.T) {
	dir := t.TempDir()
	a := New(dir)
	a.CollectNICs = func() []nicstat.NIC { return nil }
	a.Ping = func(string, time.Duration) (probe.Result, error) {
		return probe.Result{RTT: time.Millisecond}, nil
	}
	a.StartIperf = func(string) (func(), string) { return func() {}, "" }
	a.FetchLinks = func(string) (LinkReport, error) { return LinkReport{}, nil }
	a.FetchEvents = func(string) ([]EventInfo, error) { return nil, nil }
	a.ProbeUDP = func(string, int, time.Duration, time.Duration) (probe.UDPStats, error) {
		return probe.UDPStats{Sent: 200, Received: 198, LossPct: 1.0, AvgRTT: time.Millisecond, Jitter: 500 * time.Microsecond}, nil
	}
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
		if len(ps) == 1 && ps[0].JitterMs > 0 {
			if ps[0].UDPLossPct != 1.0 {
				t.Fatalf("UDP loss = %v, want 1.0", ps[0].UDPLossPct)
			}
			if ps[0].DropEpisodes < 1 {
				t.Fatalf("expected >=1 drop episode, got %d", ps[0].DropEpisodes)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("UDP jitter not populated; got %+v", a.Snapshot().Peers)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestUDPBurstsPersistedToStore(t *testing.T) {
	dir := t.TempDir()
	a := New(dir)
	a.CollectNICs = func() []nicstat.NIC { return nil }
	a.Ping = func(string, time.Duration) (probe.Result, error) { return probe.Result{RTT: time.Millisecond}, nil }
	a.StartIperf = func(string) (func(), string) { return func() {}, "" }
	a.FetchLinks = func(string) (LinkReport, error) { return LinkReport{}, nil }
	a.FetchEvents = func(string) ([]EventInfo, error) { return nil, nil }
	a.ProbeUDP = func(string, int, time.Duration, time.Duration) (probe.UDPStats, error) {
		return probe.UDPStats{Sent: 200, Received: 190, LossPct: 5, AvgRTT: time.Millisecond, Jitter: 300 * time.Microsecond}, nil
	}
	a.Discovery = fakeLister{peers: []discovery.Peer{{ID: "p1", Host: "h1", Addr: "10.0.0.1:8088"}}}
	a.tick = 5 * time.Millisecond
	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// let a few bursts persist
	time.Sleep(120 * time.Millisecond)
	a.Stop()

	// reopen and count udp_iso rows with loss
	st, err := store.Open(a.dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st.Close()
	samples, err := st.Since(0, 100000)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	udpLost := 0
	for _, s := range samples {
		if s.ProbeType == "udp_iso" && s.DstHost == "p1" && s.Lost {
			udpLost++
		}
	}
	if udpLost == 0 {
		t.Fatalf("expected persisted udp_iso lost rows, got 0 (of %d samples)", len(samples))
	}
}

func TestSnapshotInternetUptimeAndHistory(t *testing.T) {
	dir := t.TempDir()
	a := New(dir)
	a.CollectNICs = func() []nicstat.NIC { return nil }
	a.Ping = func(addr string, _ time.Duration) (probe.Result, error) {
		return probe.Result{RTT: 2 * time.Millisecond}, nil
	}
	a.StartIperf = func(string) (func(), string) { return func() {}, "" }
	a.FetchLinks = func(string) (LinkReport, error) { return LinkReport{}, nil }
	a.FetchEvents = func(string) ([]EventInfo, error) { return nil, nil }
	a.ProbeUDP = func(string, int, time.Duration, time.Duration) (probe.UDPStats, error) {
		return probe.UDPStats{Sent: 200, Received: 200, AvgRTT: time.Millisecond, Jitter: 100 * time.Microsecond}, nil
	}
	a.Discovery = fakeLister{peers: []discovery.Peer{{ID: "p1", Host: "h1", Addr: "10.0.0.1:8088"}}}
	a.GatewayIP = "192.168.0.1"
	a.InternetIP = "8.8.8.8"
	a.tick = 5 * time.Millisecond
	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()
	deadline := time.Now().Add(2 * time.Second)
	for {
		s := a.Snapshot()
		if s.InternetIP == "8.8.8.8" && s.InternetRTTms > 0 && len(s.Peers) == 1 && s.Peers[0].UpForSec >= 0 && len(s.Peers[0].RTTHist) > 0 {
			if s.SessionUptimeSec < 0 {
				t.Fatalf("bad uptime %d", s.SessionUptimeSec)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("internet/uptime/history not populated; got %+v", a.Snapshot())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestResetSessionClearsState(t *testing.T) {
	dir := t.TempDir()
	a := New(dir)
	a.CollectNICs = func() []nicstat.NIC { return nil }
	a.Ping = func(string, time.Duration) (probe.Result, error) { return probe.Result{RTT: time.Millisecond}, nil }
	a.StartIperf = func(string) (func(), string) { return func() {}, "" }
	a.FetchLinks = func(string) (LinkReport, error) { return LinkReport{}, nil }
	a.FetchEvents = func(string) ([]EventInfo, error) { return nil, nil }
	a.ProbeUDP = func(string, int, time.Duration, time.Duration) (probe.UDPStats, error) {
		return probe.UDPStats{Sent: 200, Received: 200, AvgRTT: time.Millisecond, Jitter: 100 * time.Microsecond}, nil
	}
	a.Discovery = fakeLister{peers: []discovery.Peer{{ID: "p1", Host: "h1", Addr: "10.0.0.1:8088"}}}
	a.tick = 5 * time.Millisecond
	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()
	// accumulate some samples
	deadline := time.Now().Add(time.Second)
	for a.Snapshot().Samples < 5 {
		if time.Now().After(deadline) {
			t.Fatal("no samples accumulated")
		}
		time.Sleep(5 * time.Millisecond)
	}
	a.ResetSession()
	s := a.Snapshot()
	if s.Samples > 3 { // should be reset to ~0 (a tick or two may have re-added)
		t.Fatalf("expected samples reset near 0, got %d", s.Samples)
	}
	if s.SessionUptimeSec > 2 {
		t.Fatalf("expected uptime reset, got %d", s.SessionUptimeSec)
	}
}

type fakeKeeper struct{ onStop func() }

func (f fakeKeeper) Stop() {
	if f.onStop != nil {
		f.onStop()
	}
}

func TestSetPreventSleepTogglesAndPersists(t *testing.T) {
	dir := t.TempDir()
	a := New(dir)
	a.CollectNICs = func() []nicstat.NIC { return nil }
	starts, stops := 0, 0
	a.StartKeeper = func() sleepKeeper { starts++; return fakeKeeper{onStop: func() { stops++ }} }

	a.SetPreventSleep(false) // keeper was never started; just records off + persists
	if a.Snapshot().PreventSleep {
		t.Fatalf("expected PreventSleep false")
	}
	a.SetPreventSleep(true) // starts the keeper
	if starts != 1 {
		t.Fatalf("expected 1 start, got %d", starts)
	}
	if !a.Snapshot().PreventSleep {
		t.Fatalf("expected PreventSleep true")
	}
	a.SetPreventSleep(false) // stops the keeper
	if stops != 1 {
		t.Fatalf("expected 1 stop, got %d", stops)
	}
	if appsettings.Load(appsettings.Path(dir)).PreventSleep {
		t.Fatalf("expected persisted false")
	}
}

func TestSnapshotExposesNICsWithDelta(t *testing.T) {
	dir := t.TempDir()
	a := New(dir)
	a.CollectNICs = func() []nicstat.NIC { return nil }
	a.Ping = func(string, time.Duration) (probe.Result, error) { return probe.Result{RTT: time.Millisecond}, nil }
	a.StartIperf = func(string) (func(), string) { return func() {}, "" }
	a.FetchLinks = func(string) (LinkReport, error) { return LinkReport{}, nil }
	a.FetchEvents = func(string) ([]EventInfo, error) { return nil, nil }
	a.Discovery = fakeLister{}
	calls := 0
	a.CollectNICs = func() []nicstat.NIC {
		calls++
		return []nicstat.NIC{{Name: "Ethernet", LinkSpeed: "2.5 Gbps", Status: "Up", RxDiscards: int64(40 + calls*5), Power: "Green Ethernet=Enabled"}}
	}
	a.nicTick = 5 * time.Millisecond
	a.tick = 5 * time.Millisecond
	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()
	deadline := time.Now().Add(2 * time.Second)
	for {
		nics := a.Snapshot().NICs
		// after >=2 polls, a delta should be computed
		if len(nics) == 1 && nics[0].Name == "Ethernet" && nics[0].Power == "Green Ethernet=Enabled" && nics[0].RecentRxDiscards > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("NIC delta not populated; got %+v", a.Snapshot().NICs)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
