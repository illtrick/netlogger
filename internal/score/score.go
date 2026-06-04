// Package score attributes tested/failing host-pairs to the components on their
// topology path and assigns per-component health + coverage (spec §9a).
package score

import (
	"sort"

	"netlogger/internal/config"
)

// Component is one scored network element.
type Component struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	Health       string `json:"health"`   // good|fair|poor|untested
	Coverage     string `json:"coverage"` // none|light|partial|thorough
	TestedPaths  int    `json:"tested_paths"`
	FailingPaths int    `json:"failing_paths"`
}

// key is the unordered host-pair key.
func key(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}

// Key is the unordered host-pair key (exported for callers building maps).
func Key(a, b string) string { return key(a, b) }

type graph struct {
	adj map[string][]string
}

func buildGraph(cfg *config.Config) graph {
	g := graph{adj: map[string][]string{}}
	for _, n := range cfg.Nodes {
		if _, ok := g.adj[n.ID]; !ok {
			g.adj[n.ID] = nil
		}
	}
	for _, l := range cfg.Links {
		g.adj[l[0]] = append(g.adj[l[0]], l[1])
		g.adj[l[1]] = append(g.adj[l[1]], l[0])
	}
	return g
}

// path returns the BFS shortest path of node ids from src to dst (inclusive),
// or nil if unreachable.
func (g graph) path(src, dst string) []string {
	if src == dst {
		return []string{src}
	}
	prev := map[string]string{src: ""}
	queue := []string{src}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		neigh := append([]string{}, g.adj[cur]...)
		sort.Strings(neigh) // deterministic
		for _, nx := range neigh {
			if _, seen := prev[nx]; seen {
				continue
			}
			prev[nx] = cur
			if nx == dst {
				return rebuild(prev, src, dst)
			}
			queue = append(queue, nx)
		}
	}
	return nil
}

func rebuild(prev map[string]string, src, dst string) []string {
	var rev []string
	for at := dst; at != ""; at = prev[at] {
		rev = append(rev, at)
		if at == src {
			break
		}
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

func coverageLabel(n int) string {
	switch {
	case n <= 0:
		return "none"
	case n == 1:
		return "light"
	case n <= 3:
		return "partial"
	default:
		return "thorough"
	}
}

// Score walks each addressed host-pair's topology path and attributes the
// tested/failing status to every component on it, then labels health+coverage.
func Score(cfg *config.Config, tested, failing map[string]bool) []Component {
	g := buildGraph(cfg)

	var endpoints []string
	for _, n := range cfg.Nodes {
		if n.Address != "" {
			endpoints = append(endpoints, n.ID)
		}
	}

	testedThrough := map[string]int{}
	failingThrough := map[string]int{}
	for i := 0; i < len(endpoints); i++ {
		for j := i + 1; j < len(endpoints); j++ {
			a, b := endpoints[i], endpoints[j]
			k := key(a, b)
			if !tested[k] {
				continue
			}
			for _, nodeID := range g.path(a, b) {
				testedThrough[nodeID]++
				if failing[k] {
					failingThrough[nodeID]++
				}
			}
		}
	}

	var out []Component
	for _, n := range cfg.Nodes {
		tp := testedThrough[n.ID]
		fp := failingThrough[n.ID]
		c := Component{ID: n.ID, Label: n.Label, TestedPaths: tp, FailingPaths: fp, Coverage: coverageLabel(tp)}
		switch {
		case tp == 0:
			c.Health = "untested"
		case fp == 0:
			c.Health = "good"
		case fp == tp:
			c.Health = "poor"
		default:
			c.Health = "fair"
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
