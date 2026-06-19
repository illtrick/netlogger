package appcore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"

	"netlogger/internal/iperf"
)

// SpeedReq is a request to run an iperf3 client against a target.
type SpeedReq struct {
	Target    string `json:"target"`             // host:port or host of the iperf3 server to hit
	Direction string `json:"direction"`          // "down" | "up" | "both" | "bidir"
	Proto     string `json:"proto"`              // "tcp" | "udp" ("" => tcp)
	Streams   int    `json:"streams"`            // 0 => 1
	DurationS int    `json:"duration_s"`         // 0 => 10
	OmitS     int    `json:"omit_s"`             // warm-up seconds to skip
	CapMbit   int    `json:"cap_mbit,omitempty"` // UDP rate cap
}

// SpeedResult is the parsed outcome of a speed test, in Mbit/s.
type SpeedResult struct {
	DownMbit    float64 `json:"down_mbit"`
	UpMbit      float64 `json:"up_mbit"`
	Retransmits int     `json:"retransmits"`
	JitterMs    float64 `json:"jitter_ms"`
	LossPct     float64 `json:"loss_pct"`
	Proto       string  `json:"proto"`
	Err         string  `json:"err,omitempty"`
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
func mbit(bps float64) float64 { return round1(bps / 1e6) }

// runner matches iperf.RunClient; injected for tests.
type runner func(target string, o iperf.Opts) (iperf.Result, error)

// runSpeedTest executes the requested direction(s) and maps to SpeedResult.
// "both" runs upload (forward) then download (-R) sequentially.
func runSpeedTest(run runner, target string, req SpeedReq) SpeedResult {
	res := SpeedResult{Proto: req.Proto}
	if res.Proto == "" {
		res.Proto = "tcp"
	}
	base := iperf.Opts{
		DurationS:   req.DurationS,
		Streams:     req.Streams,
		OmitS:       req.OmitS,
		UDP:         req.Proto == "udp",
		BitrateMbit: req.CapMbit,
	}
	doUp := func() bool {
		r, err := run(target, base)
		if err != nil {
			res.Err = err.Error()
			return false
		}
		res.UpMbit = mbit(r.SumBitsPerSec)
		res.Retransmits += r.SumRetransmits
		res.JitterMs = r.UDPJitterMs
		res.LossPct = r.UDPLostPercent
		return true
	}
	doDown := func() bool {
		o := base
		o.Reverse = true
		r, err := run(target, o)
		if err != nil {
			res.Err = err.Error()
			return false
		}
		res.DownMbit = mbit(r.SumRecvBitsPerSec)
		res.Retransmits += r.SumRetransmits
		if r.UDPJitterMs > res.JitterMs {
			res.JitterMs = r.UDPJitterMs
		}
		if r.UDPLostPercent > res.LossPct {
			res.LossPct = r.UDPLostPercent
		}
		return true
	}
	switch req.Direction {
	case "up":
		doUp()
	case "down":
		doDown()
	case "bidir":
		o := base
		o.Bidir = true
		r, err := run(target, o)
		if err != nil {
			res.Err = err.Error()
		} else {
			res.UpMbit = mbit(r.SumBitsPerSec)
			res.DownMbit = mbit(r.SumRecvBitsPerSec)
			res.Retransmits = r.SumRetransmits
			res.JitterMs = r.UDPJitterMs
			res.LossPct = r.UDPLostPercent
		}
	default: // "both"
		if doUp() {
			doDown()
		}
	}
	return res
}

// speedTestHandler accepts POST SpeedReq and returns SpeedResult JSON.
func speedTestHandler(do func(SpeedReq) SpeedResult) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req SpeedReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Target == "" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if req.DurationS <= 0 || req.DurationS > 60 {
			req.DurationS = 10 // clamp
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(do(req))
	}
}

// postSpeedTest asks a remote node (baseURL = its control URL) to run a client.
func postSpeedTest(client *http.Client, baseURL string, req SpeedReq) (SpeedResult, error) {
	body, _ := json.Marshal(req)
	resp, err := client.Post(baseURL+"/api/speedtest", "application/json", bytes.NewReader(body))
	if err != nil {
		return SpeedResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return SpeedResult{}, fmt.Errorf("speedtest: status %d", resp.StatusCode)
	}
	var out SpeedResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return SpeedResult{}, err
	}
	return out, nil
}

// SpeedTest orchestrates a single pair: it tells the `from` node to run an
// iperf3 client against `targetAddr`. If `from` is this node, it runs locally;
// otherwise it POSTs to the peer's control plane. Orchestrator-agnostic — the
// caller need not be either endpoint.
func (a *App) SpeedTest(from PeerInfo, targetAddr string, req SpeedReq) SpeedResult {
	req.Target = targetAddr
	if from.ID == a.nodeID {
		return a.localSpeed(req)
	}
	out, err := a.FetchSpeed("http://"+from.Addr, req)
	if err != nil {
		return SpeedResult{Err: err.Error()}
	}
	return out
}

// SpeedNode is a row/column of the matrix.
type SpeedNode struct {
	ID   string
	Host string
	Addr string
}

// SpeedPair is one directed test From->To.
type SpeedPair struct {
	From SpeedNode
	To   SpeedNode
}

// SpeedMatrix is the assembled grid of completed runs, keyed From\x00To.
type SpeedMatrix struct {
	Nodes []SpeedNode
	Cells map[string]SpeedResult
}

func speedKey(from, to string) string { return from + "\x00" + to }

func toNode(p PeerInfo) SpeedNode { return SpeedNode{ID: p.ID, Host: p.Host, Addr: p.Addr} }

// speedNodes returns self + peers, sorted by host then id for a stable layout.
func speedNodes(self PeerInfo, peers []PeerInfo) []SpeedNode {
	out := []SpeedNode{toNode(self)}
	for _, p := range peers {
		out = append(out, toNode(p))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Host != out[j].Host {
			return out[i].Host < out[j].Host
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// speedPairs enumerates every directed off-diagonal pair.
func speedPairs(nodes []SpeedNode) []SpeedPair {
	var ps []SpeedPair
	for _, f := range nodes {
		for _, t := range nodes {
			if f.ID != t.ID {
				ps = append(ps, SpeedPair{From: f, To: t})
			}
		}
	}
	return ps
}

// speedColorBucket maps a download Mbit/s to a severity bucket. -1 = not run.
// Thresholds assume a 1 GbE baseline (the mesh's link speed); on faster links
// every healthy pair reads "good", which is the intended conservative default.
func speedColorBucket(mbit float64) string {
	switch {
	case mbit < 0:
		return "none"
	case mbit >= 900:
		return "good"
	case mbit >= 400:
		return "watch"
	default:
		return "bad"
	}
}

// SpeedSweep runs every directed pair and assembles a SpeedMatrix. Pairs run
// with bounded concurrency (4 at a time) so a large mesh doesn't open dozens of
// simultaneous iperf3 streams (which would distort the measurements).
func (a *App) SpeedSweep(self PeerInfo, peers []PeerInfo, req SpeedReq) SpeedMatrix {
	nodes := speedNodes(self, peers)
	pairs := speedPairs(nodes)
	cells := make(map[string]SpeedResult, len(pairs))
	byID := map[string]PeerInfo{self.ID: self}
	for _, p := range peers {
		byID[p.ID] = p
	}

	type result struct {
		key string
		res SpeedResult
	}
	sem := make(chan struct{}, 4)
	ch := make(chan result, len(pairs))
	for _, pr := range pairs {
		pr := pr
		sem <- struct{}{}
		go func() {
			defer func() { <-sem }()
			from := byID[pr.From.ID]
			ch <- result{key: speedKey(pr.From.ID, pr.To.ID), res: a.SpeedTest(from, pr.To.Addr, req)}
		}()
	}
	for range pairs {
		r := <-ch
		cells[r.key] = r.res
	}
	return SpeedMatrix{Nodes: nodes, Cells: cells}
}
