package ui

import (
	"fmt"
	"image"
	"image/color"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"netlogger/internal/appcore"
)

// sevColor maps a loss percent to a CVD-safe severity color (Wong palette).
func sevColor(lossPct float64, hasData bool) color.NRGBA {
	if !hasData {
		return color.NRGBA{R: 0x99, G: 0x99, B: 0x99, A: 0xff}
	}
	switch {
	case lossPct < 0.1:
		return color.NRGBA{R: 0x00, G: 0x9E, B: 0x73, A: 0xff} // good
	case lossPct < 1.0:
		return color.NRGBA{R: 0xE6, G: 0x9F, B: 0x00, A: 0xff} // warn
	default:
		return color.NRGBA{R: 0xD5, G: 0x5E, B: 0x00, A: 0xff} // bad
	}
}

func cellLabel(c appcore.MatrixCell, hasData bool) string {
	if !hasData {
		return "–" // en-dash
	}
	return fmt.Sprintf("%.1f%%", c.LossPct)
}

// matrixCell paints a single fixed-size colored cell with an optional centered label.
func matrixCell(gtx layout.Context, th *material.Theme, bg color.NRGBA, lbl string) layout.Dimensions {
	w, h := gtx.Dp(unit.Dp(92)), gtx.Dp(unit.Dp(52))
	sz := image.Pt(w, h)
	func() {
		defer clip.Rect{Min: image.Pt(2, 2), Max: image.Pt(w-2, h-2)}.Push(gtx.Ops).Pop()
		paint.ColorOp{Color: bg}.Add(gtx.Ops)
		paint.PaintOp{}.Add(gtx.Ops)
	}()
	if lbl != "" {
		cgtx := gtx
		cgtx.Constraints = layout.Exact(sz)
		layout.Center.Layout(cgtx, func(gtx layout.Context) layout.Dimensions {
			t := material.Body2(th, lbl)
			t.Color = color.NRGBA{R: 0x06, G: 0x12, B: 0x1f, A: 0xff}
			return t.Layout(gtx)
		})
	}
	return layout.Dimensions{Size: sz}
}

// headerCell paints a fixed-size label cell (no background).
func headerCell(gtx layout.Context, th *material.Theme, w int, s string) layout.Dimensions {
	sz := image.Pt(gtx.Dp(unit.Dp(w)), gtx.Dp(unit.Dp(26)))
	cgtx := gtx
	cgtx.Constraints = layout.Exact(sz)
	layout.Center.Layout(cgtx, func(gtx layout.Context) layout.Dimensions {
		return material.Caption(th, s).Layout(gtx)
	})
	return layout.Dimensions{Size: sz}
}

// layoutMatrix renders a color-coded N×N link matrix.
func layoutMatrix(gtx layout.Context, th *material.Theme, m appcore.Matrix) layout.Dimensions {
	if len(m.Nodes) == 0 {
		return material.Body1(th, "Link matrix: waiting for peers…").Layout(gtx)
	}

	diag := color.NRGBA{R: 0x33, G: 0x3a, B: 0x40, A: 0xff}

	// Build header row: corner cell + one column header per node.
	headerRow := func(gtx layout.Context) layout.Dimensions {
		children := make([]layout.FlexChild, 0, len(m.Nodes)+1)
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return headerCell(gtx, th, 96, "src \\ dst")
		}))
		for _, n := range m.Nodes {
			n := n
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return headerCell(gtx, th, 92, n.Host)
			}))
		}
		return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, children...)
	}

	// Build one data row per source node.
	rows := make([]layout.FlexChild, 0, len(m.Nodes)+1)
	rows = append(rows, layout.Rigid(headerRow))

	for _, src := range m.Nodes {
		src := src
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			cells := make([]layout.FlexChild, 0, len(m.Nodes)+1)
			// Left label.
			cells = append(cells, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return headerCell(gtx, th, 96, src.Host)
			}))
			for _, dst := range m.Nodes {
				dst := dst
				if src.ID == dst.ID {
					cells = append(cells, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return matrixCell(gtx, th, diag, "")
					}))
				} else {
					c, ok := m.Cell(src.ID, dst.ID)
					bg := sevColor(c.LossPct, ok)
					lbl := cellLabel(c, ok)
					cells = append(cells, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return matrixCell(gtx, th, bg, lbl)
					}))
				}
			}
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, cells...)
		}))
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}
