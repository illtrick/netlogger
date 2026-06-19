package ui

import (
	"fmt"
	"image"
	"image/color"
	"sync"
	"time"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"netlogger/internal/appcore"
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
	runBtn     widget.Clickable
	mu         sync.Mutex
	matrix     appcore.SpeedMatrix
	haveMatrix bool
	running    bool
	status     string

	sub         int // 0 Speed, 1 Stress, 2 Internet (placeholder until Build #3)
	speedSeg    widget.Clickable
	stressSeg   widget.Clickable
	internetSeg widget.Clickable
	startStress widget.Clickable
	stopStress  widget.Clickable
	stressMu    sync.Mutex
	stressOn    bool
	stressNodes []appcore.StressStatus
}

func (st *testsState) snapshot() (appcore.SpeedMatrix, bool, bool, string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.matrix, st.haveMatrix, st.running, st.status
}

// stressSnapshot reads the stress run state safely (the poll goroutine writes
// these while the UI reads them every frame).
func (st *testsState) stressSnapshot() (bool, []appcore.StressStatus) {
	st.stressMu.Lock()
	defer st.stressMu.Unlock()
	return st.stressOn, st.stressNodes
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
				return layoutInternetPlaceholder(gtx, th)
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

// layoutInternetPlaceholder is shown for the Internet sub-view until Build #3.
func layoutInternetPlaceholder(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return material.Body1(th, "Internet").Layout(gtx)
		}),
		layout.Rigid(gap(8)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.Caption(th, "Device→internet speed test with bufferbloat grade — arriving in the next build.")
			lbl.Color = colTextMut
			return lbl.Layout(gtx)
		}),
	)
}

// layoutSpeed renders the Speed (LAN) sub-view: a header (title · subtitle · the
// Run-all primary action) above the test matrix.
func layoutSpeed(gtx layout.Context, th *material.Theme, st *testsState) layout.Dimensions {
	matrix, have, running, status := st.snapshot()
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
							return lbl.Layout(gtx)
						}),
					)
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: gtx.Constraints.Min} }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					label := "Run all pairs"
					if running {
						label = "Running…"
					}
					return primaryBtn(gtx, th, &st.runBtn, label)
				}),
			)
		}),
		layout.Rigid(gap(14)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if !have {
				lbl := material.Caption(th, "no results yet — run a sweep")
				lbl.Color = colTextMut
				return lbl.Layout(gtx)
			}
			return layoutSpeedMatrix(gtx, th, matrix)
		}),
	)
}

// layoutStress renders the Stress sub-view: a full-mesh start/stop kill-switch
// and a live per-node / per-link readout.
const stressCapMbit = 200

func layoutStress(gtx layout.Context, th *material.Theme, st *testsState, snap appcore.Snapshot) layout.Dimensions {
	on, nodes := st.stressSnapshot()
	n := len(snap.Peers) + 1
	pairs := n * (n - 1) / 2
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		// running banner, or the Start control when idle.
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if on {
				return stressBanner(gtx, th, st, nodes, pairs)
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return primaryBtn(gtx, th, &st.startStress, "Start stress test")
				}),
				layout.Rigid(gapX(12)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					lbl := material.Caption(th, "loads every link, then stops automatically")
					lbl.Color = colTextMut
					return lbl.Layout(gtx)
				}),
			)
		}),
		layout.Rigid(gap(12)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return configChips(gtx, th,
				"Topology · full mesh",
				fmt.Sprintf("Per-link cap · %d Mbit/s", stressCapMbit),
				"Protocol · TCP",
				"Probes · continuous",
			)
		}),
		layout.Rigid(gap(14)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if !on && len(nodes) == 0 {
				lbl := material.Caption(th, "no active run")
				lbl.Color = colTextMut
				return lbl.Layout(gtx)
			}
			return layoutStressList(gtx, th, nodes, snap)
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

// layoutStressList renders one row per (node, link): the target device (name large,
// IP small), a load bar (sent vs cap), and a health label/dot.
func layoutStressList(gtx layout.Context, th *material.Theme, nodes []appcore.StressStatus, snap appcore.Snapshot) layout.Dimensions {
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
								} else if l.SentMbit < float64(stressCapMbit)*0.5 {
									col = colWatch
								}
								return layout.Inset{Left: unit.Dp(8), Right: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return loadBar(gtx, l.SentMbit/float64(stressCapMbit), col)
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
// equal flex weight so cells never bleed; a small inset is the gutter.
func layoutSpeedMatrix(gtx layout.Context, th *material.Theme, m appcore.SpeedMatrix) layout.Dimensions {
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

	// dataCell paints a rounded filled rect with a centered value and an optional
	// smaller second line (RTT, "slow", loss %), filling its equal-weight flex
	// column and the fixed row height.
	dataCell := func(bg, fg color.NRGBA, txt, sub string) layout.FlexChild {
		return layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: gut, Bottom: gut}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
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
				switch {
				case from.ID == to.ID:
					cells = append(cells, dataCell(colCardAlt, colTextMut, "—", ""))
				default:
					res, ok := m.Cells[speedKeyUI(from.ID, to.ID)]
					if ok && res.Err == "" {
						cells = append(cells, dataCell(matrixCellColor(res.DownMbit), colBg, matrixCellText(res.DownMbit), matrixCellSub(res)))
					} else {
						cells = append(cells, dataCell(colCardAlt, colTextMut, "·", ""))
					}
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
	case res.RTTms > 0:
		return fmt.Sprintf("%.1f ms", res.RTTms)
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
