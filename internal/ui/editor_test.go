package ui

import (
	"testing"

	"netlogger/internal/netmodel"
)

func TestAddInterfaceCreatesDistinctLink(t *testing.T) {
	cfg := netmodel.Default()
	out := addInterface(cfg, "ncase", netmodel.MediumWifi)
	d, _ := out.Device("ncase")
	if len(d.Interfaces) != 2 || d.Interfaces[1].Medium != netmodel.MediumWifi {
		t.Fatalf("wifi interface not added distinctly: %+v", d.Interfaces)
	}
	// original config unchanged (clone semantics)
	orig, _ := cfg.Device("ncase")
	if len(orig.Interfaces) != 1 {
		t.Fatalf("addInterface mutated the input config")
	}
}

func TestRemoveDevice(t *testing.T) {
	cfg := netmodel.Default()
	before := len(cfg.Devices)
	out := removeDevice(cfg, "nas")
	if len(out.Devices) != before-1 {
		t.Fatalf("removeDevice did not drop a device")
	}
	if _, ok := out.Device("nas"); ok {
		t.Fatalf("nas still present after remove")
	}
}

func TestAddBlankDeviceUniqueID(t *testing.T) {
	cfg := netmodel.Default()
	out, idx := addBlankDevice(cfg)
	if idx != len(out.Devices)-1 {
		t.Fatalf("addBlankDevice index wrong")
	}
	id := out.Devices[idx].ID
	seen := 0
	for _, d := range out.Devices {
		if d.ID == id {
			seen++
		}
	}
	if seen != 1 {
		t.Fatalf("new device id %q is not unique", id)
	}
	if out.Devices[idx].Type != netmodel.TypePC || len(out.Devices[idx].Interfaces) != 1 {
		t.Fatalf("blank device should be a pc with one interface: %+v", out.Devices[idx])
	}
}
