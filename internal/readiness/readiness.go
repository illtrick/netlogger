// Package readiness runs device-agnostic per-node configuration checks.
// It encodes no vendor/NIC-specific knowledge — that's the operator's (spec §2a).
package readiness

import (
	"fmt"
	"net/http"
	"time"

	"netlogger/internal/clock"
	"netlogger/internal/config"
	"netlogger/internal/mesh"
)

// Check is one readiness probe result.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// Result is all checks for one node.
type Result struct {
	NodeID string  `json:"node_id"`
	Online bool    `json:"online"`
	Checks []Check `json:"checks"`
	Issues int     `json:"issues"`
}

// Checker runs readiness checks against agents.
type Checker struct {
	Client    *http.Client
	Clock     clock.Clock
	Tolerance time.Duration
}

// NewChecker returns a Checker with sane defaults (2s clock tolerance).
func NewChecker() *Checker {
	return &Checker{
		Client:    &http.Client{Timeout: 4 * time.Second},
		Clock:     clock.System{},
		Tolerance: 2 * time.Second,
	}
}

// Check runs all readiness checks for node and returns the combined result.
func (c *Checker) Check(node config.Node) Result {
	res := Result{NodeID: node.ID}
	add := func(name string, ok bool, detail string) {
		res.Checks = append(res.Checks, Check{Name: name, OK: ok, Detail: detail})
		if !ok {
			res.Issues++
		}
	}

	// Config-only check (no network needed).
	add("role/targets assigned", node.Address != "", node.Address)

	if node.Address == "" {
		res.Online = false
		return res
	}

	t0 := c.Clock.NowUnixMicro()
	info, err := mesh.FetchInfo(c.Client, "http://"+node.Address)
	t1 := c.Clock.NowUnixMicro()
	if err != nil {
		res.Online = false
		add("reachable", false, err.Error())
		return res
	}
	res.Online = true
	add("reachable", true, "")

	// Rough offset estimate: assume symmetric path, agent time ~ midpoint of RTT.
	rtt := t1 - t0
	est := t0 + rtt/2
	offset := info.TimeUnixUS - est
	absOff := offset
	if absOff < 0 {
		absOff = -absOff
	}
	add("clock sync within tolerance", absOff <= c.Tolerance.Microseconds(),
		fmt.Sprintf("offset %dms", offset/1000))
	add("iperf3 present", info.Iperf3Version != "", info.Iperf3Version)
	add("data dir writable", info.DataWritable, "")
	return res
}
