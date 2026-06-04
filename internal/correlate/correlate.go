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

// Group is a set of events whose uncertainty intervals overlap.
type Group struct {
	Events       []Corrected `json:"events"`
	Simultaneous bool        `json:"simultaneous"` // spans >1 agent => shared device
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

	var groups []Group
	for _, c := range corr {
		if n := len(groups); n > 0 && c.LoUS <= groupHi(groups[n-1]) {
			groups[n-1].Events = append(groups[n-1].Events, c)
		} else {
			groups = append(groups, Group{Events: []Corrected{c}})
		}
	}
	for i := range groups {
		groups[i].Simultaneous = distinctAgents(groups[i].Events) > 1
	}
	return groups
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

func distinctAgents(events []Corrected) int {
	seen := map[string]bool{}
	for _, e := range events {
		seen[e.AgentID] = true
	}
	return len(seen)
}
