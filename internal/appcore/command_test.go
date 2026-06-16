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

func TestResetSummary(t *testing.T) {
	if got := resetSummary(2, 2, nil); got != "reset: this machine + 2/2 peers" {
		t.Fatalf("all-acked: %q", got)
	}
	got := resetSummary(0, 1, []string{"ryzen did not ack (unreachable or old build — redeploy)"})
	want := "reset: this machine + 0/1 peers · ryzen did not ack (unreachable or old build — redeploy)"
	if got != want {
		t.Fatalf("with-note:\n got %q\nwant %q", got, want)
	}
}
