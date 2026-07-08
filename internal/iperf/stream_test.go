package iperf

import "testing"

// Real lines captured from the bundled iperf 3.21 on Windows (spec doc),
// trimmed to the fields we read; retransmits/rtt added to mirror TCP_INFO
// platforms (macOS/Linux).
const (
	streamStart    = `{"event":"start","data":{"version":"iperf 3.21","connecting_to":{"host":"127.0.0.1","port":5299}}}`
	streamInterval = `{"event":"interval","data":{"streams":[{"socket":5}],"sum":{"start":0,"end":1.003632,"seconds":1.0036319494247437,"bytes":2749104128,"bits_per_second":21913245225.608585,"retransmits":3,"rtt":420,"omitted":false,"sender":true}}}`
	streamEnd      = `{"event":"end","data":{"streams":[{"sender":{"socket":5}}],"sum_sent":{"start":0,"end":3.010186,"bytes":10297409536,"bits_per_second":27366839221.230846,"retransmits":7},"sum_received":{"start":0,"end":3.010334,"bytes":10297409536,"bits_per_second":27365493758.499889}}}`
	streamError    = `{"event":"error","data":"unable to connect to server: Connection refused"}`
)

func TestParseStreamEventInterval(t *testing.T) {
	iv, end, errText, ok := parseStreamEvent([]byte(streamInterval))
	if !ok || iv == nil || end != nil || errText != "" {
		t.Fatalf("interval parse: iv=%v end=%v err=%q ok=%v", iv, end, errText, ok)
	}
	if iv.BitsPerSecond < 21e9 || iv.BitsPerSecond > 22e9 {
		t.Errorf("bits_per_second = %v", iv.BitsPerSecond)
	}
	if iv.Retransmits != 3 || iv.RTTus != 420 {
		t.Errorf("retr/rtt = %d/%d, want 3/420", iv.Retransmits, iv.RTTus)
	}
	if iv.EndS < 1.0 || iv.EndS > 1.01 {
		t.Errorf("end_s = %v", iv.EndS)
	}
}

func TestParseStreamEventEnd(t *testing.T) {
	iv, end, errText, ok := parseStreamEvent([]byte(streamEnd))
	if !ok || iv != nil || end == nil || errText != "" {
		t.Fatalf("end parse: iv=%v end=%v err=%q ok=%v", iv, end, errText, ok)
	}
	var res Result
	applyEnd(&res, *end)
	if res.SumBitsPerSec < 27e9 || res.SumRetransmits != 7 || res.SumRecvBitsPerSec < 27e9 {
		t.Errorf("end sums wrong: %+v", res)
	}
}

func TestParseStreamEventErrorAndNoise(t *testing.T) {
	_, _, errText, ok := parseStreamEvent([]byte(streamError))
	if !ok || errText == "" {
		t.Fatalf("error event not surfaced: %q ok=%v", errText, ok)
	}
	// start is recognized but carries nothing
	iv, end, errText, ok := parseStreamEvent([]byte(streamStart))
	if !ok || iv != nil || end != nil || errText != "" {
		t.Fatalf("start event should be a recognized no-op")
	}
	// garbage lines are skipped, not fatal
	if _, _, _, ok := parseStreamEvent([]byte("not json")); ok {
		t.Fatalf("garbage should not parse")
	}
	if _, _, _, ok := parseStreamEvent([]byte(`{"foo":1}`)); ok {
		t.Fatalf("event-less json should not parse")
	}
}

func TestStreamArgs(t *testing.T) {
	got := streamArgs(buildArgs("10.0.0.2", Opts{DurationS: 5}))
	found := false
	for _, a := range got {
		if a == "--json-stream" {
			found = true
		}
		if a == "--json" {
			t.Fatalf("--json not replaced: %v", got)
		}
	}
	if !found {
		t.Fatalf("--json-stream missing: %v", got)
	}
	// original args must not be mutated
	orig := buildArgs("10.0.0.2", Opts{DurationS: 5})
	_ = streamArgs(orig)
	for _, a := range orig {
		if a == "--json-stream" {
			t.Fatalf("streamArgs mutated its input")
		}
	}
}
