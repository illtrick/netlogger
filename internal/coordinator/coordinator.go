// Package coordinator turns puller liveness + readiness results into JSON
// HTTP handlers the web server mounts.
package coordinator

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"netlogger/internal/classify"
	"netlogger/internal/config"
	"netlogger/internal/correlate"
	"netlogger/internal/iperf"
	"netlogger/internal/mesh"
	"netlogger/internal/readiness"
	"netlogger/internal/score"
	"netlogger/internal/store"
)

// serverError logs the full error and returns a generic message (no internal
// detail leak — review security I3).
func serverError(w http.ResponseWriter, where string, err error) {
	log.Printf("netlogger: %s: %v", where, err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

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
			// Exclude agents whose clock was measured but deemed unreliable
			// (clamped, spec §6) — they cannot be trusted for cross-host timing
			// and must not contribute to shared-device inference.
			if off, ok := offsets.Get(id); ok && !off.Reliable {
				continue
			}
			rows, err := agg.AgentSamplesAll(id)
			if err != nil {
				serverError(w, "correlation", err)
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
				serverError(w, "components", err)
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

// LoadTestResponse is the result of an /api/loadtest run.
type LoadTestResponse struct {
	OK             bool    `json:"ok"`
	Error          string  `json:"error,omitempty"`
	SumBitsPerSec  float64 `json:"sum_bits_per_second,omitempty"`
	SumRetransmits int     `json:"sum_retransmits,omitempty"`
	UDPLostPercent float64 `json:"udp_lost_percent,omitempty"`
}

// RunFunc executes a load test (injectable so the handler is testable without
// a real iperf3 binary).
type RunFunc func(target string, o iperf.Opts) (iperf.Result, error)

const maxLoadTestDurationS = 120

// LoadTestHandler runs an iperf3 load test against ?target= (which must be a
// configured node id — the allowlist resolves it to a host, so a free-form or
// flag-injecting target is rejected). POST only; duration is clamped. run=nil
// uses the real iperf3 client.
func LoadTestHandler(allowed map[string]string, run RunFunc) http.HandlerFunc {
	if run == nil {
		run = iperf.RunClient
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		host, ok := allowed[r.URL.Query().Get("target")]
		if !ok {
			writeJSON(w, LoadTestResponse{OK: false, Error: "unknown target (must be a configured node id)"})
			return
		}
		dur, _ := strconv.Atoi(r.URL.Query().Get("duration"))
		if dur <= 0 {
			dur = 10
		}
		if dur > maxLoadTestDurationS {
			dur = maxLoadTestDurationS
		}
		udp := r.URL.Query().Get("udp") == "true"
		res, err := run(host, iperf.Opts{DurationS: dur, UDP: udp})
		if err != nil {
			writeJSON(w, LoadTestResponse{OK: false, Error: err.Error()})
			return
		}
		writeJSON(w, LoadTestResponse{
			OK:             true,
			SumBitsPerSec:  res.SumBitsPerSec,
			SumRetransmits: res.SumRetransmits,
			UDPLostPercent: res.UDPLostPercent,
		})
	}
}

// ClassifyResponse carries the classifier verdicts.
type ClassifyResponse struct {
	LANvsWAN string `json:"lan_vs_wan"`
}

// TopologyHandler returns the configured nodes + links (for the GUI inventory).
func TopologyHandler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, cfg)
	}
}

// ClassifyHandler exposes the LAN-vs-WAN classifier over query params.
func ClassifyHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		gw := r.URL.Query().Get("gateway_failed") == "true"
		ext := r.URL.Query().Get("external_failed") == "true"
		writeJSON(w, ClassifyResponse{LANvsWAN: classify.LANvsWAN(gw, ext)})
	}
}
