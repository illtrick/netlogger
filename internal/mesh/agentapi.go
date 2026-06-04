// Package mesh implements the agent sync API and the coordinator-side puller.
// Named "mesh" (not "sync") to avoid shadowing the standard library.
package mesh

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"netlogger/internal/store"
	"netlogger/internal/version"
)

// Info is the agent identity/health payload at /api/info.
type Info struct {
	NodeID        string `json:"node_id"`
	Host          string `json:"host"`
	Version       string `json:"version"`
	TimeUnixUS    int64  `json:"time_unix_us"`
	Iperf3Version string `json:"iperf3_version"` // "" if not installed
	DataWritable  bool   `json:"data_writable"`
}

// AgentAPI serves an agent's local samples and identity to the coordinator.
type AgentAPI struct {
	Store         *store.Store
	NodeID        string
	Host          string
	Iperf3Version string
	DataWritable  bool
}

// Info handles GET /api/info.
func (a *AgentAPI) Info(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(Info{
		NodeID:        a.NodeID,
		Host:          a.Host,
		Version:       version.Version,
		TimeUnixUS:    time.Now().UTC().UnixMicro(),
		Iperf3Version: a.Iperf3Version,
		DataWritable:  a.DataWritable,
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

// TimePair carries the agent's receive (T2) and send (T3) timestamps for the
// NTP-style 4-timestamp offset handshake.
type TimePair struct {
	T2UnixUS int64 `json:"t2_unix_us"`
	T3UnixUS int64 `json:"t3_unix_us"`
}

// Time handles GET /api/time. T2 is recorded on entry, T3 just before sending.
func (a *AgentAPI) Time(w http.ResponseWriter, r *http.Request) {
	t2 := time.Now().UTC().UnixMicro()
	w.Header().Set("Content-Type", "application/json")
	t3 := time.Now().UTC().UnixMicro()
	_ = json.NewEncoder(w).Encode(TimePair{T2UnixUS: t2, T3UnixUS: t3})
}

// Register mounts the agent API routes on mux.
func (a *AgentAPI) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/info", a.Info)
	mux.HandleFunc("/api/samples", a.Samples)
	mux.HandleFunc("/api/time", a.Time)
}

// FetchInfo GETs {baseURL}/api/info and decodes it.
func FetchInfo(client *http.Client, baseURL string) (Info, error) {
	resp, err := client.Get(baseURL + "/api/info")
	if err != nil {
		return Info{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Info{}, fmt.Errorf("info status %d", resp.StatusCode)
	}
	var info Info
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return Info{}, err
	}
	return info, nil
}
