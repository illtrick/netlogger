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

func TestResolveListen(t *testing.T) {
	def := "0.0.0.0:8088"
	cases := []struct {
		name    string
		flagVal string
		s       *Settings
		want    string
	}{
		{"flag wins", "127.0.0.1:9000", &Settings{Listen: "0.0.0.0:8088"}, "127.0.0.1:9000"},
		{"settings override", "", &Settings{Listen: "0.0.0.0:8088"}, "0.0.0.0:8088"},
		{"default when empty", "", &Settings{}, def},
		{"default when nil", "", nil, def},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ResolveListen(c.flagVal, def, c.s); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestSaveLoadRoundTripWithListen(t *testing.T) {
	path := Path(t.TempDir())
	want := &Settings{DBDir: filepath.Join("D:", "data"), Listen: "0.0.0.0:8088"}
	if err := Save(path, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.DBDir != want.DBDir || got.Listen != want.Listen {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
