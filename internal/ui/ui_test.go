package ui

import (
	"strings"
	"testing"

	"netlogger/internal/appcore"
)

func TestStatusLinesGatewayMissingAndNoPeers(t *testing.T) {
	s := appcore.Snapshot{DataDir: "D", DBPath: "D/x.db", Iperf3Version: "iperf 3.21", Iperf3ServerUp: true}
	joined := strings.Join(statusLines(s), "\n")
	for _, want := range []string{"Gateway:       (not detected)", "Discovered peers (0)", "none yet", "server running"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in:\n%s", want, joined)
		}
	}
}

func TestStatusLinesWithGatewayAndPeer(t *testing.T) {
	s := appcore.Snapshot{
		GatewayIP: "192.168.0.1", GatewayRTTms: 0.6,
		Peers: []appcore.PeerInfo{{
			Host: "projectorpc", Addr: "192.168.0.127:8088",
			RTTms: 1.0, JitterMs: 0.2, UDPLossPct: 0, DropEpisodes: 3,
		}},
	}
	joined := strings.Join(statusLines(s), "\n")
	for _, want := range []string{"Gateway:       192.168.0.1", "Discovered peers (1)", "projectorpc", "drops 3"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in:\n%s", want, joined)
		}
	}
}

func TestPeerNameAndHelpers(t *testing.T) {
	if got := peerName(appcore.PeerInfo{ID: "uuid-x"}); got != "uuid-x" {
		t.Fatalf("expected ID fallback, got %q", got)
	}
	if got := peerName(appcore.PeerInfo{Host: "h", ID: "uuid"}); got != "h" {
		t.Fatalf("expected host, got %q", got)
	}
	if got := versionOr(""); got != "(not available)" {
		t.Fatalf("versionOr empty = %q", got)
	}
	if got := upDown(false); got != "stopped" {
		t.Fatalf("upDown(false) = %q", got)
	}
}
