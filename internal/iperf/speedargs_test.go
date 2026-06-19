package iperf

import (
	"reflect"
	"testing"
)

func TestParseReceivedBits(t *testing.T) {
	js := []byte(`{"intervals":[],"end":{
		"sum_sent":{"bits_per_second":100000000,"retransmits":7},
		"sum_received":{"bits_per_second":940000000},
		"sum":{"jitter_ms":0.3,"lost_percent":0.1}}}`)
	res, err := Parse(js)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.SumBitsPerSec != 100000000 {
		t.Fatalf("sent = %v, want 1e8", res.SumBitsPerSec)
	}
	if res.SumRecvBitsPerSec != 940000000 {
		t.Fatalf("received = %v, want 9.4e8", res.SumRecvBitsPerSec)
	}
}

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

func TestBuildArgsTCPRateCap(t *testing.T) {
	got := buildArgs("h", Opts{DurationS: 5, BitrateMbit: 200})
	if !contains(got, "-b") || !contains(got, "200M") {
		t.Fatalf("tcp cap should emit -b 200M: %v", got)
	}
	if contains(got, "-u") {
		t.Fatalf("tcp cap must not imply -u: %v", got)
	}
}
