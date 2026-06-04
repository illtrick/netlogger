// Package mesh implements the agent sync API and the coordinator-side puller.
// Named "mesh" (not "sync") to avoid shadowing the standard library.
package mesh

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"netlogger/internal/store"
	"netlogger/internal/version"
)

// Info is the agent identity/health payload at /api/info.
type Info struct {
	NodeID     string `json:"node_id"`
	Host       string `json:"host"`
	Version    string `json:"version"`
	TimeUnixUS int64  `json:"time_unix_us"`
}

// AgentAPI serves an agent's local samples and identity to the coordinator.
type AgentAPI struct {
	Store  *store.Store
	NodeID string
	Host   string
}

// Info handles GET /api/info.
func (a *AgentAPI) Info(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(Info{
		NodeID:     a.NodeID,
		Host:       a.Host,
		Version:    version.Version,
		TimeUnixUS: time.Now().UTC().UnixMicro(),
	})
}

// Samples handles GET /api/samples?since=N&limit=M (defaults: since=0, limit=500).
func (a *AgentAPI) Samples(w http.ResponseWriter, r *http.Request) {
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	rows, err := a.Store.Since(since, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if rows == nil {
		rows = []store.Sample{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rows)
}

// Register mounts the agent API routes on mux.
func (a *AgentAPI) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/info", a.Info)
	mux.HandleFunc("/api/samples", a.Samples)
}
