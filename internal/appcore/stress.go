package appcore

const stressAbortAfter = 3 // consecutive iperf3 client errors against a target → abort that link

// StressOpts is a node-local stress command: load these targets at a per-link
// cap for a bounded duration, starting at an absolute wall-clock time.
type StressOpts struct {
	RunID          string   `json:"run_id"`
	Targets        []string `json:"targets"`
	PerLinkCapMbit int      `json:"per_link_cap_mbit"`
	Proto          string   `json:"proto"`
	DurationS      int      `json:"duration_s"`
	StartAtUnixUS  int64    `json:"start_at_unix_us"`
}

// LinkLoad is one target's live load/health during a run.
type LinkLoad struct {
	Target      string  `json:"target"`
	SentMbit    float64 `json:"sent_mbit"`
	Retransmits int     `json:"retransmits"`
	Aborted     bool    `json:"aborted"`
}

// StressStatus is a node's current stress state.
type StressStatus struct {
	RunID         string     `json:"run_id"`
	Running       bool       `json:"running"`
	StartedUnixUS int64      `json:"started_unix_us"`
	EndsUnixUS    int64      `json:"ends_unix_us"`
	Links         []LinkLoad `json:"links"`
}

// meshTargets maps every node id → the bare hosts of all OTHER nodes (full mesh).
func meshTargets(self PeerInfo, peers []PeerInfo) map[string][]string {
	all := append([]PeerInfo{self}, peers...)
	out := make(map[string][]string, len(all))
	for _, n := range all {
		var ts []string
		for _, other := range all {
			if other.ID != n.ID {
				ts = append(ts, iperfHost(other.Addr))
			}
		}
		out[n.ID] = ts
	}
	return out
}

// shouldAbort reports whether a link's consecutive-error count warrants aborting it.
func shouldAbort(consecutiveErrs int) bool { return consecutiveErrs >= stressAbortAfter }

// startDelay returns the microseconds to wait before starting, given now and the
// scheduled absolute start. Clamped to >= 0.
func startDelay(nowUS, startAtUS int64) int64 {
	if d := startAtUS - nowUS; d > 0 {
		return d
	}
	return 0
}
