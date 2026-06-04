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

// Server holds the live state shown on the status page.
type Server struct {
	Host         string
	ServiceState string
}

// Handler returns the HTTP handler (status API + embedded static files).
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
	sub, err := fs.Sub(content, "static")
	if err != nil {
		panic(err)
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))
	return mux
}
