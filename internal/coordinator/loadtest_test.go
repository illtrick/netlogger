package coordinator

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLoadTestHandlerReportsUnavailableGracefully(t *testing.T) {
	// With no iperf3 installed, the handler must return a clean JSON error,
	// not a 500 crash.
	h := LoadTestHandler()
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/api/loadtest?target=127.0.0.1&duration=1", nil))
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
