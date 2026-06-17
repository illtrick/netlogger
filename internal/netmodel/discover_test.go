package netmodel

import "testing"

func TestMergeAgentUpdatesInPlace(t *testing.T) {
	cfg := Default()
	agent := Agent{
		NodeUUID: "uuid-ryzen",
		Name:     "ryzen",
		Interfaces: []Interface{
			{ID: "eth", Medium: MediumWired, MAC: "2C:F0:5D:11:22:33", IP: "192.168.0.200", Speed: "2.5G"},
		},
	}
	out := MergeAgent(cfg, agent)
	d, _ := out.Device("ryzen")
	if d.NodeUUID != "uuid-ryzen" || !d.Agent {
		t.Fatalf("agent identity not set: %+v", d)
	}
	var eth Interface
	for _, ifc := range d.Interfaces {
		if ifc.MAC == "2C:F0:5D:11:22:33" {
			eth = ifc
		}
	}
	if eth.IP != "192.168.0.200" { // refreshed from discovery
		t.Fatalf("discovered IP not merged: %+v", eth)
	}
	if eth.Via != "sw2" { // user topology preserved
		t.Fatalf("user Via clobbered: %+v", eth)
	}
	if len(d.Interfaces) != 2 { // Wi-Fi interface untouched, not duplicated
		t.Fatalf("interface count changed: %+v", d.Interfaces)
	}
}

func TestMergeAgentAppendsUnknown(t *testing.T) {
	cfg := Default()
	before := len(cfg.Devices)
	out := MergeAgent(cfg, Agent{NodeUUID: "u-new", Name: "Laptop",
		Interfaces: []Interface{{ID: "wifi", Medium: MediumWifi, MAC: "AA:BB:CC:DD:EE:FF"}}})
	if len(out.Devices) != before+1 {
		t.Fatalf("unknown agent should append a device")
	}
	d, ok := out.Device("laptop")
	if !ok || !d.Agent || d.Type != TypePC {
		t.Fatalf("appended device wrong: %+v", d)
	}
}
