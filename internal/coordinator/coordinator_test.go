package coordinator

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"netlogger/internal/config"
	"netlogger/internal/readiness"
)

func TestReadinessHandlerReturnsResults(t *testing.T) {
	nodes := []config.Node{{ID: "switch1"}} // addressless -> offline, but no panic
	h := ReadinessHandler(readiness.NewChecker(), nodes)

	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/api/readiness", nil))
	if rr.Code != 200 {
		t.Fatalf("code %d", rr.Code)
	}
	var out []readiness.Result
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 1 || out[0].NodeID != "switch1" {
		t.Fatalf("bad readiness output: %+v", out)
	}
}

func TestAgentsHandlerEmptyIsArrayNotNull(t *testing.T) {
	h := AgentsHandler(nil, nil)
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/api/agents", nil))
	if got := strings.TrimSpace(rr.Body.String()); got != "[]" {
		t.Fatalf("want [], got %q", got)
	}
}
