package appcore

import (
	"sync"

	"netlogger/internal/probe"
)

// udpStat aggregates high-rate UDP probe bursts for one peer: the latest burst's
// RTT/jitter/loss plus a cumulative count of bursts that saw any loss (micro-drop
// episodes — the signal that survives across a long session).
type udpStat struct {
	mu        sync.Mutex
	lastRTTms float64
	jitterMs  float64
	lossPct   float64
	episodes  int
	bursts    int
}

func (s *udpStat) record(st probe.UDPStats) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastRTTms = float64(st.AvgRTT.Microseconds()) / 1000.0
	s.jitterMs = float64(st.Jitter.Microseconds()) / 1000.0
	s.lossPct = st.LossPct
	s.bursts++
	if st.LossPct > 0 {
		s.episodes++
	}
}

func (s *udpStat) read() (rttms, jitterms, lossPct float64, episodes int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastRTTms, s.jitterMs, s.lossPct, s.episodes
}
