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

func TestMergeEventsSortsAndLabels(t *testing.T) {
	self := []EventInfo{
		{UnixMicro: 100, Online: false, Detail: "link to ryzen degraded"},
		{UnixMicro: 300, Online: true, Detail: "link to ryzen recovered"},
	}
	peers := [][]MergedEvent{{
		{Host: "ryzen", UnixMicro: 200, Online: false, Detail: "Ethernet link speed 2.5 Gbps → 1 Gbps"},
	}}
	out := mergeEvents("ProjectorPC", self, peers, 10)
	if len(out) != 3 {
		t.Fatalf("want 3 merged, got %d", len(out))
	}
	// sorted by timestamp, interleaving the peer event between the two self events
	if out[0].UnixMicro != 100 || out[1].UnixMicro != 200 || out[2].UnixMicro != 300 {
		t.Fatalf("not time-sorted: %+v", out)
	}
	if out[0].Host != "ProjectorPC" || out[1].Host != "ryzen" {
		t.Fatalf("host labels wrong: %+v", out[:2])
	}
}

func TestMergeEventsCaps(t *testing.T) {
	self := make([]EventInfo, 0, 20)
	for i := 0; i < 20; i++ {
		self = append(self, EventInfo{UnixMicro: int64(i)})
	}
	out := mergeEvents("h", self, nil, 5)
	if len(out) != 5 || out[0].UnixMicro != 15 {
		t.Fatalf("cap kept the wrong tail: len=%d first=%d", len(out), out[0].UnixMicro)
	}
}

func TestEventsHandlerRoundTrip(t *testing.T) {
	want := []EventInfo{{UnixMicro: 42, Online: false, Detail: "Ethernet link Down"}}
	mux := http.NewServeMux()
	mux.Handle("/api/events", eventsHandler(func() []EventInfo { return want }))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	got, err := fetchEvents(http.DefaultClient, srv.URL)
	if err != nil {
		t.Fatalf("fetchEvents: %v", err)
	}
	if len(got) != 1 || got[0].UnixMicro != 42 || got[0].Online || got[0].Detail != "Ethernet link Down" {
		t.Fatalf("round-trip mismatch: %+v", got)
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
