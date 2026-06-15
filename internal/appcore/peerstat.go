package appcore

import "sync"

// peerStat is a per-peer rolling aggregator: last successful RTT (ms) and packet
// loss over the most recent recentWindow probes.
type peerStat struct {
	mu        sync.Mutex
	lastRTTms float64
	recent    []bool
}

func (s *peerStat) record(ok bool, rttms float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ok {
		s.lastRTTms = rttms
	}
	s.recent = append(s.recent, ok)
	if len(s.recent) > recentWindow {
		s.recent = s.recent[len(s.recent)-recentWindow:]
	}
}

func (s *peerStat) read() (rttms, lossPct float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.recent)
	if n == 0 {
		return 0, 0
	}
	lost := 0
	for _, ok := range s.recent {
		if !ok {
			lost++
		}
	}
	return s.lastRTTms, float64(lost) / float64(n) * 100.0
}
