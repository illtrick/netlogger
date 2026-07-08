package iperf

// Live-streamed runs: iperf3 --json-stream (>= 3.17) emits one NDJSON event
// per interval, which lets the UI show throughput per second WHILE a test
// runs — the feature that made jperf worth using. Schema captured from the
// bundled iperf 3.21 (docs/superpowers/specs/2026-07-08-lan-test-overhaul.md):
//
//	{"event":"start","data":{…}}
//	{"event":"interval","data":{"streams":[…],"sum":{…}}}
//	{"event":"end","data":{"streams":[…],"sum_sent":{…},"sum_received":{…}}}
//
// Note: TCP interval sums carry retransmits/rtt only where the OS exposes
// TCP_INFO (macOS/Linux); Windows/Cygwin builds omit them — fields stay 0.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

// streamEvent is one --json-stream NDJSON line.
type streamEvent struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}

// parseStreamEvent decodes one stream line into exactly one of: an interval,
// the end block, or an error message. Unknown events (start, …) and non-JSON
// noise yield all-zero values with ok=false only for undecodable lines.
func parseStreamEvent(line []byte) (iv *Interval, end *rawEnd, errText string, ok bool) {
	var ev streamEvent
	if err := json.Unmarshal(line, &ev); err != nil || ev.Event == "" {
		return nil, nil, "", false
	}
	switch ev.Event {
	case "interval":
		var raw rawIntervalSum
		if err := json.Unmarshal(ev.Data, &raw); err != nil {
			return nil, nil, "", false
		}
		i := raw.interval()
		return &i, nil, "", true
	case "end":
		var e rawEnd
		if err := json.Unmarshal(ev.Data, &e); err != nil {
			return nil, nil, "", false
		}
		return nil, &e, "", true
	case "error":
		var msg string
		if err := json.Unmarshal(ev.Data, &msg); err != nil {
			msg = string(ev.Data)
		}
		return nil, nil, msg, true
	default: // "start" and friends — recognized, nothing to extract
		return nil, nil, "", true
	}
}

// streamArgs converts a plain --json argument list to --json-stream.
func streamArgs(args []string) []string {
	out := make([]string, len(args))
	copy(out, args)
	for i, a := range out {
		if a == "--json" {
			out[i] = "--json-stream"
		}
	}
	return out
}

// RunClientStream runs an iperf3 client with per-interval live reporting:
// onInterval (may be nil) fires once per second during the run, and the final
// Result is assembled from the end event. If the resolved binary predates
// --json-stream (iperf3 < 3.17: exits immediately, no events), it falls back
// to a single plain --json run so old installs keep working — just without
// the live readout. Cancelling ctx kills the process.
func RunClientStream(ctx context.Context, target string, o Opts, onInterval func(Interval)) (Result, error) {
	bin := binary()
	if bin == "" {
		return Result{}, fmt.Errorf("iperf3 not found (%s) — cannot run load test", installHint)
	}
	cc := exec.CommandContext(ctx, bin, streamArgs(buildArgs(target, o))...)
	hideConsole(cc)
	var stderr bytes.Buffer
	cc.Stderr = &stderr
	stdout, err := cc.StdoutPipe()
	if err != nil {
		return Result{}, err
	}
	if err := cc.Start(); err != nil {
		return Result{}, fmt.Errorf("iperf3 start: %w", err)
	}

	var res Result
	var sawEvent, sawEnd bool
	var runErr string
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // end events carry per-stream arrays
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		iv, end, errText, ok := parseStreamEvent(line)
		if !ok {
			continue
		}
		sawEvent = true
		switch {
		case iv != nil:
			res.Intervals = append(res.Intervals, *iv)
			if onInterval != nil {
				onInterval(*iv)
			}
		case end != nil:
			applyEnd(&res, *end)
			sawEnd = true
		case errText != "":
			runErr = errText
		}
	}
	waitErr := cc.Wait()

	switch {
	case runErr != "":
		return Result{}, fmt.Errorf("iperf3: %s", runErr)
	case sawEnd:
		return res, nil
	case !sawEvent && waitErr != nil && ctx.Err() == nil:
		// No stream events and an immediate failure: almost certainly a binary
		// without --json-stream (usage error on stderr). One plain-JSON retry.
		return RunClientCtx(ctx, target, o)
	case waitErr != nil:
		return Result{}, fmt.Errorf("iperf3 run: %w", waitErr)
	default:
		return Result{}, fmt.Errorf("iperf3: stream ended without a summary")
	}
}
