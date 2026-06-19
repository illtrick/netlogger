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
}

func (st *testsState) snapshot() (appcore.SpeedMatrix, bool, bool, string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.matrix, st.haveMatrix, st.running, st.status
}

// layoutTests renders the Tests tab (Speed sub-view for Build #1).
func layoutTests(gtx layout.Context, th *material.Theme, st *testsState) layout.Dimensions {
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
			return material.Button(th, &st.runBtn, label).Layout(gtx)
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

	// header label: a Caption in the requested color, centered/aligned in a
	// fixed-height cell with the gutter inset applied.
	headerCell := func(txt string, align layout.Direction) layout.FlexChild {
		return layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: unit.Dp(3), Bottom: unit.Dp(3)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min = image.Pt(gtx.Constraints.Max.X, cellH)
				return align.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						lbl := material.Caption(th, txt)
						lbl.Color = colTextSec
						return lbl.Layout(gtx)
					})
				})
			})
		})
	}

	// dataCell paints a rounded filled rect with centered text, filling its flex
	// column and the fixed row height.
	dataCell := func(bg, fg color.NRGBA, txt string) layout.FlexChild {
		return layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: unit.Dp(3), Bottom: unit.Dp(3)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min = image.Pt(gtx.Constraints.Max.X, cellH)
				size := image.Pt(gtx.Constraints.Max.X, cellH)
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

	// header row: empty row-label cell + one header per node.
	header := make([]layout.FlexChild, 0, len(m.Nodes)+1)
	header = append(header, headerCell("", layout.Center))
	for _, n := range m.Nodes {
		header = append(header, headerCell(n.Host, layout.Center))
	}
	rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, header...)
	}))

	// one row per "from" node.
	for _, from := range m.Nodes {
		from := from
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			cells := make([]layout.FlexChild, 0, len(m.Nodes)+1)
			cells = append(cells, headerCell(from.Host, layout.E))
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
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, cells...)
		}))
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}
