package appcore

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
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
// It also beacons this node's build identity so peers can detect an incompatible
// or incompletely-rolled-out mesh. Version is the compatibility contract;
// Platform and Build exist so an expected cross-OS binary difference is not
// mistaken for a real mismatch. Version/Platform are absent from pre-1.1 peers.
type LinkReport struct {
	NodeID        string     `json:"node_id"`
	Host          string     `json:"host"`
	Version       string     `json:"version,omitempty"`         // semver release, the mesh compatibility contract
	Platform      string     `json:"platform,omitempty"`        // GOOS/GOARCH, e.g. "windows/amd64"
	Build         string     `json:"build"`                     // exact git commit; same-OS rollout skew only
	LinkSpeedMbit int        `json:"link_speed_mbit,omitempty"` // this node's fastest Up NIC; 0 → old peer, grade absolute
	Links         []LinkStat `json:"links"`
}

// parseLinkSpeedMbit converts nicstat's LinkSpeed vocabulary ("2.5 Gbps",
// "1 Gbps", "100 Mbps") to Mbit/s; 0 when unknown.
func parseLinkSpeedMbit(s string) int {
	fields := strings.Fields(s)
	if len(fields) < 2 {
		return 0
	}
	val, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	switch strings.ToLower(fields[1]) {
	case "gbps":
		return int(val * 1000)
	case "mbps":
		return int(val)
	}
	return 0
}

// buildID is a node's self-identity for the mesh compatibility check.
type buildID struct {
	Version  string
	Build    string
	Platform string
}

// meshWarning returns a human-facing notice when the mesh is running binaries
// that either can't interoperate or aren't the same rollout. It reasons about
// three levels so a legitimate cross-platform mesh (a Mac and a PC on the same
// release) never false-alarms:
//
//   - Version mismatch (a peer on a different — or too-old-to-report — release)
//     is the real hazard: cross-machine features (synchronized reset, new
//     probes) may silently fail. This wins and asks the user to align versions.
//   - Otherwise, if every version matches but a SAME-PLATFORM peer runs a
//     different git build, that's an incomplete rollout on that OS — a softer
//     "redeploy the latest build" nudge.
//   - A same-version peer on a DIFFERENT platform is expected to differ and is
//     never flagged.
//
// Empty when the mesh is consistent.
func meshWarning(self buildID, reps map[string]LinkReport) string {
	var verMism, buildSkew []string
	seenVer, seenSkew := map[string]bool{}, map[string]bool{}
	for _, r := range reps {
		label := r.Host
		if label == "" {
			label = r.NodeID
		}
		switch {
		case r.Version == "": // pre-1.1 peer that can't report a version → older build
			if !seenVer["\x00"+label] {
				seenVer["\x00"+label] = true
				verMism = append(verMism, fmt.Sprintf("%s runs an older build", label))
			}
		case r.Version != self.Version:
			key := r.Version + "\x00" + label
			if !seenVer[key] {
				seenVer[key] = true
				verMism = append(verMism, fmt.Sprintf("%s runs %s (%s)", label, r.Version, platformOr(r.Platform)))
			}
		case r.Platform == self.Platform && r.Build != "" && r.Build != self.Build:
			// Same release, same OS, different commit → incomplete rollout.
			if !seenSkew[r.Build] {
				seenSkew[r.Build] = true
				buildSkew = append(buildSkew, fmt.Sprintf("%s (%s)", label, r.Build))
			}
		}
	}

	if len(verMism) > 0 {
		sort.Strings(verMism)
		return fmt.Sprintf("version mismatch — this node runs NetLogger %s (%s); %s. Update every node to %s.",
			versionOr(self.Version), platformOr(self.Platform), strings.Join(verMism, ", "), versionOr(self.Version))
	}
	if len(buildSkew) > 0 {
		sort.Strings(buildSkew)
		return fmt.Sprintf("build skew — some %s nodes run a different %s build: %s. Redeploy the latest build to all %s nodes.",
			platformOr(self.Platform), versionOr(self.Version), strings.Join(buildSkew, ", "), platformOr(self.Platform))
	}
	return ""
}

// versionOr / platformOr render an unset field readably in the warning text.
func versionOr(v string) string {
	if v == "" {
		return "an unknown version"
	}
	return v
}

func platformOr(p string) string {
	if p == "" {
		return "unknown platform"
	}
	return p
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
