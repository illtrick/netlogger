package appcore

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// LinkStat is one directed link's current quality, as measured by the source node.
type LinkStat struct {
	PeerID   string  `json:"peer_id"`
	RTTms    float64 `json:"rtt_ms"`
	JitterMs float64 `json:"jitter_ms"`
	LossPct  float64 `json:"loss_pct"`
	Drops    int     `json:"drops"`
}

// LinkReport is a node's view of its own outbound links (served at /api/links).
type LinkReport struct {
	NodeID string     `json:"node_id"`
	Host   string     `json:"host"`
	Build  string     `json:"build"` // binary identity; lets peers detect mesh build skew
	Links  []LinkStat `json:"links"`
}

// buildWarning returns a human-facing notice when any peer reports a build
// string different from this node's — meaning the mesh is running mismatched
// binaries and cross-machine features (synchronized reset, new probes) may
// silently fail. Empty when all known peer builds match (or none is reported,
// e.g. a peer too old to send the field).
func buildWarning(self string, reps map[string]LinkReport) string {
	seen := map[string]bool{}
	var mism []string
	for _, r := range reps {
		if r.Build == "" || r.Build == self || seen[r.Build] {
			continue
		}
		seen[r.Build] = true
		label := r.Host
		if label == "" {
			label = r.NodeID
		}
		mism = append(mism, fmt.Sprintf("%s on %s", label, r.Build))
	}
	if len(mism) == 0 {
		return ""
	}
	sort.Strings(mism)
	return fmt.Sprintf("build mismatch — you: %s; %s. Redeploy the same exe everywhere.",
		self, strings.Join(mism, ", "))
}

// MatrixNode is one node (a row and a column of the matrix).
type MatrixNode struct {
	ID   string
	Host string
}

// MatrixCell is one directed link src->dst in the matrix.
type MatrixCell struct {
	SrcID    string
	DstID    string
	RTTms    float64
	JitterMs float64
	LossPct  float64
	Drops    int
}

// Matrix is the assembled N×N view of all directed links.
type Matrix struct {
	Nodes []MatrixNode
	cells map[string]MatrixCell // key src+"\x00"+dst
}

func cellKey(src, dst string) string { return src + "\x00" + dst }

// Cell returns the directed link src->dst, if measured.
func (m Matrix) Cell(src, dst string) (MatrixCell, bool) {
	c, ok := m.cells[cellKey(src, dst)]
	return c, ok
}

// assembleMatrix combines this node's report with every peer's report into the
// full directed-link matrix. Nodes are sorted by host (then id) for a stable layout.
func assembleMatrix(own LinkReport, peers map[string]LinkReport) Matrix {
	nodes := map[string]MatrixNode{own.NodeID: {ID: own.NodeID, Host: own.Host}}
	cells := make(map[string]MatrixCell)
	add := func(r LinkReport) {
		if r.NodeID == "" {
			return
		}
		nodes[r.NodeID] = MatrixNode{ID: r.NodeID, Host: r.Host}
		for _, l := range r.Links {
			if l.PeerID == "" || l.PeerID == r.NodeID {
				continue
			}
			cells[cellKey(r.NodeID, l.PeerID)] = MatrixCell{
				SrcID: r.NodeID, DstID: l.PeerID,
				RTTms: l.RTTms, JitterMs: l.JitterMs, LossPct: l.LossPct, Drops: l.Drops,
			}
		}
	}
	add(own)
	for _, r := range peers {
		add(r)
	}
	out := make([]MatrixNode, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Host != out[j].Host {
			return out[i].Host < out[j].Host
		}
		return out[i].ID < out[j].ID
	})
	return Matrix{Nodes: out, cells: cells}
}

// linksHandler serves this node's current LinkReport as JSON. report is a
// callback so the handler always reflects live stats.
func linksHandler(report func() LinkReport) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(report())
	}
}

// fetchLinks GETs a peer's /api/links and decodes the LinkReport.
func fetchLinks(client *http.Client, baseURL string) (LinkReport, error) {
	resp, err := client.Get(baseURL + "/api/links")
	if err != nil {
		return LinkReport{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return LinkReport{}, fmt.Errorf("links: status %d", resp.StatusCode)
	}
	var rep LinkReport
	if err := json.NewDecoder(resp.Body).Decode(&rep); err != nil {
		return LinkReport{}, fmt.Errorf("links decode: %w", err)
	}
	return rep, nil
}
