package appcore

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"netlogger/internal/iperf"
)

func TestRunSpeedTestBoth(t *testing.T) {
	// Fake runner: forward run reports upload; reverse run reports download.
	run := func(target string, o iperf.Opts, _ func(iperf.Interval)) (iperf.Result, error) {
		if o.Reverse {
			return iperf.Result{SumRecvBitsPerSec: 940e6, UDPJitterMs: 0, UDPLostPercent: 0}, nil
		}
		return iperf.Result{SumBitsPerSec: 887e6, SumRetransmits: 12}, nil
	}
	got := runSpeedTest(run, "10.0.0.5", SpeedReq{Direction: "both", Streams: 4, DurationS: 10, OmitS: 2}, nil)
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
	run := func(target string, o iperf.Opts, _ func(iperf.Interval)) (iperf.Result, error) {
		return iperf.Result{}, errors.New("iperf3 not found")
	}
	got := runSpeedTest(run, "h", SpeedReq{Direction: "down"}, nil)
	if got.Err == "" {
		t.Fatalf("expected Err to be set")
	}
}

func TestSpeedTestHandlerRoundTrip(t *testing.T) {
	run := func(target string, o iperf.Opts, _ func(iperf.Interval)) (iperf.Result, error) {
		return iperf.Result{SumBitsPerSec: 500e6}, nil
	}
	h := speedTestHandler(func(_ context.Context, req SpeedReq, _ func(LivePoint)) SpeedResult { return runSpeedTest(run, req.Target, req, nil) })

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

func TestIsLoopbackTarget(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"127.0.0.1", true},
		{"127.0.0.1:8088", true},
		{"::1", true},
		{"localhost", true},
		{"LOCALHOST", true},
		{"192.168.0.42", false},
		{"10.0.0.2:8088", false},
		{"some-host", false},
	}
	for _, c := range cases {
		if got := isLoopbackTarget(c.in); got != c.want {
			t.Errorf("isLoopbackTarget(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestSpeedTestHandlerRejectsLoopbackTarget(t *testing.T) {
	ran := false
	h := speedTestHandler(func(_ context.Context, req SpeedReq, _ func(LivePoint)) SpeedResult { ran = true; return SpeedResult{} })
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodPost, "/api/speedtest",
		strings.NewReader(`{"target":"127.0.0.1","direction":"down"}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	var out SpeedResult
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Err == "" || ran {
		t.Fatalf("loopback target must be refused without running (err=%q ran=%v)", out.Err, ran)
	}
}

func TestSpeedTestHandlerRejectsGet(t *testing.T) {
	h := speedTestHandler(func(_ context.Context, req SpeedReq, _ func(LivePoint)) SpeedResult { return SpeedResult{} })
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/api/speedtest", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", rr.Code)
	}
}

func TestSpeedTestHandlerStreamsNDJSON(t *testing.T) {
	h := speedTestHandler(func(_ context.Context, req SpeedReq, onLive func(LivePoint)) SpeedResult {
		onLive(LivePoint{Phase: "up", Sec: 1, Mbit: 950})
		onLive(LivePoint{Phase: "up", Sec: 2, Mbit: 948})
		return SpeedResult{UpMbit: 949}
	})
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodPost, "/api/speedtest",
		strings.NewReader(`{"target":"10.0.0.5","direction":"up","stream":true}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	lines := strings.Split(strings.TrimSpace(rr.Body.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %d, want 2 live + 1 result: %q", len(lines), rr.Body.String())
	}
	var first, last streamMsg
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil || first.Live == nil || first.Live.Mbit != 950 {
		t.Fatalf("first line not a live point: %s", lines[0])
	}
	if err := json.Unmarshal([]byte(lines[2]), &last); err != nil || last.Result == nil || last.Result.UpMbit != 949 {
		t.Fatalf("last line not the result: %s", lines[2])
	}
}

func TestPostSpeedTestStreamed(t *testing.T) {
	srv := httptest.NewServer(speedTestHandler(func(_ context.Context, req SpeedReq, onLive func(LivePoint)) SpeedResult {
		if !req.Stream {
			t.Errorf("stream flag not forwarded")
		}
		onLive(LivePoint{Phase: "down", Sec: 1, Mbit: 2300})
		return SpeedResult{DownMbit: 2310, DownIvs: []float64{2300}}
	}))
	defer srv.Close()
	var live []LivePoint
	out, err := postSpeedTest(context.Background(), srv.Client(), srv.URL, SpeedReq{Target: "10.0.0.9"},
		func(p LivePoint) { live = append(live, p) })
	if err != nil {
		t.Fatalf("postSpeedTest: %v", err)
	}
	if out.DownMbit != 2310 || len(out.DownIvs) != 1 {
		t.Fatalf("result wrong: %+v", out)
	}
	if len(live) != 1 || live[0].Mbit != 2300 {
		t.Fatalf("live points wrong: %+v", live)
	}
}

func TestPostSpeedTestOldPeerBareResult(t *testing.T) {
	// A pre-1.2 peer ignores the stream flag and returns one bare SpeedResult.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"down_mbit":941,"up_mbit":887,"proto":"tcp"}`))
	}))
	defer srv.Close()
	out, err := postSpeedTest(context.Background(), srv.Client(), srv.URL, SpeedReq{Target: "10.0.0.9"},
		func(LivePoint) {})
	if err != nil {
		t.Fatalf("postSpeedTest: %v", err)
	}
	if out.DownMbit != 941 || out.UpMbit != 887 {
		t.Fatalf("bare result not decoded: %+v", out)
	}
}

func TestRunSpeedTestEmitsLiveAndIntervals(t *testing.T) {
	run := func(target string, o iperf.Opts, onIv func(iperf.Interval)) (iperf.Result, error) {
		ivs := []iperf.Interval{
			{EndS: 1, BitsPerSecond: 900e6, Retransmits: 1},
			{EndS: 2, BitsPerSecond: 950e6},
		}
		for _, iv := range ivs {
			if onIv != nil {
				onIv(iv)
			}
		}
		return iperf.Result{Intervals: ivs, SumBitsPerSec: 925e6, SumRecvBitsPerSec: 925e6}, nil
	}
	var live []LivePoint
	got := runSpeedTest(run, "h", SpeedReq{Direction: "both"}, func(p LivePoint) { live = append(live, p) })
	if got.Err != "" {
		t.Fatalf("err: %s", got.Err)
	}
	if len(live) != 4 { // two legs x two intervals
		t.Fatalf("live points = %d, want 4: %+v", len(live), live)
	}
	if live[0].Phase != "up" || live[2].Phase != "down" {
		t.Fatalf("phases wrong: %+v", live)
	}
	if live[0].Mbit != 900 || live[0].Retr != 1 {
		t.Fatalf("first point wrong: %+v", live[0])
	}
	if len(got.UpIvs) != 2 || len(got.DownIvs) != 2 || got.UpIvs[1] != 950 {
		t.Fatalf("interval series wrong: up=%v down=%v", got.UpIvs, got.DownIvs)
	}
}

func TestSpeedSweepReportsLivePoints(t *testing.T) {
	a := &App{nodeID: "self"}
	a.localSpeed = func(_ context.Context, req SpeedReq, onLive func(LivePoint)) SpeedResult {
		if onLive != nil {
			onLive(LivePoint{Phase: "down", Sec: 1, Mbit: 1200})
		}
		time.Sleep(20 * time.Millisecond) // let the scheduler drain the live channel
		return SpeedResult{DownMbit: 1200}
	}
	a.FetchSpeed = func(_ context.Context, baseURL string, req SpeedReq, onLive func(LivePoint)) (SpeedResult, error) {
		if onLive != nil {
			onLive(LivePoint{Phase: "down", Sec: 1, Mbit: 800})
		}
		time.Sleep(20 * time.Millisecond)
		return SpeedResult{DownMbit: 800}, nil
	}
	self := PeerInfo{ID: "self", Host: "a", Addr: "10.0.0.1:8088"}
	peers := []PeerInfo{{ID: "p", Host: "b", Addr: "10.0.0.2:8088"}}
	sawLive := false
	m := a.SpeedSweep(context.Background(), self, peers, SpeedReq{Direction: "down"}, func(p SweepProgress) {
		if len(p.Live) > 0 {
			sawLive = true
		}
	})
	if len(m.Cells) != 2 {
		t.Fatalf("cells = %d, want 2", len(m.Cells))
	}
	if !sawLive {
		t.Fatalf("no progress report carried a live point")
	}
}

func TestAppSpeedTestRemoteVsLocal(t *testing.T) {
	a := &App{nodeID: "self", host: "ryzen"}
	a.localSpeed = func(_ context.Context, req SpeedReq, _ func(LivePoint)) SpeedResult { return SpeedResult{UpMbit: 111, Proto: "tcp"} }
	var gotURL string
	a.FetchSpeed = func(_ context.Context, baseURL string, req SpeedReq, _ func(LivePoint)) (SpeedResult, error) {
		gotURL = baseURL
		return SpeedResult{UpMbit: 222}, nil
	}

	local := a.SpeedTest(context.Background(), PeerInfo{ID: "self", Host: "ryzen", Addr: "127.0.0.1:8088"}, "10.0.0.9", SpeedReq{Direction: "up"}, nil)
	if local.UpMbit != 111 {
		t.Fatalf("self-from should run locally, got %v", local.UpMbit)
	}
	remote := a.SpeedTest(context.Background(), PeerInfo{ID: "p1", Host: "proj", Addr: "10.0.0.2:8088"}, "10.0.0.9", SpeedReq{Direction: "up"}, nil)
	if remote.UpMbit != 222 || gotURL != "http://10.0.0.2:8088" {
		t.Fatalf("peer-from should POST to peer, got %v url=%q", remote.UpMbit, gotURL)
	}
}

func TestSpeedTestStripsControlPortForIperf(t *testing.T) {
	a := &App{nodeID: "self"}
	var gotTarget string
	a.localSpeed = func(_ context.Context, req SpeedReq, _ func(LivePoint)) SpeedResult { gotTarget = req.Target; return SpeedResult{} }
	// Self is the From node → runs locally; the iperf target must be the bare host
	// (control port 8088 stripped) so iperf3 hits the peer's :5201 server.
	a.SpeedTest(context.Background(), PeerInfo{ID: "self"}, "10.0.0.2:8088", SpeedReq{Direction: "down"}, nil)
	if gotTarget != "10.0.0.2" {
		t.Fatalf("iperf target = %q, want 10.0.0.2 (control port stripped)", gotTarget)
	}
	if iperfHost("bare-host") != "bare-host" {
		t.Fatalf("bare host should pass through unchanged")
	}
}

func TestSnapshotDeviceName(t *testing.T) {
	s := Snapshot{
		SelfPeer: PeerInfo{Host: "ryzen", Addr: "10.0.0.1:8088"},
		Peers:    []PeerInfo{{Host: "ProjectorPC", Addr: "192.168.0.127:8088"}},
	}
	if got := s.DeviceName("192.168.0.127"); got != "ProjectorPC" {
		t.Fatalf("ip → name = %q, want ProjectorPC", got)
	}
	if got := s.DeviceName("10.0.0.1"); got != "ryzen" {
		t.Fatalf("self ip → name = %q, want ryzen", got)
	}
	if got := s.DeviceName("8.8.8.8"); got != "8.8.8.8" {
		t.Fatalf("unknown should pass through, got %q", got)
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

func TestRunSpeedTestBothDownLegFails(t *testing.T) {
	// Up leg succeeds, down leg (-R) fails: keep the up number, surface the error,
	// leave down at zero (partial-result contract).
	run := func(target string, o iperf.Opts, _ func(iperf.Interval)) (iperf.Result, error) {
		if o.Reverse {
			return iperf.Result{}, errors.New("down failed")
		}
		return iperf.Result{SumBitsPerSec: 800e6}, nil
	}
	got := runSpeedTest(run, "h", SpeedReq{Direction: "both"}, nil)
	if round1(got.UpMbit) != 800 {
		t.Fatalf("up should still be measured, got %v", got.UpMbit)
	}
	if got.Err == "" {
		t.Fatalf("down failure should set Err")
	}
	if got.DownMbit != 0 {
		t.Fatalf("down should be 0 on failure, got %v", got.DownMbit)
	}
}

func TestSpeedTestHandlerClampsDuration(t *testing.T) {
	var seen SpeedReq
	h := speedTestHandler(func(_ context.Context, req SpeedReq, _ func(LivePoint)) SpeedResult { seen = req; return SpeedResult{} })
	for _, body := range []string{
		`{"target":"h","direction":"up","duration_s":0}`,
		`{"target":"h","direction":"up","duration_s":120}`,
	} {
		rr := httptest.NewRecorder()
		h(rr, httptest.NewRequest(http.MethodPost, "/api/speedtest", strings.NewReader(body)))
		if seen.DurationS != 10 {
			t.Fatalf("duration not clamped to 10 for %q, got %d", body, seen.DurationS)
		}
	}
}

func TestSpeedSweepZeroPeers(t *testing.T) {
	a := &App{nodeID: "self"}
	a.localSpeed = func(_ context.Context, req SpeedReq, _ func(LivePoint)) SpeedResult { return SpeedResult{} }
	m := a.SpeedSweep(context.Background(), PeerInfo{ID: "self", Host: "ryzen", Addr: "127.0.0.1:8088"}, nil, SpeedReq{Direction: "down"}, nil)
	if len(m.Nodes) != 1 || len(m.Cells) != 0 {
		t.Fatalf("zero-peer sweep: nodes=%d cells=%d, want 1/0", len(m.Nodes), len(m.Cells))
	}
}

func TestRunSpeedTestBidirUsesReverseField(t *testing.T) {
	run := func(target string, o iperf.Opts, _ func(iperf.Interval)) (iperf.Result, error) {
		if !o.Bidir {
			t.Fatalf("bidir direction must set Opts.Bidir")
		}
		// sum_received (101) mirrors the upload; the true download (940) is in
		// the bidir_reverse block — DownMbit must come from the latter.
		return iperf.Result{SumBitsPerSec: 100e6, SumRecvBitsPerSec: 101e6, SumRecvBidirBitsPerSec: 940e6}, nil
	}
	got := runSpeedTest(run, "h", SpeedReq{Direction: "bidir"}, nil)
	if round1(got.DownMbit) != 940 {
		t.Fatalf("bidir down = %v, want 940 (from bidir_reverse)", got.DownMbit)
	}
	if round1(got.UpMbit) != 100 {
		t.Fatalf("bidir up = %v, want 100", got.UpMbit)
	}
}

func TestSpeedSweepEndpointExclusive(t *testing.T) {
	// iperf3 servers run one test at a time: no node may be an endpoint of two
	// concurrent tests. Fakes track live endpoint usage and fail on overlap.
	var mu sync.Mutex
	active := map[string]int{}
	enter := func(from, to string) {
		mu.Lock()
		active[from]++
		active[to]++
		if active[from] > 1 || active[to] > 1 {
			mu.Unlock()
			t.Errorf("endpoint used by two concurrent tests: %v", active)
			return
		}
		mu.Unlock()
	}
	leave := func(from, to string) {
		mu.Lock()
		active[from]--
		active[to]--
		mu.Unlock()
	}

	// Normalize every endpoint reference to its bare IP so a node colliding as
	// client in one test and server in another is still detected.
	norm := func(s string) string {
		s = strings.TrimPrefix(s, "http://")
		return iperfHost(s)
	}

	a := &App{nodeID: "self"}
	a.localSpeed = func(_ context.Context, req SpeedReq, _ func(LivePoint)) SpeedResult {
		enter("10.0.0.1", req.Target)
		time.Sleep(5 * time.Millisecond)
		leave("10.0.0.1", req.Target)
		return SpeedResult{DownMbit: 900}
	}
	a.FetchSpeed = func(_ context.Context, baseURL string, req SpeedReq, _ func(LivePoint)) (SpeedResult, error) {
		from := norm(baseURL)
		enter(from, req.Target)
		time.Sleep(5 * time.Millisecond)
		leave(from, req.Target)
		return SpeedResult{DownMbit: 500}, nil
	}

	self := PeerInfo{ID: "self", Host: "a", Addr: "10.0.0.1:8088"}
	peers := []PeerInfo{
		{ID: "p1", Host: "b", Addr: "10.0.0.2:8088"},
		{ID: "p2", Host: "c", Addr: "10.0.0.3:8088"},
	}
	m := a.SpeedSweep(context.Background(), self, peers, SpeedReq{Direction: "down", DurationS: 1}, nil)
	if len(m.Cells) != 6 { // 3 nodes → 6 directed pairs
		t.Fatalf("cells = %d, want 6", len(m.Cells))
	}
}

func TestSpeedSweepRunsEveryPair(t *testing.T) {
	a := &App{nodeID: "self"}
	a.localSpeed = func(_ context.Context, req SpeedReq, _ func(LivePoint)) SpeedResult { return SpeedResult{DownMbit: 900} }
	a.FetchSpeed = func(_ context.Context, baseURL string, req SpeedReq, _ func(LivePoint)) (SpeedResult, error) {
		return SpeedResult{DownMbit: 500}, nil
	}

	self := PeerInfo{ID: "self", Host: "ryzen", Addr: "127.0.0.1:8088"}
	peers := []PeerInfo{{ID: "p", Host: "proj", Addr: "10.0.0.2:8088"}}
	m := a.SpeedSweep(context.Background(), self, peers, SpeedReq{Direction: "down", DurationS: 3}, nil)

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
