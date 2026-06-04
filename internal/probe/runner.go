package probe

import (
	"time"

	"netlogger/internal/clock"
	"netlogger/internal/store"
)

// PingFunc abstracts the ICMP probe so the runner is testable with a fake.
type PingFunc func(addr string, timeout time.Duration) (Result, error)

// Runner probes a set of targets and writes one sample per target per Tick.
type Runner struct {
	Store   *store.Store
	Clock   clock.Clock
	Src     string
	Targets []string
	Ping    PingFunc
	Timeout time.Duration
}

// Tick probes every target once and persists the results.
func (r *Runner) Tick() error {
	for _, target := range r.Targets {
		res, err := r.Ping(target, r.Timeout)
		sm := store.Sample{
			TSUnixUS:  r.Clock.NowUnixMicro(),
			ProbeType: "icmp",
			SrcHost:   r.Src,
			DstHost:   target,
			Direction: "rtt",
		}
		if err != nil || res.Lost {
			sm.Lost = true
		} else {
			sm.RTTus = res.RTT.Microseconds()
		}
		if _, err := r.Store.Insert(sm); err != nil {
			return err
		}
	}
	return nil
}
