package iperf

import (
	"reflect"
	"testing"
)

func TestBuildArgsSpeedFlags(t *testing.T) {
	got := buildArgs("10.0.0.5", Opts{DurationS: 10, Streams: 4, OmitS: 2, Port: 5201})
	want := []string{"-c", "10.0.0.5", "--json", "-i", "1", "-t", "10", "-p", "5201", "-P", "4", "-O", "2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tcp+streams+omit:\n got %v\nwant %v", got, want)
	}

	rev := buildArgs("h", Opts{DurationS: 5, Reverse: true})
	if !contains(rev, "-R") {
		t.Fatalf("reverse should add -R: %v", rev)
	}

	bid := buildArgs("h", Opts{DurationS: 5, Bidir: true})
	if !contains(bid, "--bidir") {
		t.Fatalf("bidir should add --bidir: %v", bid)
	}

	udp := buildArgs("h", Opts{DurationS: 5, UDP: true, BitrateMbit: 200, Streams: 3})
	if !contains(udp, "-u") || !contains(udp, "-b") || !contains(udp, "200M") || !contains(udp, "-P") {
		t.Fatalf("udp capped + streams: %v", udp)
	}
}

