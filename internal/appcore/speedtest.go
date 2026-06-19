package appcore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"

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
