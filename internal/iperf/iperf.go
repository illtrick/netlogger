// Package iperf wraps the iperf3 binary: it parses --json output and runs the
// client, degrading gracefully when iperf3 is not installed.
package iperf

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Interval is one 1-second iperf3 interval (the high-signal fields, spec §5.4).
type Interval struct {
	StartS        float64 `json:"start_s"`
	EndS          float64 `json:"end_s"`
	BitsPerSecond float64 `json:"bits_per_second"`
	Retransmits   int     `json:"retransmits"`  // TCP
	RTTus         int     `json:"rtt_us"`       // TCP
	JitterMs      float64 `json:"jitter_ms"`    // UDP
	LostPercent   float64 `json:"lost_percent"` // UDP
}

// Result is the parsed iperf3 run.
type Result struct {
	Intervals      []Interval `json:"intervals"`
	SumBitsPerSec  float64    `json:"sum_bits_per_second"`
	SumRetransmits int        `json:"sum_retransmits"`
	UDPLostPercent float64    `json:"udp_lost_percent"`
	UDPJitterMs    float64    `json:"udp_jitter_ms"`
}

type rawResult struct {
	Intervals []struct {
		Sum struct {
			Start         float64 `json:"start"`
			End           float64 `json:"end"`
			BitsPerSecond float64 `json:"bits_per_second"`
			Retransmits   int     `json:"retransmits"`
			RTT           int     `json:"rtt"`
			JitterMs      float64 `json:"jitter_ms"`
			LostPercent   float64 `json:"lost_percent"`
		} `json:"sum"`
	} `json:"intervals"`
	End struct {
		SumSent struct {
			BitsPerSecond float64 `json:"bits_per_second"`
			Retransmits   int     `json:"retransmits"`
		} `json:"sum_sent"`
		Sum struct {
			JitterMs    float64 `json:"jitter_ms"`
			LostPercent float64 `json:"lost_percent"`
		} `json:"sum"`
	} `json:"end"`
	Error string `json:"error"`
}

// Parse converts iperf3 --json bytes into a Result.
func Parse(data []byte) (Result, error) {
	var raw rawResult
	if err := json.Unmarshal(data, &raw); err != nil {
		return Result{}, fmt.Errorf("parse iperf3 json: %w", err)
	}
	if raw.Error != "" {
		return Result{}, fmt.Errorf("iperf3: %s", raw.Error)
	}
	res := Result{
		SumBitsPerSec:  raw.End.SumSent.BitsPerSecond,
		SumRetransmits: raw.End.SumSent.Retransmits,
		UDPLostPercent: raw.End.Sum.LostPercent,
		UDPJitterMs:    raw.End.Sum.JitterMs,
	}
	for _, iv := range raw.Intervals {
		res.Intervals = append(res.Intervals, Interval{
			StartS:        iv.Sum.Start,
			EndS:          iv.Sum.End,
			BitsPerSecond: iv.Sum.BitsPerSecond,
			Retransmits:   iv.Sum.Retransmits,
			RTTus:         iv.Sum.RTT,
			JitterMs:      iv.Sum.JitterMs,
			LostPercent:   iv.Sum.LostPercent,
		})
	}
	return res, nil
}

// pick resolves the iperf3 binary: prefer one bundled next to the app, else
// fall back to PATH. Pure helper for testability.
func pick(exeDir, name string, exists func(string) bool, look func(string) (string, bool)) string {
	cand := filepath.Join(exeDir, name)
	if exists(cand) {
		return cand
	}
	if p, ok := look("iperf3"); ok {
		return p
	}
	return ""
}

// bundledPath is the extracted bundled iperf3 (set by Bootstrap on Windows).
var bundledPath string

func setBundled(p string) { bundledPath = p }

// binary returns the path to the iperf3 executable, or "" if none is found.
// Resolution order: extracted bundled binary > co-located (next to netlogger) >
// PATH.
func binary() string {
	name := "iperf3"
	if runtime.GOOS == "windows" {
		name = "iperf3.exe"
	}
	exists := func(p string) bool { st, err := os.Stat(p); return err == nil && !st.IsDir() }
	if bundledPath != "" && exists(bundledPath) {
		return bundledPath
	}
	exeDir := ""
	if exe, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(exe)
	}
	look := func(n string) (string, bool) { p, err := exec.LookPath(n); return p, err == nil }
	return pick(exeDir, name, exists, look)
}

// Available reports whether an iperf3 binary is bundled or on PATH.
func Available() bool { return binary() != "" }

// Version returns iperf3's version line using the SAME resolution as the runner
// (co-located binary preferred over PATH), or "" if iperf3 is not found. This
// keeps the readiness "iperf3 present" check consistent with what load tests
// will actually use.
func Version() string {
	bin := binary()
	if bin == "" {
		return ""
	}
	vc := exec.Command(bin, "--version")
	hideConsole(vc)
	out, err := vc.CombinedOutput()
	if err != nil && len(out) == 0 {
		return ""
	}
	return strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
}

// Opts configures a load test run.
type Opts struct {
	DurationS   int
	UDP         bool
	BitrateMbit int  // UDP target bitrate in Mbit/s; 0 = iperf3 default
	Port        int  // 0 = iperf3 default (5201)
	Streams     int  // -P parallel streams; 0 = single stream (one-thread-per-stream needs iperf3 >= 3.16)
	Reverse     bool // -R: server sends, client receives (download from the client's seat)
	Bidir       bool // --bidir: simultaneous both directions (needs iperf3 >= 3.7)
	OmitS       int  // -O: omit the first N seconds (skip TCP slow-start)
}

func buildArgs(target string, o Opts) []string {
	if o.DurationS <= 0 {
		o.DurationS = 10
	}
	args := []string{"-c", target, "--json", "-i", "1", "-t", strconv.Itoa(o.DurationS)}
	if o.Port > 0 {
		args = append(args, "-p", strconv.Itoa(o.Port))
	}
	if o.Streams > 0 {
		args = append(args, "-P", strconv.Itoa(o.Streams))
	}
	if o.OmitS > 0 {
		args = append(args, "-O", strconv.Itoa(o.OmitS))
	}
	if o.Reverse {
		args = append(args, "-R")
	}
	if o.Bidir {
		args = append(args, "--bidir")
	}
	if o.UDP {
		args = append(args, "-u")
		if o.BitrateMbit > 0 {
			args = append(args, "-b", strconv.Itoa(o.BitrateMbit)+"M")
		}
	}
	return args
}

// RunClient runs iperf3 (bundled or on PATH) and parses the result. It returns
// a clear error if no iperf3 binary is found.
func RunClient(target string, o Opts) (Result, error) {
	bin := binary()
	if bin == "" {
		return Result{}, fmt.Errorf("iperf3 not found (bundle it next to NetLogger or install it) — cannot run load test")
	}
	cc := exec.Command(bin, buildArgs(target, o)...)
	hideConsole(cc)
	out, err := cc.Output()
	if err != nil && len(out) == 0 {
		return Result{}, fmt.Errorf("iperf3 run: %w", err)
	}
	return Parse(out)
}
