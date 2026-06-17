package ui

import (
	"testing"

	"netlogger/internal/appcore"
	"netlogger/internal/netmodel"
)

func TestViaLabelMultiHomed(t *testing.T) {
	d := netmodel.Device{Interfaces: []netmodel.Interface{
		{Medium: netmodel.MediumWired, Via: "sw2"},
		{Medium: netmodel.MediumWifi, Via: "router"},
	}}
	if got := viaLabel(d); got != "wired→sw2 · wifi→router" {
		t.Fatalf("viaLabel = %q", got)
	}
}

func TestDeviceHealthSources(t *testing.T) {
	snap := appcore.Snapshot{
		SelfNodeID:  "self",
		Build:       "b1",
		Peers:       []appcore.PeerInfo{{ID: "p1", Host: "ProjectorPC", LossPct: 1.4, Build: "b0"}},
		MonitorLoss: map[string]float64{"192.168.0.1": 0.0},
	}
	// self device → healthy with data
	if l, ok := deviceHealth(netmodel.Device{NodeUUID: "self"}, snap); !ok || l != 0 {
		t.Fatalf("self device: %v %v", l, ok)
	}
	// peer match by host → that peer's loss
	if l, ok := deviceHealth(netmodel.Device{Name: "ProjectorPC"}, snap); !ok || l != 1.4 {
		t.Fatalf("peer device: %v %v", l, ok)
	}
	// monitored infra by IP
	infra := netmodel.Device{Interfaces: []netmodel.Interface{{Monitor: true, IP: "192.168.0.1"}}}
	if l, ok := deviceHealth(infra, snap); !ok || l != 0 {
		t.Fatalf("infra device: %v %v", l, ok)
	}
	// unmeasured → no data
	if _, ok := deviceHealth(netmodel.Device{Name: "NAS"}, snap); ok {
		t.Fatalf("unmeasured device should have no data")
	}
}

func TestDeviceSkew(t *testing.T) {
	snap := appcore.Snapshot{Build: "b1", Peers: []appcore.PeerInfo{{Host: "ProjectorPC", Build: "b0"}}}
	if got := deviceSkew(netmodel.Device{Name: "ProjectorPC"}, snap); got != "b0" {
		t.Fatalf("skew = %q, want b0", got)
	}
	snap.Peers[0].Build = "b1" // matched
	if got := deviceSkew(netmodel.Device{Name: "ProjectorPC"}, snap); got != "" {
		t.Fatalf("matched build should not flag skew: %q", got)
	}
}
