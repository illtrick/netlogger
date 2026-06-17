package netmodel

import "testing"

func TestTiers(t *testing.T) {
	tiers := Tiers(Default())
	if len(tiers) != 4 {
		t.Fatalf("want 4 tiers, got %d", len(tiers))
	}
	if tiers[0].Name != "Internet" || tiers[3].Name != "Devices" {
		t.Fatalf("tier names: %q %q", tiers[0].Name, tiers[3].Name)
	}
	// Internet:1, Gateway(modem+router):2, Switches:2, Devices(nas+3 pcs):4
	if len(tiers[0].Devices) != 1 || len(tiers[1].Devices) != 2 || len(tiers[2].Devices) != 2 || len(tiers[3].Devices) != 4 {
		t.Fatalf("tier sizes: %d/%d/%d/%d", len(tiers[0].Devices), len(tiers[1].Devices), len(tiers[2].Devices), len(tiers[3].Devices))
	}
}

func TestMonitorTargets(t *testing.T) {
	tg := MonitorTargets(Default())
	has := func(ip string) bool {
		for _, x := range tg {
			if x.IP == ip {
				return true
			}
		}
		return false
	}
	if !has("8.8.8.8") || !has("192.168.0.1") || !has("192.168.0.154") {
		t.Fatalf("missing expected monitor target: %+v", tg)
	}
	if has("192.168.0.24") { // ryzen Wi-Fi is not monitored
		t.Fatalf("unmonitored Wi-Fi must not be a target: %+v", tg)
	}
}
