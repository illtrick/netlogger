// Package correlate detects per-path failure events and correlates them across
// hosts by uncertainty-interval overlap (spec §6, §9).
package correlate

import (
	"sort"

	"netlogger/internal/store"
)

// Event is a maximal run of consecutive losses on one (agent, src->dst) path,
// in the agent's local clock.
type Event struct {
	AgentID    string `json:"agent_id"`
	Src        string `json:"src"`
	Dst        string `json:"dst"`
	StartUS    int64  `json:"start_us"`
	EndUS      int64  `json:"end_us"`
	DurationUS int64  `json:"duration_us"`
}

// DetectEvents finds failure events per destination in an agent's samples.
func DetectEvents(agentID string, samples []store.Sample) []Event {
	byDst := map[string][]store.Sample{}
	for _, s := range samples {
		byDst[s.DstHost] = append(byDst[s.DstHost], s)
	}
	var events []Event
	for dst, rows := range byDst {
		sort.Slice(rows, func(i, j int) bool { return rows[i].TSUnixUS < rows[j].TSUnixUS })
		var cur *Event
		for _, s := range rows {
			if s.Lost {
				if cur == nil {
					cur = &Event{AgentID: agentID, Src: s.SrcHost, Dst: dst, StartUS: s.TSUnixUS, EndUS: s.TSUnixUS}
				} else {
					cur.EndUS = s.TSUnixUS
				}
			} else if cur != nil {
				cur.DurationUS = cur.EndUS - cur.StartUS
				events = append(events, *cur)
				cur = nil
			}
		}
		if cur != nil {
			cur.DurationUS = cur.EndUS - cur.StartUS
			events = append(events, *cur)
		}
	}
	sort.Slice(events, func(i, j int) bool { return events[i].StartUS < events[j].StartUS })
	return events
}
