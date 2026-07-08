package ui

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"netlogger/internal/appcore"
	"netlogger/internal/store"
)

type navTab int

const (
	navDashboard navTab = iota
	navTests
	navEvents
)

func tabLabel(t navTab) string {
	switch t {
	case navTests:
		return "Tests"
	case navEvents:
		return "Events"
	default:
		return "Dashboard"
	}
}

// nextTab returns the selected tab (explicit selection wins); pure for testing.
func nextTab(current, selected navTab) navTab { return selected }

// subLabel names the Tests sub-views. Pure.
func subLabel(i int) string {
	switch i {
	case 1:
		return "Stress"
	case 2:
		return "Internet"
	default:
		return "Speed (LAN)"
	}
}

// stressHealthColor maps a link's aborted flag to the severity palette. Pure.
func stressHealthColor(aborted bool) color.NRGBA {
	if aborted {
		return colBad
	}
	return colGood
}

// testsState holds the Tests tab's widget + result state across frames.
// mu guards matrix/haveMatrix/running/status because SpeedSweep runs on a
// background goroutine while the UI reads these every frame.
type testsState struct {
	runBtn      widget.Clickable
	sweepStop   widget.Clickable
	mu          sync.Mutex
	matrix      appcore.SpeedMatrix
	haveMatrix  bool
	running     bool
	status      string
	sweep       appcore.SweepProgress // live fill while a sweep runs (guarded by mu)
	sweepCancel context.CancelFunc    // stops the running sweep (guarded by mu)
	lastReq     appcore.SpeedReq      // config of the running/last sweep (guarded by mu)

	// Sweep controls (frame-thread-only): direction, duration, parallel streams.
	swDirSegs [4]widget.Clickable // Both / Down / Up / Bidir
	swDir     int
	swDurSegs [2]widget.Clickable // 10s / 30s
	swDur     int
	swStrSegs [3]widget.Clickable // 1 / 4 / 8 streams
	swStreams int

	sub         int // 0 Speed, 1 Stress, 2 Internet (placeholder until Build #3)
	speedSeg    widget.Clickable
	stressSeg   widget.Clickable
	internetSeg widget.Clickable
	startStress widget.Clickable
	stopStress  widget.Clickable
	capDec      widget.Clickable
	capInc      widget.Clickable
	capMbit     int // per-link cap; 0 → stressDefaultCap
	stressMu    sync.Mutex
	stressOn    bool
	stressNodes []appcore.StressStatus
	// stressGen invalidates stale poll goroutines across stop→start cycles;
	// stressSelf/stressPeers pin the node set the ACTIVE run was started with so
	// Stop reaches every loaded node even if discovery has since dropped one.
	stressGen   int
	stressSelf  appcore.PeerInfo
	stressPeers []appcore.PeerInfo
	stressMsg   string // start-failure notice (empty when fine)

	internetRun  widget.Clickable
	internetMu   sync.Mutex
	internetOn   bool
	internetHave bool
	internetRes  appcore.InternetResult
	internetProg appcore.InternetProgress // live phase/rate while a test runs
	internetAt   time.Time                // when the last result landed
	internetHost string                   // node the running/last test executed on

	// Frame-thread-only view state (no background writers).
	netNodeID  string                       // internet test target node ("" = this device)
	nodeClicks map[string]*widget.Clickable // node-picker chips, keyed by node id
	epBtn      widget.Clickable             // endpoint dropdown trigger
	epOpen     bool                         // endpoint dropdown expanded
	epSel      string                       // pinned server name ("" = auto)
	epClicks   map[string]*widget.Clickable // endpoint option rows
	cellClicks map[string]*widget.Clickable // matrix cells, keyed From\x00To
	selPairKey string                       // matrix pair opened for detail ("" = none)
	netHist    []store.TestResult           // recent internet runs (newest first)
	sweepHist  []store.TestResult           // recent sweep runs (newest first)
	stressHist []store.TestResult           // recent stress runs (newest first)
	histAt     time.Time                    // last history refresh
}

// clickFor returns a persistent Clickable for key from m (allocating on first use).
func clickFor(m *map[string]*widget.Clickable, key string) *widget.Clickable {
	if *m == nil {
		*m = make(map[string]*widget.Clickable)
	}
	c := (*m)[key]
	if c == nil {
		c = new(widget.Clickable)
		(*m)[key] = c
	}
	return c
}

// cap returns the configured per-link cap, clamped to [stressCapMin, stressCapMax].
func (st *testsState) cap() int {
	c := st.capMbit
	if c == 0 {
		c = stressDefaultCap
	}
	if c < stressCapMin {
		c = stressCapMin
	}
	if c > stressCapMax {
		c = stressCapMax
	}
	return c
}

func (st *testsState) snapshot() (appcore.SpeedMatrix, bool, bool, string, appcore.SweepProgress) {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.matrix, st.haveMatrix, st.running, st.status, st.sweep
}

// sweepDirs/sweepDurs/sweepStreams map control indices to request values.
var (
	sweepDirs    = [4]string{"both", "down", "up", "bidir"}
	sweepDirLbls = [4]string{"Both", "Down", "Up", "Bidir"}
	sweepDurs    = [2]int{10, 30}
	sweepStreams = [3]int{1, 4, 8}
)

// sweepReq builds the SpeedReq for the configured controls (frame thread).
// The first second is omitted (-O 1) so averages reflect steady state, not
// TCP slow-start.
func (st *testsState) sweepReq() appcore.SpeedReq {
	return appcore.SpeedReq{
		Direction: sweepDirs[st.swDir],
		DurationS: sweepDurs[st.swDur],
		Streams:   sweepStreams[st.swStreams],
		OmitS:     1,
	}
}

// sweepControls renders the direction / duration / streams segmented controls
// and handles their clicks (frame thread). Hidden while a sweep runs.
func sweepControls(gtx layout.Context, th *material.Theme, st *testsState) layout.Dimensions {
	for i := range st.swDirSegs {
		if st.swDirSegs[i].Clicked(gtx) {
			st.swDir = i
		}
	}
	for i := range st.swDurSegs {
		if st.swDurSegs[i].Clicked(gtx) {
			st.swDur = i
		}
	}
	for i := range st.swStrSegs {
		if st.swStrSegs[i].Clicked(gtx) {
			st.swStreams = i
		}
	}
	group := func(label string, seg func(gtx layout.Context) layout.Dimensions) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: unit.Dp(18)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						l := material.Caption(th, label)
						l.Color = colTextSec
						return l.Layout(gtx)
					}),
					layout.Rigid(gapX(8)),
					layout.Rigid(seg),
				)
			})
		})
	}
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		group("Direction", func(gtx layout.Context) layout.Dimensions {
			return segControl(gtx, th,
				segSpec{click: &st.swDirSegs[0], label: sweepDirLbls[0], active: st.swDir == 0},
				segSpec{click: &st.swDirSegs[1], label: sweepDirLbls[1], active: st.swDir == 1},
				segSpec{click: &st.swDirSegs[2], label: sweepDirLbls[2], active: st.swDir == 2},
				segSpec{click: &st.swDirSegs[3], label: sweepDirLbls[3], active: st.swDir == 3},
			)
		}),
		group("Duration", func(gtx layout.Context) layout.Dimensions {
			return segControl(gtx, th,
				segSpec{click: &st.swDurSegs[0], label: "10s", active: st.swDur == 0},
				segSpec{click: &st.swDurSegs[1], label: "30s", active: st.swDur == 1},
			)
		}),
		group("Streams", func(gtx layout.Context) layout.Dimensions {
			return segControl(gtx, th,
				segSpec{click: &st.swStrSegs[0], label: "1", active: st.swStreams == 0},
				segSpec{click: &st.swStrSegs[1], label: "4", active: st.swStreams == 1},
				segSpec{click: &st.swStrSegs[2], label: "8", active: st.swStreams == 2},
			)
		}),
	)
}

// stressSnapshot reads the stress run state safely (the poll goroutine writes
// these while the UI reads them every frame).
func (st *testsState) stressSnapshot() (bool, []appcore.StressStatus, string) {
	st.stressMu.Lock()
	defer st.stressMu.Unlock()
	return st.stressOn, st.stressNodes, st.stressMsg
}

// layoutTests renders the Tests tab: a segmented control (Speed / Stress) on
// top of the matching sub-view.
func layoutTests(gtx layout.Context, th *material.Theme, st *testsState, snap appcore.Snapshot) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layoutSubSeg(gtx, th, st)
		}),
		layout.Rigid(gap(14)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			switch st.sub {
			case 1:
				return layoutStress(gtx, th, st, snap)
			case 2:
				return layoutInternet(gtx, th, st, snap)
			default:
				return layoutSpeed(gtx, th, st, snap)
			}
		}),
	)
}

// layoutSubSeg draws a two-button segmented control switching st.sub. The active
// segment uses the accent background (like the active nav tab); the inactive one
// uses colCardAlt + colTextPri.
func layoutSubSeg(gtx layout.Context, th *material.Theme, st *testsState) layout.Dimensions {
	return segControl(gtx, th,
		segSpec{click: &st.speedSeg, label: subLabel(0), active: st.sub == 0},
		segSpec{click: &st.stressSeg, label: subLabel(1), active: st.sub == 1},
		segSpec{click: &st.internetSeg, label: subLabel(2), active: st.sub == 2},
	)
}

// historyList renders recent persisted test results as compact rows:
// "Jul 2 14:06 · label · 581↓ 332↑ · detail".
func historyList(gtx layout.Context, th *material.Theme, rows []store.TestResult) layout.Dimensions {
	if len(rows) == 0 {
		return layout.Dimensions{}
	}
	ch := make([]layout.FlexChild, 0, len(rows)+2)
	ch = append(ch, layout.Rigid(gap(16)), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		lbl := material.Caption(th, "Recent")
		lbl.Color = colTextMut
		return lbl.Layout(gtx)
	}))
	for _, r := range rows {
		r := r
		ch = append(ch, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return roundedBG(gtx, colCardAlt, unit.Dp(6), unit.Dp(8), func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							l := material.Caption(th, time.UnixMicro(r.TSUnixUS).Format("Jan 2 15:04"))
							l.Color = colTextMut
							gtx.Constraints.Min.X = gtx.Dp(unit.Dp(84))
							return l.Layout(gtx)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							l := material.Body2(th, r.Label)
							l.Color = colTextPri
							return l.Layout(gtx)
						}),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: gtx.Constraints.Min} }),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if r.DownMbit == 0 && r.UpMbit == 0 {
								return layout.Dimensions{} // stress rows carry no rates
							}
							l := material.Body2(th, fmt.Sprintf("↓ %s  ↑ %s", fmtRate(r.DownMbit), fmtRate(r.UpMbit)))
							l.Color = colTextSec
							return l.Layout(gtx)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if r.Detail == "" {
								return layout.Dimensions{}
							}
							return layout.Inset{Left: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								l := material.Caption(th, r.Detail)
								l.Color = colTextMut
								return l.Layout(gtx)
							})
						}),
					)
				})
			})
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
}

// sweepStatusLine describes a live sweep for the header caption. Pure.
func sweepStatusLine(p appcore.SweepProgress) string {
	if p.Total == 0 {
		return ""
	}
	s := fmt.Sprintf("%d/%d pairs", p.Done, p.Total)
	names := make([]string, 0, len(p.Active))
	for _, pr := range p.Active {
		names = append(names, pr.From.Host+" → "+pr.To.Host)
	}
	sort.Strings(names) // stable across frames
	if len(names) > 0 {
		s += " · testing " + strings.Join(names, ", ")
	}
	return s
}

// layoutSpeed renders the Speed (LAN) sub-view: a header (title · subtitle · the
// Run-all primary action) above the test matrix. While a sweep runs the matrix
// fills in live: completed cells land as they finish, active pairs pulse.
func layoutSpeed(gtx layout.Context, th *material.Theme, st *testsState, snap appcore.Snapshot) layout.Dimensions {
	matrix, have, running, status, sweep := st.snapshot()
	// Per-node link speed (Mbit/s) for %-of-link grading, keyed by node id.
	// Old peers report 0 → linkPct falls back to absolute thresholds.
	linkSpeed := map[string]int{snap.SelfPeer.ID: snap.SelfPeer.LinkSpeedMbit}
	for _, p := range snap.Peers {
		linkSpeed[p.ID] = p.LinkSpeedMbit
	}
	live := running && sweep.Total > 0
	if live {
		matrix = appcore.SpeedMatrix{Nodes: sweep.Nodes, Cells: sweep.Cells}
		have = true
		status = sweepStatusLine(sweep)
	}
	var active map[string]bool
	if live {
		active = make(map[string]bool, len(sweep.Active))
		for _, pr := range sweep.Active {
			active[speedKeyUI(pr.From.ID, pr.To.ID)] = true
		}
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return material.Body1(th, "Test matrix").Layout(gtx)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							sub := "download · every live pair"
							if status != "" {
								sub = status
							}
							lbl := material.Caption(th, sub)
							lbl.Color = colTextMut
							if live {
								lbl.Color = colTextSec
							}
							return lbl.Layout(gtx)
						}),
					)
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: gtx.Constraints.Min} }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if running {
						return dangerBtn(gtx, th, &st.sweepStop, "Stop")
					}
					return primaryBtn(gtx, th, &st.runBtn, "Run all pairs")
				}),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if running {
				return layout.Dimensions{} // controls locked while a sweep runs
			}
			return layout.Inset{Top: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return sweepControls(gtx, th, st)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if !live {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return loadBar(gtx, float64(sweep.Done)/float64(sweep.Total), colAccent)
			})
		}),
		layout.Rigid(gap(14)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if !have {
				lbl := material.Caption(th, "no results yet — run a sweep")
				lbl.Color = colTextMut
				return lbl.Layout(gtx)
			}
			return layoutSpeedMatrix(gtx, th, st, matrix, active, sweep.Live, linkSpeed)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return pairDetail(gtx, th, st, matrix, linkSpeed)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return historyList(gtx, th, st.sweepHist)
		}),
	)
}

// linkSpeedLabel renders a link speed in Mbit/s compactly ("1 Gb", "2.5 Gb",
// "100 Mb") for the "% of <n> link" provenance. Pure.
func linkSpeedLabel(mbit int) string {
	if mbit >= 1000 {
		g := float64(mbit) / 1000
		return strconv.FormatFloat(g, 'f', -1, 64) + " Gb"
	}
	return fmt.Sprintf("%d Mb", mbit)
}

// pairDetail shows the clicked flow's full result: the row→column rate, its
// %-of-link grade, retransmits, and the reverse direction; the chart plots the
// measured per-second series for this direction plus (when present) the mirror
// run's confirming series.
func pairDetail(gtx layout.Context, th *material.Theme, st *testsState, m appcore.SpeedMatrix, linkSpeed map[string]int) layout.Dimensions {
	if st.selPairKey == "" {
		return layout.Dimensions{}
	}
	flows := flowCells(m.Cells)
	fc, ok := flows[st.selPairKey]
	if !ok {
		return layout.Dimensions{}
	}
	hostByID := make(map[string]string, len(m.Nodes))
	for _, n := range m.Nodes {
		hostByID[n.ID] = n.Host
	}
	from, to := splitFlowKey(st.selPairKey)
	var txt string
	if fc.Mbit > 0 {
		txt = fmt.Sprintf("%s → %s   %s", hostByID[from], hostByID[to], fmtRate(fc.Mbit))
		lo := linkSpeed[from]
		if linkSpeed[to] < lo {
			lo = linkSpeed[to]
		}
		if pct := linkPct(fc.Mbit, linkSpeed[from], linkSpeed[to]); pct >= 0 {
			txt += fmt.Sprintf(" · %.0f%% of %s link", pct*100, linkSpeedLabel(lo))
		}
		txt += fmt.Sprintf(" · retransmits %d", fc.Retr)
		if fc.RTTms > 0 {
			txt += fmt.Sprintf(" · rtt %.1f ms", fc.RTTms)
		}
		if fc.Confirm > 0 {
			txt += fmt.Sprintf(" · reverse direction %s", fmtRate(fc.Confirm))
		}
	} else {
		txt = fmt.Sprintf("%s → %s   error: %s", hostByID[from], hostByID[to], fc.Err)
	}
	// Per-second series: this direction = run[from,to] up leg (from→to); reverse
	// confirm = run[to,from] down leg. Fall back to the mirror down leg when no
	// up leg ran (one-way peer). Both on one shared zero-based scale.
	runFwd := m.Cells[speedKeyUI(from, to)]
	runRev := m.Cells[speedKeyUI(to, from)]
	primaryIvs, confirmIvs := runFwd.UpIvs, runRev.DownIvs
	if len(primaryIvs) < 2 {
		primaryIvs, confirmIvs = runRev.DownIvs, nil
	}
	var series [][]float64
	var cols []color.NRGBA
	var names []string
	if len(primaryIvs) > 1 {
		series, cols = append(series, primaryIvs), append(cols, seriesColors[0])
		names = append(names, "this direction")
	}
	if len(confirmIvs) > 1 {
		series, cols = append(series, confirmIvs), append(cols, seriesColors[1])
		names = append(names, "reverse")
	}
	return layout.Inset{Top: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return roundedBG(gtx, colCardAlt, unit.Dp(8), unit.Dp(10), func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					l := material.Body2(th, txt)
					l.Color = colTextPri
					if fc.Mbit == 0 {
						l.Color = colBad
					}
					return l.Layout(gtx)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if len(series) == 0 {
						return layout.Dimensions{}
					}
					return layout.Inset{Top: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								_, mx := chartBounds(series, 0)
								return scaledChart(gtx, th, series, cols, 56, 0, mx, fmtRate(mx), -1)
							}),
							layout.Rigid(gap(6)),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return legendRow(gtx, th, names)
							}),
						)
					})
				}),
			)
		})
	})
}

// layoutStress renders the Stress sub-view: a full-mesh start/stop kill-switch
// and a live per-node / per-link readout.
const (
	stressDefaultCap = 200
	stressCapMin     = 50
	stressCapMax     = 2000
	stressCapStep    = 50
)

func layoutStress(gtx layout.Context, th *material.Theme, st *testsState, snap appcore.Snapshot) layout.Dimensions {
	on, nodes, msg := st.stressSnapshot()
	n := len(snap.Peers) + 1
	pairs := n * (n - 1) / 2
	active := on || len(nodes) > 0 // a run is live or just finished polling
	// Run-start marker position within the 60-sample window: at the right edge
	// when the run just began, scrolling left over the first minute, off once
	// the window no longer contains the start.
	var elapsed int64
	if len(nodes) > 0 && nodes[0].StartedUnixUS > 0 {
		elapsed = (time.Now().UnixMicro() - nodes[0].StartedUnixUS) / 1_000_000
	}
	markFrac := -1.0
	if elapsed >= 0 && elapsed <= 60 {
		markFrac = 1 - float64(elapsed)/60
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		// running banner, or the Start control when idle.
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if on {
				return stressBanner(gtx, th, st, nodes, pairs)
			}
			if len(snap.Peers) == 0 {
				lbl := material.Caption(th, "no peers online — a stress test needs at least two nodes")
				lbl.Color = colTextMut
				return lbl.Layout(gtx)
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return primaryBtn(gtx, th, &st.startStress, "Start stress test")
				}),
				layout.Rigid(gapX(16)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return capStepper(gtx, th, st)
				}),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if msg == "" {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				lbl := material.Caption(th, msg)
				lbl.Color = colBad
				return lbl.Layout(gtx)
			})
		}),
		layout.Rigid(gap(12)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			// The cap chip is redundant with the stepper beside Start, so it only
			// appears while running (when the stepper is hidden).
			chips := []string{"Topology · full mesh"}
			if on {
				chips = append(chips, fmt.Sprintf("Cap · %s", fmtRate(float64(st.cap()))))
			}
			chips = append(chips, "Protocol · TCP", "Probes · continuous")
			return configChips(gtx, th, chips...)
		}),
		layout.Rigid(gap(14)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if active {
				// RRUL-aligned panel: throughput over latency on one shared time
				// axis, one legend for both, then the per-link list.
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return stressRateChart(gtx, th, nodes, snap, st.cap(), markFrac)
					}),
					layout.Rigid(gap(10)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return stressLatencyChart(gtx, th, snap, markFrac)
					}),
					layout.Rigid(gap(6)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return stressTimeCaption(gtx, th, elapsed)
					}),
					layout.Rigid(gap(10)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return legendRow(gtx, th, stressLinkNames(nodes, snap))
					}),
					layout.Rigid(gap(14)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layoutStressList(gtx, th, nodes, snap, st.cap())
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return historyList(gtx, th, st.stressHist)
					}),
				)
			}
			// Idle: the recent-runs history, or a quiet caption when there is none.
			if len(st.stressHist) > 0 {
				return historyList(gtx, th, st.stressHist)
			}
			lbl := material.Caption(th, "no active run")
			lbl.Color = colTextMut
			return lbl.Layout(gtx)
		}),
	)
}

// stressRateChart draws each loaded link's per-second achieved rate on one
// shared scale — throughput under load, streamed live from the iperf3 clients.
// Read together with the latency chart below it: rate sag + RTT spike on the
// same second is the load-triggered fault caught in the act.
func stressRateChart(gtx layout.Context, th *material.Theme, nodes []appcore.StressStatus, snap appcore.Snapshot, cap int, markFrac float64) layout.Dimensions {
	var series [][]float64
	for _, n := range nodes {
		for _, l := range n.Links {
			if len(l.RateHist) >= 2 {
				series = append(series, l.RateHist)
			}
		}
	}
	if len(series) == 0 {
		return layout.Dimensions{}
	}
	_, mx := chartBounds(series, float64(cap))
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.Caption(th, "Throughput per link")
			lbl.Color = colTextMut
			return lbl.Layout(gtx)
		}),
		layout.Rigid(gap(6)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return scaledChart(gtx, th, series, seriesColors, 56, 0, mx, fmtRate(mx), markFrac)
		}),
	)
}

// stressLinkName labels one directed link "source → target". Every device is
// a TARGET of N-1 links, so destination-only labels read as duplicates; the
// source is what tells two "→ sarah-pc" rows apart. Pre-1.3.3 peers report no
// Host — those keep the destination-only form. Pure.
func stressLinkName(srcHost, dstName string) string {
	if srcHost == "" {
		return "→ " + dstName
	}
	return srcHost + " → " + dstName
}

// stressLinkNames lists the loaded links as "source → target", in the same
// order stressRateChart draws their series — the shared legend for the RRUL panel.
func stressLinkNames(nodes []appcore.StressStatus, snap appcore.Snapshot) []string {
	var names []string
	for _, n := range nodes {
		for _, l := range n.Links {
			if len(l.RateHist) >= 2 {
				names = append(names, stressLinkName(n.Host, snap.DeviceName(l.Target)))
			}
		}
	}
	return names
}

// stressTimeCaption is the shared RRUL time axis under the stacked charts:
// run start (or "last 60 s" once the run window has scrolled) on the left, "now"
// on the right.
func stressTimeCaption(gtx layout.Context, th *material.Theme, elapsed int64) layout.Dimensions {
	left := "last 60 s"
	if elapsed < 60 {
		left = "run start"
	}
	cap := func(txt string) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			l := material.Label(th, unit.Sp(10), txt)
			l.Color = colTextMut
			return l.Layout(gtx)
		}
	}
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(cap(left)),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: gtx.Constraints.Min} }),
		layout.Rigid(cap("now")),
	)
}

// legendRow renders color-dot + name chips matching seriesColors order.
func legendRow(gtx layout.Context, th *material.Theme, names []string) layout.Dimensions {
	ch := make([]layout.FlexChild, 0, len(names))
	for i, name := range names {
		i, name := i, name
		ch = append(ch, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: unit.Dp(14)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(dotWidget(seriesColors[i%len(seriesColors)], 8)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: unit.Dp(5)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							lbl := material.Caption(th, name)
							lbl.Color = colTextSec
							return lbl.Layout(gtx)
						})
					}),
				)
			})
		}))
	}
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, ch...)
}

// scaledChart wraps multiSparklineRange with a labeled y-axis (max over 0).
// fmtMax renders the top label (e.g. fmtRate or "80 ms").
func scaledChart(gtx layout.Context, th *material.Theme, serieses [][]float64, cols []color.NRGBA, hDp int, min, max float64, fmtMax string, markFrac float64) layout.Dimensions {
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			w, h := gtx.Dp(unit.Dp(52)), gtx.Dp(unit.Dp(hDp))
			gtx.Constraints.Min = image.Pt(w, h)
			gtx.Constraints.Max = image.Pt(w, h)
			return layout.Inset{Right: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				axisLbl := func(txt string) layout.Widget {
					return func(gtx layout.Context) layout.Dimensions {
						return layout.E.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							l := material.Label(th, unit.Sp(10), txt)
							l.Color = colTextMut
							return l.Layout(gtx)
						})
					}
				}
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(axisLbl(fmtMax)),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: gtx.Constraints.Min} }),
					layout.Rigid(axisLbl("0")),
				)
			})
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			w := int(gtx.Metric.PxToDp(gtx.Constraints.Max.X))
			return multiSparklineRange(gtx, serieses, cols, w, hDp, min, max, markFrac)
		}),
	)
}

// capStepper is the per-link bandwidth control: − value + (ghost buttons).
func capStepper(gtx layout.Context, th *material.Theme, st *testsState) layout.Dimensions {
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.Caption(th, "Per-link cap")
			lbl.Color = colTextSec
			return lbl.Layout(gtx)
		}),
		layout.Rigid(gapX(8)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return ghostBtn(gtx, th, &st.capDec, "−") }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(8), Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Dp(unit.Dp(86))
				lbl := material.Body2(th, fmtRate(float64(st.cap())))
				lbl.Color = colTextPri
				return lbl.Layout(gtx)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return ghostBtn(gtx, th, &st.capInc, "+") }),
	)
}

// seriesColors is the validated series palette (dark surface #161d29; all six
// dataviz checks pass, protan ΔE 58 for the first three). Severity colors
// (colGood/colWatch/colBad) are RESERVED for state and never reused as a series
// color, so a healthy line never draws in the palette's alarm color.
var seriesColors = []color.NRGBA{
	{R: 0x4f, G: 0x8f, B: 0xf7, A: 0xff}, // blue
	{R: 0xc6, G: 0x77, B: 0x18, A: 0xff}, // orange
	{R: 0x0b, G: 0xab, B: 0x9e, A: 0xff}, // teal
	{R: 0x9a, G: 0x7e, B: 0xf0, A: 0xff}, // violet (4th+: legend chips carry identity)
	{R: 0xd4, G: 0x69, B: 0x9e, A: 0xff}, // pink
}

// stressLatencyChart draws the per-peer RTT histories on one shared scale — the
// "latency under load" readout: lines spike as the stress test saturates links.
func stressLatencyChart(gtx layout.Context, th *material.Theme, snap appcore.Snapshot, markFrac float64) layout.Dimensions {
	var series [][]float64
	for _, p := range snap.Peers {
		if len(p.RTTHist) >= 2 {
			series = append(series, p.RTTHist)
		}
	}
	if len(series) == 0 {
		return layout.Dimensions{}
	}
	_, mx := chartBounds(series, 0)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.Caption(th, "Latency")
			lbl.Color = colTextMut
			return lbl.Layout(gtx)
		}),
		layout.Rigid(gap(6)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return scaledChart(gtx, th, series, seriesColors, 56, 0, mx, fmt.Sprintf("%.0f ms", mx), markFrac)
		}),
	)
}

// stressBanner is the running strip: status dot, state, descriptor, elapsed/total
// timer, and the Stop kill-switch. See docs/design-guide.md (status banner).
func stressBanner(gtx layout.Context, th *material.Theme, st *testsState, nodes []appcore.StressStatus, pairs int) layout.Dimensions {
	var elapsed, total int64
	if len(nodes) > 0 {
		s := nodes[0]
		if s.StartedUnixUS > 0 {
			elapsed = (time.Now().UnixMicro() - s.StartedUnixUS) / 1_000_000
		}
		if s.EndsUnixUS > s.StartedUnixUS {
			total = (s.EndsUnixUS - s.StartedUnixUS) / 1_000_000
		}
	}
	desc := fmt.Sprintf("full mesh · %d link-pairs · all nodes loading", pairs)
	return widget.Border{Color: colBad, Width: unit.Dp(1), CornerRadius: unit.Dp(10)}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			return roundedBG(gtx, colBadTint, unit.Dp(10), unit.Dp(12), func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(dotWidget(colBad, 9)),
					layout.Rigid(gapX(10)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						l := material.Body2(th, "Stress running")
						l.Color = colTextPri
						l.Font.Weight = 500
						return l.Layout(gtx)
					}),
					layout.Rigid(gapX(12)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						l := material.Caption(th, desc)
						l.Color = colTextSec
						return l.Layout(gtx)
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: gtx.Constraints.Min} }),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						l := material.Body2(th, mmss(elapsed)+" / "+mmss(total))
						l.Color = colTextSec
						return l.Layout(gtx)
					}),
					layout.Rigid(gapX(12)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return dangerBtn(gtx, th, &st.stopStress, "Stop")
					}),
				)
			})
		})
}

// configChips lays out a row of "key · value" pills describing the run parameters.
func configChips(gtx layout.Context, th *material.Theme, items ...string) layout.Dimensions {
	ch := make([]layout.FlexChild, 0, len(items)*2)
	for i, s := range items {
		if i > 0 {
			ch = append(ch, layout.Rigid(gapX(6)))
		}
		s := s
		ch = append(ch, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return chipLabel(gtx, th, s, colTextSec, colCardAlt)
		}))
	}
	return layout.Flex{}.Layout(gtx, ch...)
}

// mmss formats seconds as M:SS.
func mmss(sec int64) string {
	if sec < 0 {
		sec = 0
	}
	return fmt.Sprintf("%d:%02d", sec/60, sec%60)
}

// fmtRate renders a Mbit/s value with an explicit unit: whole Mb/s below
// 1 Gbit, Gb/s above (two decimals under 10 Gb/s, one above). Pure.
func fmtRate(mbit float64) string {
	switch {
	case mbit <= 0:
		return "0 Mb/s"
	case mbit >= 1000:
		g := mbit / 1000
		if g >= 10 {
			return fmt.Sprintf("%.1f Gb/s", g)
		}
		return fmt.Sprintf("%.2f Gb/s", g)
	default:
		return fmt.Sprintf("%.0f Mb/s", mbit)
	}
}

// layoutStressList renders one row per (node, link): the target device (name large,
// IP small), a load bar (sent vs cap), and a health label/dot.
func layoutStressList(gtx layout.Context, th *material.Theme, nodes []appcore.StressStatus, snap appcore.Snapshot, cap int) layout.Dimensions {
	rows := make([]layout.FlexChild, 0)
	for _, n := range nodes {
		for _, l := range n.Links {
			l, src := l, n.Host
			rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return roundedBG(gtx, colCardAlt, unit.Dp(6), unit.Dp(8), func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(dotWidget(stressHealthColor(l.Aborted), 8)),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								gtx.Constraints.Min.X = gtx.Dp(unit.Dp(196))
								gtx.Constraints.Max.X = gtx.Dp(unit.Dp(196))
								return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return deviceLabel(gtx, th, stressLinkName(src, snap.DeviceName(l.Target)), l.Target)
								})
							}),
							layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								col := colGood
								if l.Aborted {
									col = colBad
								} else if l.SentMbit < float64(cap)*0.5 {
									col = colWatch
								}
								return layout.Inset{Left: unit.Dp(8), Right: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return loadBar(gtx, l.SentMbit/float64(cap), col)
								})
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								txt := fmtRate(l.SentMbit)
								if l.Retransmits > 0 {
									txt += fmt.Sprintf(" · retr %d", l.Retransmits)
								}
								col := colTextSec
								if l.Aborted {
									txt, col = "aborted", colBad
								}
								l2 := material.Body2(th, txt)
								l2.Color = col
								return l2.Layout(gtx)
							}),
						)
					})
				})
			}))
		}
	}
	if len(rows) == 0 {
		lbl := material.Caption(th, "starting…")
		lbl.Color = colTextMut
		return lbl.Layout(gtx)
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}

// loadBar draws a recessed track with a fraction filled in colour c.
func loadBar(gtx layout.Context, frac float64, c color.NRGBA) layout.Dimensions {
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	w := gtx.Constraints.Max.X
	h := gtx.Dp(unit.Dp(8))
	paint.FillShape(gtx.Ops, colTrack, clip.UniformRRect(image.Rect(0, 0, w, h), gtx.Dp(unit.Dp(4))).Op(gtx.Ops))
	fw := int(float64(w) * frac)
	if fw > 0 {
		paint.FillShape(gtx.Ops, c, clip.UniformRRect(image.Rect(0, 0, fw, h), gtx.Dp(unit.Dp(4))).Op(gtx.Ops))
	}
	return layout.Dimensions{Size: image.Pt(w, h)}
}

// matrixCellColor maps a download Mbit/s to the severity palette. Pure.
func matrixCellColor(downMbit float64) color.NRGBA {
	switch {
	case downMbit >= 900:
		return colGood
	case downMbit >= 400:
		return colWatch
	default:
		return colBad
	}
}

// matrixCellText renders a cell's download value ("—" for not-run < 0). Pure.
func matrixCellText(downMbit float64) string {
	if downMbit < 0 {
		return "—"
	}
	return fmtRate(downMbit)
}

// speedKeyUI builds the SpeedMatrix.Cells key (mirrors appcore's speedKey).
func speedKeyUI(from, to string) string { return from + "\x00" + to }

// layoutSpeedMatrix draws the directed speed grid with uniform column widths
// and a fixed row height. Every column (including the row-label column) uses an
// equal flex weight so cells never bleed; a small inset is the gutter. Cells in
// `active` are currently under test: they show the per-second rate streaming
// in from the running iperf3 (live), or "testing" before the first interval.
// Clicking a completed cell toggles the pair-detail panel below the grid.
func layoutSpeedMatrix(gtx layout.Context, th *material.Theme, st *testsState, m appcore.SpeedMatrix, active map[string]bool, live map[string]appcore.LivePoint, linkSpeed map[string]int) layout.Dimensions {
	if len(m.Nodes) == 0 {
		return material.Body2(th, "no live devices").Layout(gtx)
	}
	flows := flowCells(m.Cells)
	// Show the %-of-link legend only when at least two nodes report a link speed
	// (so at least one pair can be graded); otherwise the absolute legend.
	known := 0
	for _, n := range m.Nodes {
		if linkSpeed[n.ID] > 0 {
			known++
		}
	}
	linkGraded := known >= 2
	cellH := gtx.Dp(unit.Dp(46))
	labelW := gtx.Dp(unit.Dp(104))
	gut := unit.Dp(5)

	// labelCell is the fixed-width, left-aligned row-label / corner cell. Fixed
	// width (not flexed) so the data columns get the rest of the width and the
	// labels don't float in dead space.
	labelCell := func(txt string) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min = image.Pt(labelW, cellH)
			gtx.Constraints.Max.X = labelW
			return layout.Inset{Right: gut}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.W.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					lbl := material.Caption(th, txt)
					lbl.Color = colTextSec
					return lbl.Layout(gtx)
				})
			})
		})
	}

	// headerCol is an equal-weight centered column header.
	headerCol := func(txt string) layout.FlexChild {
		return layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: gut, Bottom: gut}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min = image.Pt(gtx.Constraints.Max.X, cellH)
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					lbl := material.Caption(th, txt)
					lbl.Color = colTextSec
					return lbl.Layout(gtx)
				})
			})
		})
	}

	// cellBody paints a rounded filled rect with a centered value and an optional
	// smaller second line (RTT, "slow", loss %), filling its equal-weight flex
	// column and the fixed row height.
	cellBody := func(bg, fg color.NRGBA, txt, sub string) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			size := image.Pt(gtx.Constraints.Max.X, cellH)
			gtx.Constraints.Min = size
			rect := image.Rectangle{Max: size}
			paint.FillShape(gtx.Ops, bg, clip.UniformRRect(rect, gtx.Dp(unit.Dp(6))).Op(gtx.Ops))
			layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						lbl := material.Body2(th, txt)
						lbl.Color = fg
						return lbl.Layout(gtx)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if sub == "" {
							return layout.Dimensions{}
						}
						lbl := material.Label(th, unit.Sp(10), sub)
						lbl.Color = fg
						return lbl.Layout(gtx)
					}),
				)
			})
			return layout.Dimensions{Size: size}
		}
	}
	dataCell := func(bg, fg color.NRGBA, txt, sub string) layout.FlexChild {
		return layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: gut, Bottom: gut}.Layout(gtx, cellBody(bg, fg, txt, sub))
		})
	}
	// clickableCell is a dataCell that toggles the pair-detail panel.
	clickableCell := func(c *widget.Clickable, bg, fg color.NRGBA, txt, sub string) layout.FlexChild {
		return layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: gut, Bottom: gut}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return hoverCursor(gtx, func(gtx layout.Context) layout.Dimensions {
					return material.Clickable(gtx, c, cellBody(bg, fg, txt, sub))
				})
			})
		})
	}

	rows := make([]layout.FlexChild, 0, len(m.Nodes)+1)

	// header row: fixed corner cell (axis key) + one centered bare header per node.
	header := make([]layout.FlexChild, 0, len(m.Nodes)+1)
	header = append(header, labelCell("from ↓ · to →"))
	for _, n := range m.Nodes {
		header = append(header, headerCol(n.Host))
	}
	rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, header...)
	}))

	// one row per "from" node: fixed label + equal-weight data cells.
	for _, from := range m.Nodes {
		from := from
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			cells := make([]layout.FlexChild, 0, len(m.Nodes)+1)
			cells = append(cells, labelCell(from.Host))
			for _, to := range m.Nodes {
				key := speedKeyUI(from.ID, to.ID)
				switch {
				case from.ID == to.ID:
					cells = append(cells, dataCell(colCardAlt, colTextMut, "—", ""))
				case active[key]:
					txt, sub := "testing", "•••"
					if lp, ok := live[key]; ok && lp.Mbit > 0 {
						txt, sub = fmtRate(lp.Mbit), phaseArrow(lp.Phase)+" testing"
					}
					cells = append(cells, dataCell(colTestingBG, colAccent, txt, sub))
				default:
					fc, ok := flows[key]
					if !ok || (fc.Mbit == 0 && fc.Err == "") {
						cells = append(cells, dataCell(colCardAlt, colTextMut, "·", ""))
						break
					}
					click := clickFor(&st.cellClicks, key)
					if click.Clicked(gtx) {
						if st.selPairKey == key {
							st.selPairKey = "" // second click closes the detail
						} else {
							st.selPairKey = key
						}
					}
					bg, fg, txt, sub := colCardAlt, colTextMut, "·", ""
					if fc.Mbit > 0 {
						pct := linkPct(fc.Mbit, linkSpeed[from.ID], linkSpeed[to.ID])
						if pct >= 0 {
							bg = pctBucket(pct)
						} else {
							bg = matrixCellColor(fc.Mbit)
						}
						fg = colBg
						txt = fmtRate(fc.Mbit)
						// Mark this cell when it is the slower half of an asymmetric
						// mirror pair (row→col meaningfully slower than col→row).
						rev := flows[speedKeyUI(to.ID, from.ID)].Mbit
						if asymmetric(fc.Mbit, rev) && fc.Mbit < rev {
							txt += " ▲"
						}
						sub = flowCellSub(fc, pct)
					}
					cells = append(cells, clickableCell(click, bg, fg, txt, sub))
				}
			}
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, cells...)
		}))
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
		}),
		layout.Rigid(gap(8)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					lbl := material.Caption(th, "cell = data flowing from row to column · ▲ slower than reverse")
					lbl.Color = colTextMut
					return lbl.Layout(gtx)
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: gtx.Constraints.Min} }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return matrixLegend(gtx, th, linkGraded) }),
			)
		}),
	)
}

// phaseArrow maps a live point's phase to its direction glyph. Pure.
func phaseArrow(phase string) string {
	switch phase {
	case "down":
		return "↓"
	case "up":
		return "↑"
	case "bidir":
		return "⇅"
	default:
		return "·"
	}
}

// flowCellSub is the small second line under a flow cell's rate: %-of-link when
// graded, else a slow/RTT hint.
func flowCellSub(fc flowCell, pct float64) string {
	switch {
	case pct >= 0:
		return fmt.Sprintf("%.0f%% of link", pct*100)
	case fc.Mbit > 0 && fc.Mbit < 400:
		return "slow"
	case fc.RTTms > 0:
		return fmt.Sprintf("%.1f ms", fc.RTTms)
	default:
		return ""
	}
}

// matrixLegend is the severity key shown under the matrix: a %-of-link scale
// when link speeds are known, else the absolute Mb/s scale.
func matrixLegend(gtx layout.Context, th *material.Theme, linkGraded bool) layout.Dimensions {
	item := func(c color.NRGBA, txt string) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						sz := gtx.Dp(unit.Dp(10))
						paint.FillShape(gtx.Ops, c, clip.UniformRRect(image.Rect(0, 0, sz, sz), gtx.Dp(unit.Dp(2))).Op(gtx.Ops))
						return layout.Dimensions{Size: image.Pt(sz, sz)}
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: unit.Dp(5)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							lbl := material.Caption(th, txt)
							lbl.Color = colTextMut
							return lbl.Layout(gtx)
						})
					}),
				)
			})
		})
	}
	if linkGraded {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			item(colGood, "≥85% of link"), item(colWatch, "50–85%"), item(colBad, "<50%"))
	}
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		item(colGood, "≥900 Mb/s"), item(colWatch, "400–900"), item(colBad, "<400"))
}
