// Package web serves the embedded status SPA and a status JSON endpoint.
package web

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"

	"netlogger/internal/version"
)

//go:embed static/*
var content embed.FS

// Status is the JSON payload for /api/status.
type Status struct {
	Host         string `json:"host"`
	Version      string `json:"version"`
	ServiceState string `json:"service_state"`
}

// Server holds the live state and optional coordinator data handlers.
type Server struct {
	Host             string
	ServiceState     string
	AgentsHandler      http.HandlerFunc // optional; nil -> empty array
	ReadinessHandler   http.HandlerFunc // optional; nil -> empty array
	CorrelationHandler http.HandlerFunc // optional; nil -> empty array
	ComponentsHandler  http.HandlerFunc // optional; nil -> empty array
	LoadTestHandler    http.HandlerFunc // optional; nil -> empty array
	ClassifyHandler    http.HandlerFunc // optional; nil -> empty array
	TopologyHandler    http.HandlerFunc // optional; nil -> empty array
}

// Handler returns the HTTP handler (status API, agents/readiness, static files).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Status{
			Host:         s.Host,
			Version:      version.Version,
			ServiceState: s.ServiceState,
		})
	})
	mux.HandleFunc("/api/agents", orEmptyArray(s.AgentsHandler))
	mux.HandleFunc("/api/readiness", orEmptyArray(s.ReadinessHandler))
	mux.HandleFunc("/api/correlation", orEmptyArray(s.CorrelationHandler))
	mux.HandleFunc("/api/components", orEmptyArray(s.ComponentsHandler))
	mux.HandleFunc("/api/loadtest", orEmptyArray(s.LoadTestHandler))
	mux.HandleFunc("/api/classify", orEmptyArray(s.ClassifyHandler))
	mux.HandleFunc("/api/topology", orEmptyArray(s.TopologyHandler))
	sub, err := fs.Sub(content, "static")
	if err != nil {
		panic(err)
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))
	return mux
}

func orEmptyArray(h http.HandlerFunc) http.HandlerFunc {
	if h != nil {
		return h
	}
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}
}
