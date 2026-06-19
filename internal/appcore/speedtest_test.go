package appcore

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"netlogger/internal/iperf"
)

func TestRunSpeedTestBoth(t *testing.T) {
	// Fake runner: forward run reports upload; reverse run reports download.
	run := func(target string, o iperf.Opts) (iperf.Result, error) {
		if o.Reverse {
			return iperf.Result{SumRecvBitsPerSec: 940e6, UDPJitterMs: 0, UDPLostPercent: 0}, nil
		}
		return iperf.Result{SumBitsPerSec: 887e6, SumRetransmits: 12}, nil
	}
	got := runSpeedTest(run, "10.0.0.5", SpeedReq{Direction: "both", Streams: 4, DurationS: 10, OmitS: 2})
	if got.Err != "" {
		t.Fatalf("unexpected err: %s", got.Err)
	}
	if round1(got.DownMbit) != 940 || round1(got.UpMbit) != 887 {
		t.Fatalf("down/up = %v/%v, want 940/887", got.DownMbit, got.UpMbit)
	}
	if got.Retransmits != 12 {
		t.Fatalf("retransmits = %d, want 12", got.Retransmits)
	}
}

func TestRunSpeedTestErrorSurfaces(t *testing.T) {
	run := func(target string, o iperf.Opts) (iperf.Result, error) {
		return iperf.Result{}, errors.New("iperf3 not found")
	}
	got := runSpeedTest(run, "h", SpeedReq{Direction: "down"})
	if got.Err == "" {
		t.Fatalf("expected Err to be set")
	}
}

func TestSpeedTestHandlerRoundTrip(t *testing.T) {
	run := func(target string, o iperf.Opts) (iperf.Result, error) {
		return iperf.Result{SumBitsPerSec: 500e6}, nil
	}
	h := speedTestHandler(func(req SpeedReq) SpeedResult { return runSpeedTest(run, req.Target, req) })

	body := `{"target":"10.0.0.5","direction":"up","streams":4,"duration_s":3}`
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodPost, "/api/speedtest", strings.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	var out SpeedResult
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if round1(out.UpMbit) != 500 {
		t.Fatalf("up = %v, want 500", out.UpMbit)
	}
}

func TestSpeedTestHandlerRejectsGet(t *testing.T) {
	h := speedTestHandler(func(req SpeedReq) SpeedResult { return SpeedResult{} })
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/api/speedtest", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", rr.Code)
	}
}

func TestAppSpeedTestRemoteVsLocal(t *testing.T) {
	a := &App{nodeID: "self", host: "ryzen"}
	a.localSpeed = func(req SpeedReq) SpeedResult { return SpeedResult{UpMbit: 111, Proto: "tcp"} }
	var gotURL string
	a.FetchSpeed = func(baseURL string, req SpeedReq) (SpeedResult, error) {
		gotURL = baseURL
		return SpeedResult{UpMbit: 222}, nil
	}

	local := a.SpeedTest(PeerInfo{ID: "self", Host: "ryzen", Addr: "127.0.0.1:8088"}, "10.0.0.9", SpeedReq{Direction: "up"})
	if local.UpMbit != 111 {
		t.Fatalf("self-from should run locally, got %v", local.UpMbit)
	}
	remote := a.SpeedTest(PeerInfo{ID: "p1", Host: "proj", Addr: "10.0.0.2:8088"}, "10.0.0.9", SpeedReq{Direction: "up"})
	if remote.UpMbit != 222 || gotURL != "http://10.0.0.2:8088" {
		t.Fatalf("peer-from should POST to peer, got %v url=%q", remote.UpMbit, gotURL)
	}
}

func TestSpeedNodesAndPairs(t *testing.T) {
	self := PeerInfo{ID: "self", Host: "ryzen", Addr: "127.0.0.1:8088"}
	peers := []PeerInfo{
		{ID: "b", Host: "proj", Addr: "10.0.0.2:8088"},
		{ID: "a", Host: "nas", Addr: "10.0.0.3:8088"},
	}
	nodes := speedNodes(self, peers)
	if len(nodes) != 3 || nodes[0].Host != "nas" || nodes[2].Host != "ryzen" {
		t.Fatalf("nodes not sorted by host: %+v", nodes)
	}
	pairs := speedPairs(nodes)
	if len(pairs) != 6 {
		t.Fatalf("pairs = %d, want 6", len(pairs))
	}
	for _, p := range pairs {
		if p.From.ID == p.To.ID {
			t.Fatalf("diagonal pair leaked: %+v", p)
		}
	}
}

func TestSpeedColor(t *testing.T) {
	if speedColorBucket(950) != "good" || speedColorBucket(600) != "watch" || speedColorBucket(120) != "bad" || speedColorBucket(-1) != "none" {
		t.Fatalf("color buckets wrong")
	}
}

func TestSpeedSweepRunsEveryPair(t *testing.T) {
	a := &App{nodeID: "self"}
	a.localSpeed = func(req SpeedReq) SpeedResult { return SpeedResult{DownMbit: 900} }
	a.FetchSpeed = func(baseURL string, req SpeedReq) (SpeedResult, error) { return SpeedResult{DownMbit: 500}, nil }

	self := PeerInfo{ID: "self", Host: "ryzen", Addr: "127.0.0.1:8088"}
	peers := []PeerInfo{{ID: "p", Host: "proj", Addr: "10.0.0.2:8088"}}
	m := a.SpeedSweep(self, peers, SpeedReq{Direction: "down", DurationS: 3})

	if len(m.Nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(m.Nodes))
	}
	if len(m.Cells) != 2 {
		t.Fatalf("cells = %d, want 2", len(m.Cells))
	}
	if c := m.Cells[speedKey("self", "p")]; c.DownMbit != 900 {
		t.Fatalf("self->p should be local 900, got %v", c.DownMbit)
	}
	if c := m.Cells[speedKey("p", "self")]; c.DownMbit != 500 {
		t.Fatalf("p->self should be remote 500, got %v", c.DownMbit)
	}
}
