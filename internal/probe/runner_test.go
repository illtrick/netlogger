package probe

import (
	"path/filepath"
	"testing"
	"time"

	"netlogger/internal/clock"
	"netlogger/internal/store"
)

func TestRunnerTickWritesSamples(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	// Fake pinger: "good" replies fast, "bad" is lost.
	fake := func(addr string, _ time.Duration) (Result, error) {
		if addr == "good" {
			return Result{RTT: 1200 * time.Microsecond}, nil
		}
		return Result{Lost: true}, nil
	}

	r := &Runner{
		Store:   s,
		Clock:   clock.Fixed{Micros: 5000},
		Src:     "self",
		Targets: []string{"good", "bad"},
		Ping:    fake,
		Timeout: time.Second,
	}
	if err := r.Tick(); err != nil {
		t.Fatalf("tick: %v", err)
	}

	rows, err := s.Since(0, 100)
	if err != nil {
		t.Fatalf("since: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 samples, got %d", len(rows))
	}
	byDst := map[string]store.Sample{}
	for _, r := range rows {
		byDst[r.DstHost] = r
	}
	if g := byDst["good"]; g.Lost || g.RTTus != 1200 || g.ProbeType != "icmp" {
		t.Fatalf("good sample wrong: %+v", g)
	}
	if b := byDst["bad"]; !b.Lost {
		t.Fatalf("bad sample should be Lost: %+v", byDst["bad"])
	}
}
