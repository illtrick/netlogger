package iperf

import (
	"testing"
)

func TestRunClientErrorsWhenAbsent(t *testing.T) {
	if Available() {
		t.Skip("iperf3 is installed; this test only checks the absent path")
	}
	_, err := RunClient("127.0.0.1", Opts{DurationS: 1, UDP: false})
	if err == nil {
		t.Fatal("RunClient should error when iperf3 is not installed")
	}
}

func TestRunClientBuildsArgs(t *testing.T) {
	got := buildArgs("10.0.0.5", Opts{DurationS: 5, UDP: true, BitrateMbit: 30})
	joined := ""
	for _, a := range got {
		joined += a + " "
	}
	for _, want := range []string{"-c", "10.0.0.5", "--json", "-t", "5", "-u", "-b", "30M", "-i", "1"} {
		if !contains(got, want) {
			t.Fatalf("args %q missing %q", joined, want)
		}
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
