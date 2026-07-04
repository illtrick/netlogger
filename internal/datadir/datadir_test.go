package datadir

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePrefersExeDirWhenWritable(t *testing.T) {
	exeDir := t.TempDir()
	fallback := t.TempDir()
	got, err := resolve(exeDir, fallback, true, func(string) bool { return true })
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := filepath.Join(exeDir, "NetLogger-data")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("expected dir created: %v", err)
	}
}

func TestResolveFallsBackWhenExeDirNotWritable(t *testing.T) {
	exeDir := t.TempDir()
	fallback := t.TempDir()
	writable := func(p string) bool { return p != filepath.Join(exeDir, "NetLogger-data") }
	got, err := resolve(exeDir, fallback, true, writable)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := filepath.Join(fallback, "NetLogger")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestResolveErrorsWhenNothingWritable(t *testing.T) {
	exeDir := t.TempDir()
	fallback := t.TempDir()
	_, err := resolve(exeDir, fallback, true, func(string) bool { return false })
	if err == nil {
		t.Fatalf("expected error when no location is writable")
	}
}

func TestProbeWritable(t *testing.T) {
	if !probeWritable(t.TempDir()) {
		t.Fatalf("expected temp dir to be writable")
	}
	if probeWritable(filepath.Join(t.TempDir(), "does-not-exist-subdir")) {
		t.Fatalf("expected non-existent dir to be not writable")
	}
}

func TestResolveSkipsBesideWhenNotPreferred(t *testing.T) {
	exeDir := t.TempDir()
	fb := t.TempDir()
	got, err := resolve(exeDir, fb, false, func(string) bool { return true })
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := filepath.Join(fb, "NetLogger")
	if got != want {
		t.Errorf("resolve = %q, want fallback %q", got, want)
	}
}

func TestInsideAppBundle(t *testing.T) {
	cases := []struct {
		dir  string
		want bool
	}{
		{"/Applications/NetLogger.app/Contents/MacOS", true},
		{"/Users/x/dev/netlogger/bin", false},
		{"/Users/x/My.app/Contents/MacOS", true},
		{"C:\\tools\\netlogger", false},
	}
	for _, c := range cases {
		if got := insideAppBundle(c.dir); got != c.want {
			t.Errorf("insideAppBundle(%q) = %v, want %v", c.dir, got, c.want)
		}
	}
}
