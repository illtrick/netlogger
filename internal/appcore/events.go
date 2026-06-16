package appcore

import (
	"log"
	"time"

	"netlogger/internal/discovery"
)

// Event hysteresis thresholds (UDP loss %).
const (
	degradeEnterPct = 1.0
	degradeExitPct  = 0.2
	degradeEnterN   = 2 // consecutive lossy samples to enter
)

// linkState tracks healthy/degraded for one link with hysteresis so a single
// stray lossy burst doesn't flap the event log.
type linkState struct {
	degraded bool
	hiCount  int
}

// step folds one loss% sample in and reports whether the state changed and the
// new degraded value.
func (s *linkState) step(lossPct float64) (changed bool, degraded bool) {
	if !s.degraded {
		if lossPct >= degradeEnterPct {
			s.hiCount++
			if s.hiCount >= degradeEnterN {
				s.degraded = true
				s.hiCount = 0
				return true, true
			}
		} else {
			s.hiCount = 0
		}
		return false, false
	}
	// currently degraded: recover when loss drops below the exit threshold
	if lossPct < degradeExitPct {
		s.degraded = false
		s.hiCount = 0
		return true, false
	}
	return false, true
}

// eventRingCap bounds the in-memory recent-event ring shown in the UI.
const eventRingCap = 100

func (a *App) recordEvent(online bool, detail string) {
	log.Printf("event: %s", detail)
	now := time.Now().UnixMicro()
	if a.store != nil {
		_ = a.store.InsertConnectivityEvent(now, a.nodeID, online, detail)
	}
	a.eventMu.Lock()
	a.events = append(a.events, EventInfo{UnixMicro: now, Online: online, Detail: detail})
	if len(a.events) > eventRingCap {
		a.events = a.events[len(a.events)-eventRingCap:]
	}
	a.eventMu.Unlock()
}

// recentEvents returns a copy of the in-memory event ring, oldest first.
func (a *App) recentEvents() []EventInfo {
	a.eventMu.Lock()
	defer a.eventMu.Unlock()
	out := make([]EventInfo, len(a.events))
	copy(out, a.events)
	return out
}

func (a *App) linkStateFor(id string) *linkState {
	a.peerMu.Lock()
	defer a.peerMu.Unlock()
	s := a.linkStates[id]
	if s == nil {
		s = &linkState{}
		a.linkStates[id] = s
	}
	return s
}

func peerLabel(p discovery.Peer) string {
	if p.Host != "" {
		return p.Host
	}
	return p.ID
}
