// Package coordinator turns puller liveness + readiness results into JSON
// HTTP handlers the web server mounts.
package coordinator

import (
	"encoding/json"
	"net/http"

	"netlogger/internal/config"
	"netlogger/internal/mesh"
	"netlogger/internal/readiness"
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
