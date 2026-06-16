package appcore

import "sort"

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
	Links  []LinkStat `json:"links"`
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
