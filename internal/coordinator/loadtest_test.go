package coordinator

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"netlogger/internal/iperf"
)

func TestLoadTestHandlerReportsUnavailableGracefully(t *testing.T) {
	// Real iperf3 client (run=nil); on a box without iperf3 -> ok=false, no crash.
	h := LoadTestHandler(map[string]string{"ncase": "127.0.0.1"}, nil)
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodPost, "/api/loadtest?target=ncase&duration=1", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 with a JSON body, got %d", rr.Code)
	}
	var resp LoadTestResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.OK && resp.Error != "" {
		t.Fatalf("inconsistent response: %+v", resp)
	}
}

func TestLoadTestHandlerMapsResultFields(t *testing.T) {
	var gotHost string
	var gotOpts iperf.Opts
	run := func(target string, o iperf.Opts) (iperf.Result, error) {
		gotHost, gotOpts = target, o
		return iperf.Result{SumBitsPerSec: 2.35e9, SumRetransmits: 87, UDPLostPercent: 1.5}, nil
	}
	h := LoadTestHandler(map[string]string{"ncase": "10.0.0.5"}, run)
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodPost, "/api/loadtest?target=ncase&duration=999&udp=true", nil))

	var resp LoadTestResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.OK || resp.SumRetransmits != 87 || resp.SumBitsPerSec != 2.35e9 || resp.UDPLostPercent != 1.5 {
		t.Fatalf("result fields not mapped: %+v", resp)
	}
	if gotHost != "10.0.0.5" {
		t.Fatalf("target node id not resolved to host: %q", gotHost)
	}
	if gotOpts.DurationS != 120 {
		t.Fatalf("duration must clamp to 120, got %d", gotOpts.DurationS)
	}
	if !gotOpts.UDP {
		t.Fatal("udp flag not forwarded")
	}
}

func TestLoadTestHandlerRejectsUnknownTargetAndGET(t *testing.T) {
	run := func(string, iperf.Opts) (iperf.Result, error) {
		t.Fatal("run must not be called for a rejected request")
		return iperf.Result{}, nil
	}
	h := LoadTestHandler(map[string]string{"ncase": "10.0.0.5"}, run)

	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/api/loadtest?target=ncase", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET should be 405, got %d", rr.Code)
	}

	rr2 := httptest.NewRecorder()
	h(rr2, httptest.NewRequest(http.MethodPost, "/api/loadtest?target=ghost", nil))
	var resp LoadTestResponse
	if err := json.Unmarshal(rr2.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.OK {
		t.Fatalf("unknown target must not be OK: %+v", resp)
	}
}

func TestClassifyHandler(t *testing.T) {
	h := ClassifyHandler()
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/api/classify?gateway_failed=true&external_failed=true", nil))
	var resp ClassifyResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.LANvsWAN != "lan" {
		t.Fatalf("gateway failure should classify lan, got %q", resp.LANvsWAN)
	}
}
