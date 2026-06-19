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
