package ui

import (
	"fmt"
	"image"
	"image/color"
	"sync"

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

	sub         int // 0 Speed, 1 Stress
	speedSeg    widget.Clickable
	stressSeg   widget.Clickable
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
func layoutTests(gtx layout.Context, th *material.Theme, st *testsState) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layoutSubSeg(gtx, th, st)
		}),
		layout.Rigid(gap(14)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if st.sub == 1 {
				return layoutStress(gtx, th, st)
			}
			return layoutSpeed(gtx, th, st)
		}),
	)
}

// layoutSubSeg draws a two-button segmented control switching st.sub. The active
// segment uses the accent background (like the active nav tab); the inactive one
// uses colCardAlt + colTextPri.
func layoutSubSeg(gtx layout.Context, th *material.Theme, st *testsState) layout.Dimensions {
	seg := func(b *widget.Clickable, idx int) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				btn := material.Button(th, b, subLabel(idx))
				if st.sub != idx {
					btn.Background = colCardAlt
					btn.Color = colTextPri
				}
				return btn.Layout(gtx)
			})
		})
	}
	return layout.Flex{}.Layout(gtx, seg(&st.speedSeg, 0), seg(&st.stressSeg, 1))
}

// layoutSpeed renders the Speed (LAN) sub-view.
func layoutSpeed(gtx layout.Context, th *material.Theme, st *testsState) layout.Dimensions {
	matrix, have, running, status := st.snapshot()
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return material.Body1(th, "Speed (LAN)").Layout(gtx)
		}),
		layout.Rigid(gap(8)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			label := "Run all pairs"
			if running {
				label = "Running…"
			}
			return layout.Flex{}.Layout(gtx,
				layout.Rigid(material.Button(th, &st.runBtn, label).Layout),
			)
		}),
		layout.Rigid(gap(6)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if status == "" {
				return layout.Dimensions{}
			}
			lbl := material.Caption(th, status)
			lbl.Color = colTextSec
			return lbl.Layout(gtx)
		}),
		layout.Rigid(gap(12)),
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
func layoutStress(gtx layout.Context, th *material.Theme, st *testsState) layout.Dimensions {
	on, nodes := st.stressSnapshot()
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.Caption(th, "Full mesh · per-link cap 200 Mbit/s · TCP")
			lbl.Color = colTextSec
			return lbl.Layout(gtx)
		}),
		layout.Rigid(gap(8)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{}.Layout(gtx, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if on {
					return material.Button(th, &st.stopStress, "Stop").Layout(gtx)
				}
				return material.Button(th, &st.startStress, "Start").Layout(gtx)
			}))
		}),
		layout.Rigid(gap(12)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if !on && len(nodes) == 0 {
				lbl := material.Caption(th, "no active run")
				lbl.Color = colTextMut
				return lbl.Layout(gtx)
			}
			return layoutStressList(gtx, th, nodes)
		}),
	)
}

// layoutStressList renders one row per (node, link): target, sent rate, and a
// health dot colored via stressHealthColor.
func layoutStressList(gtx layout.Context, th *material.Theme, nodes []appcore.StressStatus) layout.Dimensions {
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
								return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									lbl := material.Body2(th, l.Target)
									lbl.Color = colTextPri
									return lbl.Layout(gtx)
								})
							}),
							layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								return layout.Dimensions{Size: gtx.Constraints.Min}
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									lbl := material.Body2(th, fmt.Sprintf("%.0f Mbit/s", l.SentMbit))
									lbl.Color = colTextSec
									return lbl.Layout(gtx)
								})
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

	// dataCell paints a rounded filled rect with centered text, filling its
	// equal-weight flex column and the fixed row height.
	dataCell := func(bg, fg color.NRGBA, txt string) layout.FlexChild {
		return layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: gut, Bottom: gut}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				size := image.Pt(gtx.Constraints.Max.X, cellH)
				gtx.Constraints.Min = size
				rect := image.Rectangle{Max: size}
				paint.FillShape(gtx.Ops, bg, clip.UniformRRect(rect, gtx.Dp(unit.Dp(6))).Op(gtx.Ops))
				layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					lbl := material.Body2(th, txt)
					lbl.Color = fg
					return lbl.Layout(gtx)
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
				switch {
				case from.ID == to.ID:
					cells = append(cells, dataCell(colCardAlt, colTextMut, "—"))
				default:
					res, ok := m.Cells[speedKeyUI(from.ID, to.ID)]
					if ok && res.Err == "" {
						cells = append(cells, dataCell(matrixCellColor(res.DownMbit), colBg, matrixCellText(res.DownMbit)))
					} else {
						cells = append(cells, dataCell(colCardAlt, colTextMut, "·"))
					}
				}
			}
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, cells...)
		}))
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}
