package appsettings

import (
	"path/filepath"
	"testing"
)

func TestLoadMissingReturnsDefault(t *testing.T) {
	s := Load(filepath.Join(t.TempDir(), "nope.json"))
	if !s.PreventSleep {
		t.Fatalf("default PreventSleep should be true")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	p := Path(t.TempDir())
	if err := Save(p, Settings{PreventSleep: false}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if Load(p).PreventSleep {
		t.Fatalf("expected persisted false")
	}
}
