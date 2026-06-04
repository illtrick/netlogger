// Package iperf wraps the iperf3 binary: it parses --json output and runs the
// client, degrading gracefully when iperf3 is not installed.
package iperf

import (
	"encoding/json"
	"fmt"
	"os/exec"
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

// Available reports whether the iperf3 binary is on PATH.
func Available() bool {
	_, err := exec.LookPath("iperf3")
	return err == nil
}
