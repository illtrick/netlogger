package appcore

import "time"

// testWindow is a time span during which a test ran. The heatmap overlays these
// so hovering a cell shows what test was running at that moment.
type testWindow struct {
	fromUS int64
	toUS   int64
	label  string
}

// ~25h, a little over the heatmap's 24h window, so spans stay visible all day.
const testWindowRetentionUS = 25 * 3600 * 1_000_000

// recordTestEvent logs an informational test event to the connectivity timeline
// (shown in the Events tab and the mesh-wide event log).
func (a *App) recordTestEvent(detail string) { a.recordEvent(true, detail) }

// markTestSpan opens a heatmap-visible window [now, now+plannedMS] for a running
// test and returns a finalize func that clamps the window's end to the real stop.
func (a *App) markTestSpan(label string, plannedMS int64) func() {
	now := time.Now().UnixMicro()
	w := &testWindow{fromUS: now, toUS: now + plannedMS*1000, label: label}
	a.testWinMu.Lock()
	a.testWindows = append(a.testWindows, w)
	cut := now - testWindowRetentionUS
	kept := a.testWindows[:0]
	for _, x := range a.testWindows {
		if x.toUS >= cut {
			kept = append(kept, x)
		}
	}
	a.testWindows = kept
	a.testWinMu.Unlock()
	return func() {
		a.testWinMu.Lock()
		w.toUS = time.Now().UnixMicro()
		a.testWinMu.Unlock()
	}
}

// BeginSpeedTestNote logs a speed-test event and opens its heatmap window; the
// returned func closes the window when the run finishes. Called from the UI.
func (a *App) BeginSpeedTestNote() func() {
	a.recordTestEvent("speed test (all pairs)")
	return a.markTestSpan("speed test", 0)
}

// testNotes returns, per bucket on the [fromSec, bucketSec, buckets] grid, the
// labels of any test windows overlapping that bucket (joined; "" when none).
func (a *App) testNotes(fromSec int64, bucketSec, buckets int) []string {
	a.testWinMu.Lock()
	wins := make([]testWindow, len(a.testWindows))
	for i, w := range a.testWindows {
		wins[i] = *w
	}
	a.testWinMu.Unlock()
	notes := make([]string, buckets)
	for b := 0; b < buckets; b++ {
		lo := (fromSec + int64(b*bucketSec)) * 1_000_000
		hi := lo + int64(bucketSec)*1_000_000
		for _, w := range wins {
			if w.fromUS < hi && w.toUS >= lo {
				if notes[b] != "" {
					notes[b] += ", "
				}
				notes[b] += w.label
			}
		}
	}
	return notes
}
