// Package coordinator turns puller liveness + readiness results into JSON
// HTTP handlers the web server mounts.
package coordinator

import (
	"encoding/json"
	"net/http"

	"netlogger/internal/config"
	"netlogger/internal/correlate"
	"netlogger/internal/mesh"
	"netlogger/internal/readiness"
	"netlogger/internal/score"
	"netlogger/internal/store"
)

// AgentView is the per-agent liveness row for /api/agents.
type AgentView struct {
	ID             string `json:"id"`
	Online         bool   `json:"online"`
	LastSeenUnixUS int64  `json:"last_seen_unix_us"`
	LastErr        string `json:"last_err"`
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// AgentsHandler reports liveness for each node from the puller's state.
func AgentsHandler(p *mesh.Puller, nodes []config.TargetRef) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		views := []AgentView{}
		if p != nil {
			for _, n := range nodes {
				st := p.State(n.ID)
				var seen int64
				if !st.LastSeen.IsZero() {
					seen = st.LastSeen.UnixMicro()
				}
				views = append(views, AgentView{ID: n.ID, Online: st.Online, LastSeenUnixUS: seen, LastErr: st.LastErr})
			}
		}
		writeJSON(w, views)
	}
}

// ReadinessHandler runs the readiness checks for the given nodes on demand.
func ReadinessHandler(c *readiness.Checker, nodes []config.Node) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out := []readiness.Result{}
		for _, n := range nodes {
			out = append(out, c.Check(n))
		}
		writeJSON(w, out)
	}
}

// CorrelationHandler detects events across all aggregated agents and correlates
// them, returning the groups as JSON.
func CorrelationHandler(agg *store.Store, agentIDs []string, offsets *mesh.Offsets) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var events []correlate.Event
		for _, id := range agentIDs {
			rows, err := agg.AgentSamplesAll(id)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			events = append(events, correlate.DetectEvents(id, rows)...)
		}
		groups := correlate.Correlate(events, func(id string) (int64, int64) {
			if off, ok := offsets.Get(id); ok && off.Reliable {
				return off.OffsetUS, off.HalfUncUS()
			}
			return 0, 1000 // unknown clock: 1ms floor
		})
		writeJSON(w, groups)
	}
}

// ComponentsHandler scores every component from the aggregated samples: a
// host-pair is "tested" if it has any sample and "failing" if it has any event.
func ComponentsHandler(agg *store.Store, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tested := map[string]bool{}
		failing := map[string]bool{}
		for _, n := range cfg.Nodes {
			if n.Address == "" {
				continue
			}
			rows, err := agg.AgentSamplesAll(n.ID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			for _, s := range rows {
				tested[score.Key(s.SrcHost, s.DstHost)] = true
			}
			for _, e := range correlate.DetectEvents(n.ID, rows) {
				failing[score.Key(e.Src, e.Dst)] = true
			}
		}
		writeJSON(w, score.Score(cfg, tested, failing))
	}
}
