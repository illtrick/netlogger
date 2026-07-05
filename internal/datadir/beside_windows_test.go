//go:build windows

package datadir

import "testing"

// Replaces the deleted TestLocalAppData: fallbackBase on Windows must honor
// %LOCALAPPDATA% and fall back to the temp dir when it is unset.
func TestFallbackBaseWindows(t *testing.T) {
	t.Setenv("LOCALAPPDATA", `C:\custom\local`)
	if got := fallbackBase(); got != `C:\custom\local` {
		t.Fatalf("expected env value, got %q", got)
	}
	t.Setenv("LOCALAPPDATA", "")
	if got := fallbackBase(); got == "" {
		t.Fatalf("expected non-empty temp-dir fallback when LOCALAPPDATA unset")
	}
}
