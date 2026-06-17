package netmodel

import (
	"path/filepath"
	"testing"
)

func TestRoundTripAndIdentity(t *testing.T) {
	cfg := Default()
	ryzen, ok := cfg.Device("ryzen")
	if !ok || len(ryzen.Interfaces) != 2 {
		t.Fatalf("ryzen should have 2 interfaces, got %+v", ryzen)
	}
	var wired, wifi int
	macs := map[string]bool{}
	for _, ifc := range ryzen.Interfaces {
		if ifc.MAC == "" || macs[ifc.MAC] {
			t.Fatalf("interface MAC must be unique+nonempty: %+v", ifc)
		}
		macs[ifc.MAC] = true
		switch ifc.Medium {
		case MediumWired:
			wired++
		case MediumWifi:
			wifi++
		default:
			t.Fatalf("interface medium must be wired|wifi: %q", ifc.Medium)
		}
	}
	if wired != 1 || wifi != 1 {
		t.Fatalf("ryzen want 1 wired + 1 wifi, got %d/%d", wired, wifi)
	}

	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := Parse(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got.Devices) != len(cfg.Devices) {
		t.Fatalf("round-trip device count: %d != %d", len(got.Devices), len(cfg.Devices))
	}
}

func TestParseEmptyIsDefault(t *testing.T) {
	cfg, err := Parse(nil)
	if err != nil || len(cfg.Devices) == 0 {
		t.Fatalf("empty config should fall back to Default(): %v", err)
	}
}

func TestLoadSaveFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "network.json")
	cfg, err := Load(p) // missing → Default
	if err != nil || len(cfg.Devices) == 0 {
		t.Fatalf("missing file should load Default: %v", err)
	}
	if err := Save(p, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(p)
	if err != nil || len(got.Devices) != len(cfg.Devices) {
		t.Fatalf("reload mismatch: %v %d", err, len(got.Devices))
	}
}
