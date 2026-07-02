package ui

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"sort"
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
				return layoutSpeed(gtx, th, st)
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
							l := material.Body2(th, fmt.Sprintf("%.0f↓ %.0f↑", r.DownMbit, r.UpMbit))
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
func layoutSpeed(gtx layout.Context, th *material.Theme, st *testsState) layout.Dimensions {
	matrix, have, running, status, sweep := st.snapshot()
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
							sub := "download Mbit/s · every live pair"
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
			return layoutSpeedMatrix(gtx, th, st, matrix, active)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return pairDetail(gtx, th, st, matrix)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return historyList(gtx, th, st.sweepHist)
		}),
	)
}

// pairDetail shows the clicked matrix cell's full result: both directions plus
// the TCP/UDP health numbers hidden inside the cell.
func pairDetail(gtx layout.Context, th *material.Theme, st *testsState, m appcore.SpeedMatrix) layout.Dimensions {
	if st.selPairKey == "" {
		return layout.Dimensions{}
	}
	res, ok := m.Cells[st.selPairKey]
	if !ok {
		return layout.Dimensions{}
	}
	hostByID := make(map[string]string, len(m.Nodes))
	for _, n := range m.Nodes {
		hostByID[n.ID] = n.Host
	}
	from, to := st.selPairKey, ""
	for i := 0; i < len(st.selPairKey); i++ {
		if st.selPairKey[i] == 0 {
			from, to = st.selPairKey[:i], st.selPairKey[i+1:]
			break
		}
	}
	txt := fmt.Sprintf("%s → %s   ↓ %.0f  ↑ %.0f Mbit/s   retransmits %d",
		hostByID[from], hostByID[to], res.DownMbit, res.UpMbit, res.Retransmits)
	if res.RTTms > 0 {
		txt += fmt.Sprintf("   rtt %.1f ms", res.RTTms)
	}
	if res.JitterMs > 0 || res.LossPct > 0 {
		txt += fmt.Sprintf("   jitter %.1f ms   loss %.1f%%", res.JitterMs, res.LossPct)
	}
	if res.Err != "" {
		txt = fmt.Sprintf("%s → %s   error: %s", hostByID[from], hostByID[to], res.Err)
	}
	return layout.Inset{Top: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return roundedBG(gtx, colCardAlt, unit.Dp(8), unit.Dp(10), func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			l := material.Body2(th, txt)
			l.Color = colTextPri
			if res.Err != "" {
				l.Color = colBad
			}
			return l.Layout(gtx)
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
			return configChips(gtx, th,
				"Topology · full mesh",
				fmt.Sprintf("Per-link cap · %d Mbit/s", st.cap()),
				"Protocol · TCP",
				"Probes · continuous",
			)
		}),
		layout.Rigid(gap(14)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return stressLatencyChart(gtx, th, snap)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if !on && len(nodes) == 0 {
				lbl := material.Caption(th, "no active run")
				lbl.Color = colTextMut
				return lbl.Layout(gtx)
			}
			return layoutStressList(gtx, th, nodes, snap, st.cap())
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
				lbl := material.Body2(th, fmt.Sprintf("%d Mbit/s", st.cap()))
				lbl.Color = colTextPri
				return lbl.Layout(gtx)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return ghostBtn(gtx, th, &st.capInc, "+") }),
	)
}

// linkColors cycles per-link line/legend colors for the latency chart.
var linkColors = []color.NRGBA{colBad, colAccent, colGood, colWatch, {R: 0x7e, G: 0xe0, B: 0xa0, A: 0xff}}

// stressLatencyChart draws the per-peer RTT histories on one shared scale — the
// "latency under load" readout: lines spike as the stress test saturates links.
func stressLatencyChart(gtx layout.Context, th *material.Theme, snap appcore.Snapshot) layout.Dimensions {
	var series [][]float64
	var names []string
	for _, p := range snap.Peers {
		if len(p.RTTHist) >= 2 {
			series = append(series, p.RTTHist)
			names = append(names, p.Host)
		}
	}
	if len(series) == 0 {
		return layout.Dimensions{}
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.Caption(th, "Latency under load · last minute")
			lbl.Color = colTextMut
			return lbl.Layout(gtx)
		}),
		layout.Rigid(gap(6)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			w := int(gtx.Metric.PxToDp(gtx.Constraints.Max.X))
			return multiSparkline(gtx, series, linkColors, w, 80)
		}),
		layout.Rigid(gap(6)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			ch := make([]layout.FlexChild, 0, len(names))
			for i, name := range names {
				i, name := i, name
				ch = append(ch, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Right: unit.Dp(14)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(dotWidget(linkColors[i%len(linkColors)], 8)),
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
		}),
		layout.Rigid(gap(14)),
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

// layoutStressList renders one row per (node, link): the target device (name large,
// IP small), a load bar (sent vs cap), and a health label/dot.
func layoutStressList(gtx layout.Context, th *material.Theme, nodes []appcore.StressStatus, snap appcore.Snapshot, cap int) layout.Dimensions {
	rows := make([]layout.FlexChild, 0)
	for _, n := range nodes {
		for _, l := range n.Links {
			l := l
			rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return roundedBG(gtx, colCardAlt, unit.Dp(6), unit.Dp(8), func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(dotWidget(stressHealthColor(l.Aborted), 8)),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								gtx.Constraints.Min.X = gtx.Dp(unit.Dp(150))
								gtx.Constraints.Max.X = gtx.Dp(unit.Dp(150))
								return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return deviceLabel(gtx, th, snap.DeviceName(l.Target), l.Target)
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
								txt := fmt.Sprintf("%.0f Mbit/s", l.SentMbit)
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
	return fmt.Sprintf("%.0f", downMbit)
}

// speedKeyUI builds the SpeedMatrix.Cells key (mirrors appcore's speedKey).
func speedKeyUI(from, to string) string { return from + "\x00" + to }

// layoutSpeedMatrix draws the directed speed grid with uniform column widths
// and a fixed row height. Every column (including the row-label column) uses an
// equal flex weight so cells never bleed; a small inset is the gutter. Cells in
// `active` are currently under test and render as a pulsing accent state.
// Clicking a completed cell toggles the pair-detail panel below the grid.
func layoutSpeedMatrix(gtx layout.Context, th *material.Theme, st *testsState, m appcore.SpeedMatrix, active map[string]bool) layout.Dimensions {
	if len(m.Nodes) == 0 {
		return material.Body2(th, "no live devices").Layout(gtx)
	}
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

	// header row: fixed corner cell + one centered header per node.
	header := make([]layout.FlexChild, 0, len(m.Nodes)+1)
	header = append(header, labelCell(""))
	for _, n := range m.Nodes {
		header = append(header, headerCol("↓ "+n.Host))
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
					cells = append(cells, dataCell(colTestingBG, colAccent, "testing", "•••"))
				default:
					res, ok := m.Cells[key]
					if !ok {
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
					if res.Err == "" {
						bg, fg = matrixCellColor(res.DownMbit), colBg
						txt, sub = matrixCellText(res.DownMbit), matrixCellSub(res)
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
					lbl := material.Caption(th, "row = client (sender) → col = server")
					lbl.Color = colTextMut
					return lbl.Layout(gtx)
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: gtx.Constraints.Min} }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return matrixLegend(gtx, th) }),
			)
		}),
	)
}

// matrixCellSub is the small second line in a matrix cell: loss, "slow", or RTT.
func matrixCellSub(res appcore.SpeedResult) string {
	switch {
	case res.LossPct > 0:
		return fmt.Sprintf("%.1f%% loss", res.LossPct)
	case res.DownMbit > 0 && res.DownMbit < 400:
		return "slow"
	case res.RTTms > 0: // mean TCP RTT, when the platform reports it
		return fmt.Sprintf("%.1f ms", res.RTTms)
	case res.UpMbit > 0: // otherwise show the upload leg (down/up asymmetry)
		return fmt.Sprintf("↑ %.0f", res.UpMbit)
	default:
		return ""
	}
}

// matrixLegend is the severity key shown under the matrix.
func matrixLegend(gtx layout.Context, th *material.Theme) layout.Dimensions {
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
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		item(colGood, "≥900"), item(colWatch, "400–900"), item(colBad, "<400"))
}
