package ui

import (
	"testing"

	"netlogger/internal/appcore"
)

func TestKPIValues(t *testing.T) {
	s := appcore.Snapshot{GatewayRTTms: 0.5, InternetRTTms: 4.0, Peers: []appcore.PeerInfo{{}, {}}}
	k := kpis(s)
	if len(k) != 4 {
		t.Fatalf("want 4 KPI tiles, got %d", len(k))
	}
	if k[0].value != "0.5 ms" || k[2].value != "2" {
		t.Fatalf("kpi values wrong: %+v", k)
	}
}

func TestKPIDropTone(t *testing.T) {
	clean := kpis(appcore.Snapshot{})
	if clean[3].tone != colTextPri {
		t.Fatalf("no drops should be neutral tone")
	}
	lossy := kpis(appcore.Snapshot{Peers: []appcore.PeerInfo{{DropEpisodes: 3}}})
	if lossy[3].tone != colWatch {
		t.Fatalf("drops should tint the tile")
	}
}
