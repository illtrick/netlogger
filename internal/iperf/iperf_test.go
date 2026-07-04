package iperf

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestBinaryPrefersBundled verifies the extracted bundled binary wins over
// co-located / PATH resolution.
func TestBinaryPrefersBundled(t *testing.T) {
	name := "iperf3"
	if runtime.GOOS == "windows" {
		name = "iperf3.exe"
	}
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := bundledPath
	t.Cleanup(func() { bundledPath = old })
	setBundled(p)
	if got := binary(); got != p {
		t.Fatalf("binary() = %q, want bundled %q", got, p)
	}
}

// TestVersionConsistentWithAvailable guards the contract that the readiness
// check (Version) and the runner (Available/binary) agree about whether iperf3
// exists — they must use the same resolution (co-located preferred over PATH).
func TestVersionConsistentWithAvailable(t *testing.T) {
	if Available() != (Version() != "") {
		t.Fatalf("Available()=%v but Version()=%q — detection paths disagree", Available(), Version())
	}
}

// TestFirstExecutable verifies firstExecutable walks the candidate list in
// order and returns the first regular file that exists, or "" if none do.
func TestFirstExecutable(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "iperf3")
	if err := os.WriteFile(real, []byte("#!"), 0o755); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "nope", "iperf3")

	if got := firstExecutable([]string{missing, real}); got != real {
		t.Errorf("firstExecutable = %q, want %q", got, real)
	}
	if got := firstExecutable([]string{missing}); got != "" {
		t.Errorf("firstExecutable(miss) = %q, want empty", got)
	}
	if got := firstExecutable(nil); got != "" {
		t.Errorf("firstExecutable(nil) = %q, want empty", got)
	}
}

func TestParseTCP(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "tcp.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	res, err := Parse(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(res.Intervals) != 2 {
		t.Fatalf("want 2 intervals, got %d", len(res.Intervals))
	}
	if res.Intervals[1].Retransmits != 87 || res.Intervals[1].RTTus != 38000 {
		t.Fatalf("interval 2 fields wrong: %+v", res.Intervals[1])
	}
	if res.SumRetransmits != 87 {
		t.Fatalf("want 87 total retransmits, got %d", res.SumRetransmits)
	}
	if res.Intervals[1].BitsPerSecond >= res.Intervals[0].BitsPerSecond {
		t.Fatal("expected a throughput drop between intervals")
	}
}

func TestParseUDP(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "udp.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	res, err := Parse(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.UDPLostPercent != 11.6 || res.UDPJitterMs != 14.2 {
		t.Fatalf("UDP summary wrong: lost=%v jitter=%v", res.UDPLostPercent, res.UDPJitterMs)
	}
	if len(res.Intervals) != 2 {
		t.Fatalf("want 2 intervals, got %d", len(res.Intervals))
	}
	if res.Intervals[1].LostPercent != 11.6 || res.Intervals[1].JitterMs != 14.2 {
		t.Fatalf("per-interval UDP fields wrong: %+v", res.Intervals[1])
	}
}

func TestParseErrorField(t *testing.T) {
	_, err := Parse([]byte(`{"error":"unable to connect to server"}`))
	if err == nil {
		t.Fatal("an iperf3 error payload must surface as an error")
	}
}
