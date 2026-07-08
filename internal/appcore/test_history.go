package appcore

import (
	"fmt"
	"time"

	"netlogger/internal/store"
)

// recordTestResult persists a completed test for the history views (no-op until
// the store is open; store is set once in Start before any test can run).
func (a *App) recordTestResult(r store.TestResult) {
	if a.store != nil {
		_ = a.store.InsertTestResult(r)
	}
}

// TestHistory returns the most recent `limit` persisted results of kind
// ("internet" or "sweep"), newest first.
func (a *App) TestHistory(kind string, limit int) []store.TestResult {
	if a.store == nil {
		return nil
	}
	out, _ := a.store.TestResults(kind, limit)
	return out
}

// SweepConfigLine renders a run's settings for provenance chips/history.
// e.g. "both · 10s · 4 streams".
func SweepConfigLine(req SpeedReq) string {
	d := req.Direction
	if d == "" {
		d = "both"
	}
	streams := req.Streams
	if streams <= 0 {
		streams = 1
	}
	return fmt.Sprintf("%s · %ds · %d streams", d, req.DurationS, streams)
}

// sweepSummary condenses a finished sweep into one history row: how many pairs
// succeeded, the slowest link (the diagnostic headline), and the run config so a
// row's numbers are read against the settings that produced them. ok is false
// when no pair produced a result (nothing worth recording).
func sweepSummary(nodes []SpeedNode, cells map[string]SpeedResult, req SpeedReq) (store.TestResult, bool) {
	hostByID := make(map[string]string, len(nodes))
	for _, n := range nodes {
		hostByID[n.ID] = n.Host
	}
	okCount := 0
	slowKey := ""
	var slow SpeedResult
	for k, res := range cells {
		if res.Err != "" || res.DownMbit <= 0 {
			continue
		}
		okCount++
		if slowKey == "" || res.DownMbit < slow.DownMbit {
			slowKey, slow = k, res
		}
	}
	if okCount == 0 {
		return store.TestResult{}, false
	}
	fromID, toID := splitSpeedKey(slowKey)
	return store.TestResult{
		TSUnixUS: time.Now().UnixMicro(),
		Kind:     "sweep",
		Label:    fmt.Sprintf("%d/%d pairs", okCount, len(cells)),
		DownMbit: slow.DownMbit,
		UpMbit:   slow.UpMbit,
		Detail:   "slowest " + hostByID[fromID] + " → " + hostByID[toID] + " · " + SweepConfigLine(req),
	}, true
}

// splitSpeedKey undoes speedKey's From\x00To encoding.
func splitSpeedKey(k string) (from, to string) {
	for i := 0; i < len(k); i++ {
		if k[i] == 0 {
			return k[:i], k[i+1:]
		}
	}
	return k, ""
}
