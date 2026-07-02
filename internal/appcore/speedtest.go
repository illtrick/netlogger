package appcore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net"
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
	RTTms       float64 `json:"rtt_ms"` // mean TCP RTT over the run (0 if unavailable)
	JitterMs    float64 `json:"jitter_ms"`
	LossPct     float64 `json:"loss_pct"`
	Proto       string  `json:"proto"`
	Err         string  `json:"err,omitempty"`
}

// meanRTTms averages the non-zero per-interval TCP RTTs (microseconds) into ms.
func meanRTTms(ivs []iperf.Interval) float64 {
	var sum float64
	var n int
	for _, iv := range ivs {
		if iv.RTTus > 0 {
			sum += float64(iv.RTTus)
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return round1(sum / float64(n) / 1000)
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
		if rtt := meanRTTms(r.Intervals); rtt > 0 {
			res.RTTms = rtt
		}
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
			// In bidir JSON, sum_received mirrors the upload direction; the true
			// download rate lives in sum_received_bidir_reverse. Fall back for
			// older iperf3 builds that omit the bidir_reverse block.
			down := r.SumRecvBidirBitsPerSec
			if down == 0 {
				down = r.SumRecvBitsPerSec
			}
			res.DownMbit = mbit(down)
			res.Retransmits = r.SumRetransmits
			res.RTTms = meanRTTms(r.Intervals)
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

// speedTestHandler accepts POST SpeedReq and returns SpeedResult JSON. The
// request context is threaded into the runner so a caller that stops its sweep
// (dropping the connection) also kills the iperf3 client running here.
func speedTestHandler(do func(context.Context, SpeedReq) SpeedResult) http.HandlerFunc {
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
		// Clamp everything a peer could inflate. Duration ≤30 keeps a "both" run
		// (two sequential legs) inside the orchestrator's 90s client timeout.
		if req.DurationS <= 0 || req.DurationS > 30 {
			req.DurationS = 10
		}
		if req.Streams < 0 || req.Streams > 16 {
			req.Streams = 4
		}
		if req.OmitS < 0 || req.OmitS >= req.DurationS {
			req.OmitS = 0
		}
		if req.CapMbit < 0 || req.CapMbit > 10000 {
			req.CapMbit = 0
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(do(r.Context(), req))
	}
}

// postSpeedTest asks a remote node (baseURL = its control URL) to run a client.
// Cancelling ctx drops the connection, which cancels the remote handler's
// request context and kills its iperf3 run.
func postSpeedTest(ctx context.Context, client *http.Client, baseURL string, req SpeedReq) (SpeedResult, error) {
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/speedtest", bytes.NewReader(body))
	if err != nil {
		return SpeedResult{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(httpReq)
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
// caller need not be either endpoint. Cancelling ctx aborts the run (local
// iperf3 killed; remote request dropped, which kills the remote iperf3 too).
func (a *App) SpeedTest(ctx context.Context, from PeerInfo, targetAddr string, req SpeedReq) SpeedResult {
	// Node addresses are host:controlPort (e.g. 10.0.0.2:8088). iperf3's server
	// listens on its own default port (5201) and `-c` wants a bare host, so strip
	// the control port before handing the target to iperf3.
	req.Target = iperfHost(targetAddr)
	if from.ID == a.nodeID {
		return a.localSpeed(ctx, req)
	}
	out, err := a.FetchSpeed(ctx, "http://"+from.Addr, req)
	if err != nil {
		return SpeedResult{Err: err.Error()}
	}
	return out
}

// iperfHost returns the bare host of a node address, stripping the control
// port if present (iperf3 connects to its own default port on the peer).
func iperfHost(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

// DeviceName resolves a bare host/IP (e.g. a stress/iperf target) to its device
// (host) name, or returns the input unchanged when unknown. The UI leads with the
// device name and shows the IP as smaller secondary text — see docs/design-guide.md.
func (s Snapshot) DeviceName(hostOrIP string) string {
	for _, p := range append([]PeerInfo{s.SelfPeer}, s.Peers...) {
		if p.Host == hostOrIP || iperfHost(p.Addr) == hostOrIP {
			return p.Host
		}
	}
	return hostOrIP
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

// SweepProgress is a live update from a running SpeedSweep: the completed cells
// so far, the pairs currently under test, and the done/total counts. Everything
// is a copy — the receiver may hold it across frames.
type SweepProgress struct {
	Nodes       []SpeedNode
	Cells       map[string]SpeedResult
	Active      []SpeedPair
	Done, Total int
}

// SpeedSweep runs every directed pair and assembles a SpeedMatrix. iperf3
// servers accept ONE test at a time, so no node may be an endpoint of two
// concurrent tests: pairs whose endpoints are both free launch in parallel;
// the rest wait for a completion. This keeps parallelism on larger meshes
// without the "server is busy" failures a plain semaphore produces.
// onProgress (optional) is invoked from the scheduling goroutine after every
// launch and completion, so a UI can fill the matrix in live. Cancelling ctx
// stops the sweep: no new pairs launch, in-flight runs are killed, and the
// partial result is returned (and not recorded to history).
func (a *App) SpeedSweep(ctx context.Context, self PeerInfo, peers []PeerInfo, req SpeedReq, onProgress func(SweepProgress)) SpeedMatrix {
	nodes := speedNodes(self, peers)
	pairs := speedPairs(nodes)
	total := len(pairs)
	cells := make(map[string]SpeedResult, total)
	byID := map[string]PeerInfo{self.ID: self}
	for _, p := range peers {
		byID[p.ID] = p
	}

	type result struct {
		key      string
		from, to string
		res      SpeedResult
	}
	ch := make(chan result, total)
	busy := map[string]bool{}
	active := map[string]SpeedPair{} // key → pair currently under test
	pending := pairs
	inFlight := 0
	report := func() {
		if onProgress == nil {
			return
		}
		p := SweepProgress{Nodes: nodes, Done: len(cells), Total: total,
			Cells: make(map[string]SpeedResult, len(cells))}
		for k, v := range cells {
			p.Cells[k] = v
		}
		for _, pr := range active {
			p.Active = append(p.Active, pr)
		}
		onProgress(p)
	}
	launch := func() {
		if ctx.Err() != nil {
			return // stopped — let in-flight runs drain, launch nothing new
		}
		for i := 0; i < len(pending); {
			pr := pending[i]
			if busy[pr.From.ID] || busy[pr.To.ID] {
				i++
				continue
			}
			busy[pr.From.ID], busy[pr.To.ID] = true, true
			pending = append(pending[:i], pending[i+1:]...)
			active[speedKey(pr.From.ID, pr.To.ID)] = pr
			inFlight++
			from := byID[pr.From.ID]
			go func(pr SpeedPair, from PeerInfo) {
				ch <- result{
					key: speedKey(pr.From.ID, pr.To.ID), from: pr.From.ID, to: pr.To.ID,
					res: a.SpeedTest(ctx, from, pr.To.Addr, req),
				}
			}(pr, from)
		}
	}
	launch()
	report()
	for inFlight > 0 {
		r := <-ch
		cells[r.key] = r.res
		delete(active, r.key)
		busy[r.from], busy[r.to] = false, false
		inFlight--
		launch()
		report()
	}
	if ctx.Err() == nil { // a stopped sweep is partial — don't record it
		if sum, ok := sweepSummary(nodes, cells); ok {
			a.recordTestResult(sum)
		}
	}
	return SpeedMatrix{Nodes: nodes, Cells: cells}
}
