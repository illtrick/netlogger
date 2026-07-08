package appcore

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"netlogger/internal/iperf"
)

func TestMeshTargets(t *testing.T) {
	self := PeerInfo{ID: "self", Host: "ryzen", Addr: "10.0.0.1:8088"}
	peers := []PeerInfo{
		{ID: "p", Host: "proj", Addr: "10.0.0.2:8088"},
		{ID: "s", Host: "laptop", Addr: "10.0.0.3:8088"},
	}
	m := meshTargets(self, peers)
	if len(m["self"]) != 2 || len(m["p"]) != 2 || len(m["s"]) != 2 {
		t.Fatalf("each node should target 2 others: %+v", m)
	}
	for _, ts := range m {
		for _, tg := range ts {
			if tg == "10.0.0.1:8088" {
				t.Fatalf("target still has control port: %v", tg)
			}
		}
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
	a := &App{}
	var calls int64
	a.stressRunner = func(ctx context.Context, target string, o iperf.Opts) (iperf.Result, error) {
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
	a.stressRunner = func(ctx context.Context, target string, o iperf.Opts) (iperf.Result, error) {
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
	got := sanitizeTargets(in)
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("dedupe wrong: %v", got)
	}
	// Loopbacks are misrouted self-load, never a LAN target.
	if got := sanitizeTargets([]string{"127.0.0.1", "localhost", "10.0.0.2"}); len(got) != 1 || got[0] != "10.0.0.2" {
		t.Fatalf("loopbacks not dropped: %v", got)
	}
	big := make([]string, 200)
	for i := range big {
		big[i] = "t" + strconv.Itoa(i)
	}
	if got := sanitizeTargets(big); len(got) != stressMaxTargets {
		t.Fatalf("cap = %d, want %d", len(got), stressMaxTargets)
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
	a.stressRunner = func(ctx context.Context, target string, o iperf.Opts) (iperf.Result, error) {
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
