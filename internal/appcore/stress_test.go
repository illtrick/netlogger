package appcore

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"netlogger/internal/iperf"
)

func TestMeshAssignments(t *testing.T) {
	self := PeerInfo{ID: "self", Host: "ryzen", Addr: "10.0.0.1:8088"}
	peers := []PeerInfo{
		{ID: "p", Host: "proj", Addr: "10.0.0.2:8088"},
		{ID: "s", Host: "laptop", Addr: "10.0.0.3:8088"},
	}
	m := meshAssignments(self, peers, true)
	for id, asg := range m {
		if len(asg.Targets) != 2 || len(asg.TargetPorts) != 2 {
			t.Fatalf("%s should target 2 others with ports: %+v", id, asg)
		}
		for _, tg := range asg.Targets {
			if tg == "10.0.0.1:8088" {
				t.Fatalf("target still has control port: %v", tg)
			}
		}
		// Exactly one extra listener per node in a 3-node mesh: its two inbound
		// clients get 5201 and 5202.
		if len(asg.ListenPorts) != 1 || asg.ListenPorts[0] != 5202 {
			t.Fatalf("%s listen ports = %v, want [5202]", id, asg.ListenPorts)
		}
	}
	// Per target, inbound ports must be distinct — that IS the fix.
	inbound := map[string]map[int]bool{}
	for _, asg := range m {
		for i, tg := range asg.Targets {
			if inbound[tg] == nil {
				inbound[tg] = map[int]bool{}
			}
			if inbound[tg][asg.TargetPorts[i]] {
				t.Fatalf("two clients share port %d on target %s", asg.TargetPorts[i], tg)
			}
			inbound[tg][asg.TargetPorts[i]] = true
		}
	}

	// Legacy mode (a pre-1.3.1 node in the mesh): everything on 5201, no
	// extra listeners — the old busy-collision beats connection-refused.
	legacy := meshAssignments(self, peers, false)
	for id, asg := range legacy {
		if len(asg.ListenPorts) != 0 {
			t.Fatalf("legacy %s should open no extra listeners: %+v", id, asg)
		}
		for _, p := range asg.TargetPorts {
			if p != 5201 {
				t.Fatalf("legacy %s port = %d, want 5201", id, p)
			}
		}
	}
}

func TestPortsSupported(t *testing.T) {
	peers := []PeerInfo{{ID: "a", Version: "1.3.1"}, {ID: "b", Version: "1.3.1"}}
	if !portsSupported(peers, "1.3.1") {
		t.Fatalf("uniform mesh should support ports")
	}
	peers[1].Version = "1.2.0"
	if portsSupported(peers, "1.3.1") {
		t.Fatalf("mixed mesh must fall back to legacy single-port")
	}
	peers[1].Version = ""
	if portsSupported(peers, "1.3.1") {
		t.Fatalf("unknown-version peer must force legacy")
	}
}

func TestStressAbortPredicate(t *testing.T) {
	if shouldAbort(2) {
		t.Fatalf("2 consecutive errors should not abort")
	}
	if !shouldAbort(3) {
		t.Fatalf("3 consecutive errors should abort")
	}
}

func TestStressStartDelay(t *testing.T) {
	if d := startDelay(1000, 1500); d != 500 {
		t.Fatalf("future delay = %d us, want 500", d)
	}
	if d := startDelay(2000, 1500); d != 0 {
		t.Fatalf("past delay should clamp to 0, got %d", d)
	}
}

func TestStressLifecycleWithFakeRunner(t *testing.T) {
	a := &App{host: "ryzen"}
	var calls int64
	a.stressRunner = func(ctx context.Context, target string, o iperf.Opts, _ func(iperf.Interval)) (iperf.Result, error) {
		atomic.AddInt64(&calls, 1)
		select {
		case <-ctx.Done():
			return iperf.Result{}, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
		return iperf.Result{SumBitsPerSec: 100e6}, nil
	}

	opts := StressOpts{RunID: "r1", Targets: []string{"10.0.0.2", "10.0.0.3"}, PerLinkCapMbit: 50, Proto: "tcp", DurationS: 1, StartAtUnixUS: 0}
	if err := a.startStressLocal(opts); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := a.startStressLocal(opts); err == nil {
		t.Fatalf("duplicate start should be rejected")
	}
	time.Sleep(30 * time.Millisecond)
	st := a.stressStatusLocal()
	if !st.Running || len(st.Links) != 2 {
		t.Fatalf("expected running with 2 links, got %+v", st)
	}
	if st.Host != "ryzen" {
		t.Fatalf("status must name its source node, got %q", st.Host)
	}
	a.stopStressLocal("r1")
	time.Sleep(20 * time.Millisecond)
	if a.stressStatusLocal().Running {
		t.Fatalf("stop should end the run")
	}
	if atomic.LoadInt64(&calls) == 0 {
		t.Fatalf("runner was never called")
	}
}

func TestStressAutoAbortsFailingLink(t *testing.T) {
	a := &App{}
	a.stressRunner = func(ctx context.Context, target string, o iperf.Opts, _ func(iperf.Interval)) (iperf.Result, error) {
		if target == "bad" {
			return iperf.Result{}, errors.New("connection refused")
		}
		select {
		case <-ctx.Done():
			return iperf.Result{}, ctx.Err()
		case <-time.After(2 * time.Millisecond):
		}
		return iperf.Result{SumBitsPerSec: 100e6}, nil
	}
	_ = a.startStressLocal(StressOpts{RunID: "r2", Targets: []string{"bad", "good"}, DurationS: 2, Proto: "tcp"})
	time.Sleep(60 * time.Millisecond)
	st := a.stressStatusLocal()
	var badAborted bool
	for _, l := range st.Links {
		if l.Target == "bad" {
			badAborted = l.Aborted
		}
	}
	if !badAborted {
		t.Fatalf("the failing link should auto-abort: %+v", st.Links)
	}
	a.stopStressLocal("r2")
}

func TestLoadTargetStreamsLiveRates(t *testing.T) {
	a := &App{}
	a.stressRunner = func(ctx context.Context, target string, o iperf.Opts, onIv func(iperf.Interval)) (iperf.Result, error) {
		if onIv != nil {
			onIv(iperf.Interval{EndS: 1, BitsPerSecond: 150e6})
			onIv(iperf.Interval{EndS: 2, BitsPerSecond: 180e6})
		}
		<-ctx.Done() // hold the run open so status observes the live values
		return iperf.Result{}, ctx.Err()
	}
	_ = a.startStressLocal(StressOpts{RunID: "rl", Targets: []string{"10.0.0.2"}, DurationS: 2, Proto: "tcp"})
	time.Sleep(30 * time.Millisecond)
	st := a.stressStatusLocal()
	if len(st.Links) != 1 || st.Links[0].SentMbit != 180 || len(st.Links[0].RateHist) != 2 {
		t.Fatalf("live rates not streamed into the link: %+v", st.Links)
	}
	a.stopStressLocal("rl")
	time.Sleep(20 * time.Millisecond)
}

func TestStartStressReportsFailures(t *testing.T) {
	a := &App{nodeID: "self"}
	a.startLocalStress = func(o StressOpts) error { return errStressBusy }
	a.FetchStressStart = func(baseURL string, o StressOpts) error { return errors.New("unreachable") }
	self := PeerInfo{ID: "self", Host: "ryzen", Addr: "10.0.0.1:8088"}
	peers := []PeerInfo{{ID: "p", Host: "proj", Addr: "10.0.0.2:8088"}}
	_, count := a.StartStress(self, peers, StressParams{DurationS: 30, NowUnixUS: 1})
	if count != 0 {
		t.Fatalf("nothing accepted the run, count = %d, want 0", count)
	}
}

func TestSanitizeTargets(t *testing.T) {
	in := []string{"a", "b", "a", "", "c"}
	ports := []int{5201, 5202, 5203, 5204, 5205}
	got, gotP := sanitizeTargets(in, ports)
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("dedupe wrong: %v", got)
	}
	// Ports stay aligned with their surviving targets.
	if len(gotP) != 3 || gotP[0] != 5201 || gotP[1] != 5202 || gotP[2] != 5205 {
		t.Fatalf("ports misaligned after sanitize: %v", gotP)
	}
	// Loopbacks are misrouted self-load, never a LAN target.
	if got, _ := sanitizeTargets([]string{"127.0.0.1", "localhost", "10.0.0.2"}, nil); len(got) != 1 || got[0] != "10.0.0.2" {
		t.Fatalf("loopbacks not dropped: %v", got)
	}
	// Old orchestrator: no ports at all → zeros (iperf3 default).
	if _, p := sanitizeTargets([]string{"x", "y"}, nil); len(p) != 2 || p[0] != 0 || p[1] != 0 {
		t.Fatalf("missing ports should default to 0: %v", p)
	}
	big := make([]string, 200)
	for i := range big {
		big[i] = "t" + strconv.Itoa(i)
	}
	if got, _ := sanitizeTargets(big, nil); len(got) != stressMaxTargets {
		t.Fatalf("cap = %d, want %d", len(got), stressMaxTargets)
	}
}

func TestSanitizeListenPorts(t *testing.T) {
	in := []int{5202, 5202, 5201, 80, 99999, 5203}
	got := sanitizeListenPorts(in)
	if len(got) != 2 || got[0] != 5202 || got[1] != 5203 {
		t.Fatalf("listen ports = %v, want [5202 5203]", got)
	}
}

func TestStressSpawnsAndStopsExtraListeners(t *testing.T) {
	a := &App{}
	var mu sync.Mutex
	started, stopped := []int{}, 0
	a.stressSrv = func(port int) func() {
		mu.Lock()
		started = append(started, port)
		mu.Unlock()
		return func() { mu.Lock(); stopped++; mu.Unlock() }
	}
	a.stressRunner = func(ctx context.Context, target string, o iperf.Opts, _ func(iperf.Interval)) (iperf.Result, error) {
		if o.Port != 5202 {
			t.Errorf("client port = %d, want 5202", o.Port)
		}
		<-ctx.Done()
		return iperf.Result{}, ctx.Err()
	}
	err := a.startStressLocal(StressOpts{
		RunID: "rp", Targets: []string{"10.0.0.2"}, TargetPorts: []int{5202},
		ListenPorts: []int{5202}, DurationS: 1, Proto: "tcp",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	a.stopStressLocal("rp")
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(started) != 1 || started[0] != 5202 {
		t.Fatalf("extra listener not spawned: %v", started)
	}
	if stopped != 1 {
		t.Fatalf("extra listener not stopped with the run: %d", stopped)
	}
}

func TestStartStressFansOutPerNodeTargets(t *testing.T) {
	a := &App{nodeID: "self"}
	started := map[string]StressOpts{}
	var localOpts StressOpts
	a.startLocalStress = func(o StressOpts) error { localOpts = o; return nil }
	a.FetchStressStart = func(baseURL string, o StressOpts) error {
		started[baseURL] = o
		return nil
	}
	self := PeerInfo{ID: "self", Host: "ryzen", Addr: "10.0.0.1:8088"}
	peers := []PeerInfo{{ID: "p", Host: "proj", Addr: "10.0.0.2:8088"}}

	_, count := a.StartStress(self, peers, StressParams{PerLinkCapMbit: 100, Proto: "tcp", DurationS: 30, NowUnixUS: 1_000_000})

	if count != 2 {
		t.Fatalf("started count = %d, want 2", count)
	}
	if len(localOpts.Targets) != 1 || localOpts.Targets[0] != "10.0.0.2" {
		t.Fatalf("self targets wrong: %+v", localOpts.Targets)
	}
	po, ok := started["http://10.0.0.2:8088"]
	if !ok || len(po.Targets) != 1 || po.Targets[0] != "10.0.0.1" {
		t.Fatalf("peer command wrong: %+v", started)
	}
	if localOpts.RunID == "" || localOpts.RunID != po.RunID {
		t.Fatalf("run ids must match and be non-empty")
	}
	if localOpts.StartAtUnixUS != 1_000_000+2_000_000 {
		t.Fatalf("start_at should be now+2s, got %d", localOpts.StartAtUnixUS)
	}
}

func TestStressStartStopStatusHandlers(t *testing.T) {
	a := &App{}
	a.stressRunner = func(ctx context.Context, target string, o iperf.Opts, _ func(iperf.Interval)) (iperf.Result, error) {
		select {
		case <-ctx.Done():
			return iperf.Result{}, ctx.Err()
		case <-time.After(2 * time.Millisecond):
		}
		return iperf.Result{SumBitsPerSec: 10e6}, nil
	}
	start := stressStartHandler(a.startStressLocal)
	rr := httptest.NewRecorder()
	body := `{"run_id":"rx","targets":["10.0.0.2"],"per_link_cap_mbit":50,"proto":"tcp","duration_s":2}`
	start(rr, httptest.NewRequest(http.MethodPost, "/api/stress/start", strings.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("start status %d", rr.Code)
	}
	time.Sleep(20 * time.Millisecond)

	status := stressStatusHandler(a.stressStatusLocal)
	rr2 := httptest.NewRecorder()
	status(rr2, httptest.NewRequest(http.MethodGet, "/api/stress/status", nil))
	var st StressStatus
	_ = json.Unmarshal(rr2.Body.Bytes(), &st)
	if !st.Running {
		t.Fatalf("status should report running: %s", rr2.Body.String())
	}

	stop := stressStopHandler(a.stopStressLocal)
	rr3 := httptest.NewRecorder()
	stop(rr3, httptest.NewRequest(http.MethodPost, "/api/stress/stop", strings.NewReader(`{"run_id":"rx"}`)))
	if rr3.Code != http.StatusOK {
		t.Fatalf("stop status %d", rr3.Code)
	}
}
