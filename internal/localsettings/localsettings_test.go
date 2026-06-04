package localsettings

import (
	"path/filepath"
	"testing"
)

func TestLoadMissingReturnsEmpty(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("missing file should not error, got %v", err)
	}
	if s.DBDir != "" {
		t.Fatalf("expected empty DBDir, got %q", s.DBDir)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := Path(t.TempDir())
	want := &Settings{DBDir: filepath.Join("D:", "netlogger-data")}
	if err := Save(path, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.DBDir != want.DBDir {
		t.Fatalf("DBDir = %q, want %q", got.DBDir, want.DBDir)
	}
}

func TestResolveDBPathDefault(t *testing.T) {
	got := ResolveDBPath(filepath.Join("base", "dir"), "netlogger.db", &Settings{})
	want := filepath.Join("base", "dir", "netlogger.db")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestResolveDBPathOverride(t *testing.T) {
	got := ResolveDBPath(filepath.Join("base", "dir"), "netlogger.db", &Settings{DBDir: filepath.Join("other", "place")})
	want := filepath.Join("other", "place", "netlogger.db")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestResolveDBPathNilSettings(t *testing.T) {
	got := ResolveDBPath("base", "x.db", nil)
	want := filepath.Join("base", "x.db")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
