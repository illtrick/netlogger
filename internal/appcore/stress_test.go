package appcore

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"netlogger/internal/iperf"
)

func TestMeshTargets(t *testing.T) {
	self := PeerInfo{ID: "self", Host: "ryzen", Addr: "10.0.0.1:8088"}
	peers := []PeerInfo{
		{ID: "p", Host: "proj", Addr: "10.0.0.2:8088"},
		{ID: "s", Host: "sarah", Addr: "10.0.0.3:8088"},
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
