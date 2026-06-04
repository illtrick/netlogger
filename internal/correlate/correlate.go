package correlate

import "sort"

// OffsetFunc returns, for an agent id, its (offsetUS, halfUncUS): the clock
// offset (agent-coordinator) and the uncertainty half-width to widen intervals.
type OffsetFunc func(agentID string) (offsetUS, halfUncUS int64)

// Corrected is an event shifted into coordinator time with an uncertainty band.
type Corrected struct {
	Event
	LoUS int64 `json:"lo_us"`
	HiUS int64 `json:"hi_us"`
}

// Group is a connected component of events whose uncertainty intervals overlap
// (single-linkage). Simultaneous is decided not by mere membership but by
// whether >=2 distinct agents are actually concurrent at some instant
// (PeakAgents), so a single over-wide interval cannot, on its own, manufacture
// a shared-device verdict from events that never truly co-occur.
type Group struct {
	Events       []Corrected `json:"events"`
	Simultaneous bool        `json:"simultaneous"` // >=2 distinct agents concurrent => shared device
	PeakAgents   int         `json:"peak_agents"`  // max distinct agents concurrent at any instant
}

// Correlate shifts events into coordinator time, widens them by clock
// uncertainty + duration, and groups those whose intervals overlap.
func Correlate(events []Event, off OffsetFunc) []Group {
	corr := make([]Corrected, 0, len(events))
	for _, e := range events {
		offset, half := off(e.AgentID)
		lo := (e.StartUS - offset) - half
		hi := (e.EndUS - offset) + half
		corr = append(corr, Corrected{Event: e, LoUS: lo, HiUS: hi})
	}
	sort.Slice(corr, func(i, j int) bool { return corr[i].LoUS < corr[j].LoUS })

	groups := []Group{}
	for _, c := range corr {
		if n := len(groups); n > 0 && c.LoUS <= groupHi(groups[n-1]) {
			groups[n-1].Events = append(groups[n-1].Events, c)
		} else {
			groups = append(groups, Group{Events: []Corrected{c}})
		}
	}
	for i := range groups {
		groups[i].PeakAgents = peakConcurrentAgents(groups[i].Events)
		groups[i].Simultaneous = groups[i].PeakAgents > 1
	}
	return groups
}

// peakConcurrentAgents sweeps the events' intervals and returns the maximum
// number of DISTINCT agents whose intervals are simultaneously active at any
// instant. Touching intervals (one's Hi == another's Lo) count as overlapping.
func peakConcurrentAgents(events []Corrected) int {
	type pt struct {
		t     int64
		delta int
		agent string
	}
	pts := make([]pt, 0, len(events)*2)
	for _, e := range events {
		pts = append(pts, pt{e.LoUS, +1, e.AgentID}, pt{e.HiUS, -1, e.AgentID})
	}
	sort.Slice(pts, func(i, j int) bool {
		if pts[i].t != pts[j].t {
			return pts[i].t < pts[j].t
		}
		return pts[i].delta > pts[j].delta // open (+1) before close (-1) at the same instant
	})
	active := map[string]int{}
	peak := 0
	for _, p := range pts {
		active[p.agent] += p.delta
		if active[p.agent] == 0 {
			delete(active, p.agent)
		}
		if len(active) > peak {
			peak = len(active)
		}
	}
	return peak
}

func groupHi(g Group) int64 {
	max := g.Events[0].HiUS
	for _, e := range g.Events {
		if e.HiUS > max {
			max = e.HiUS
		}
	}
	return max
}

