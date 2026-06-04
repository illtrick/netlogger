package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"netlogger/internal/version"
)

func TestStatusEndpoint(t *testing.T) {
	srv := &Server{Host: "ryzen", ServiceState: "running"}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status code: %d", rr.Code)
	}
	var got Status
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Host != "ryzen" || got.ServiceState != "running" || got.Version != version.Version {
		t.Fatalf("bad status payload: %+v", got)
	}
}

func TestServesIndex(t *testing.T) {
	srv := &Server{Host: "ryzen", ServiceState: "running"}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status code: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "NetLogger") {
		t.Fatalf("index did not render NetLogger title")
	}
}
