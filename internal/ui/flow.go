package ui

import (
	"image/color"

	"netlogger/internal/appcore"
)

// flowCell is one direction of data flow src→dst, assembled from run cells.
type flowCell struct {
	Mbit    float64 // primary measurement (see mapping below)
	Confirm float64 // the mirror run's measurement of the SAME flow (0 = none)
	Retr    int
	RTTms   float64
	Err     string
}

// flowKey encodes a directed flow src→dst (mirrors speedKeyUI's run encoding).
func flowKey(src, dst string) string { return src + "\x00" + dst }

// splitFlowKey undoes flowKey.
func splitFlowKey(k string) (src, dst string) {
	for i := 0; i < len(k); i++ {
		if k[i] == 0 {
			return k[:i], k[i+1:]
		}
	}
	return k, ""
}

// flowCells re-keys run results (client\x00server) into flow results
// (src\x00dst). A run client=A server=B measures: up leg = flow A→B,
// down leg = flow B→A. When both ordered runs exist ("both" sweeps), the
// flow gets two measurements: primary = the leg whose CLIENT is the flow
// source (A's up leg for A→B), confirm = the other run's down leg.
func flowCells(cells map[string]appcore.SpeedResult) map[string]flowCell {
	out := make(map[string]flowCell)
	// Pass 1 — up legs are the authoritative primary: run[c,s].up = flow c→s.
	for key, res := range cells {
		c, s := splitFlowKey(key)
		if res.UpMbit > 0 {
			k := flowKey(c, s)
			fc := out[k]
			fc.Mbit = res.UpMbit
			fc.Retr = res.Retransmits
			fc.RTTms = res.RTTms
			out[k] = fc
		}
	}
	// Pass 2 — down legs: run[c,s].down = flow s→c. Fill the primary only when
	// no up leg claimed it (old/one-way peers); otherwise it's the confirm.
	for key, res := range cells {
		c, s := splitFlowKey(key)
		if res.DownMbit > 0 {
			k := flowKey(s, c)
			fc := out[k]
			if fc.Mbit == 0 {
				fc.Mbit = res.DownMbit
			} else {
				fc.Confirm = res.DownMbit
			}
			out[k] = fc
		}
	}
	// Pass 3 — errors: a failed run would have measured both its flows; mark
	// only those the mirror run did not already measure (never clobber a rate).
	for key, res := range cells {
		if res.Err == "" {
			continue
		}
		c, s := splitFlowKey(key)
		for _, k := range []string{flowKey(c, s), flowKey(s, c)} {
			fc := out[k]
			if fc.Mbit == 0 {
				fc.Err = res.Err
				out[k] = fc
			}
		}
	}
	return out
}

// asymmetric reports whether two mirror-direction rates differ meaningfully:
// both measured and the slower under 80% of the faster.
func asymmetric(ab, ba float64) bool {
	if ab <= 0 || ba <= 0 {
		return false
	}
	lo, hi := ab, ba
	if lo > hi {
		lo, hi = hi, lo
	}
	return lo < 0.80*hi
}

// linkPct returns rate ÷ min(endpoint link speeds), or -1 when unknown (either
// endpoint's link speed is 0 → old peer → grade absolute instead).
func linkPct(mbit float64, aMbit, bMbit int) float64 {
	lo := aMbit
	if bMbit < lo {
		lo = bMbit
	}
	if lo <= 0 {
		return -1
	}
	return mbit / float64(lo)
}

// pctBucket maps a %-of-link fraction to severity: >=0.85 good, >=0.50 watch,
// else bad. Callers must guard pct < 0 (unknown) and fall back to absolute.
func pctBucket(pct float64) color.NRGBA {
	switch {
	case pct >= 0.85:
		return colGood
	case pct >= 0.50:
		return colWatch
	default:
		return colBad
	}
}
