package appcore

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCommandHandlerInvokesReset(t *testing.T) {
	called := 0
	mux := http.NewServeMux()
	mux.Handle("/api/command", commandHandler(func() { called++ }))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if err := postCommand(http.DefaultClient, srv.URL, "reset"); err != nil {
		t.Fatalf("postCommand: %v", err)
	}
	if called != 1 {
		t.Fatalf("reset called %d times, want 1", called)
	}
}

func TestCommandHandlerIgnoresUnknown(t *testing.T) {
	called := 0
	mux := http.NewServeMux()
	mux.Handle("/api/command", commandHandler(func() { called++ }))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	_ = postCommand(http.DefaultClient, srv.URL, "frobnicate")
	if called != 0 {
		t.Fatalf("unknown command should not reset")
	}
}
