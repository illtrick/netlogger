package ui

import (
	"image"
	"image/color"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// normalize maps a series to 0..1 by min/max. A flat or empty series maps every
// point to 0.5. Returns nil for nil/empty input.
func normalize(v []float64) []float64 {
	if len(v) == 0 {
		return nil
	}
	min, max := v[0], v[0]
	for _, x := range v {
		if x < min {
			min = x
		}
		if x > max {
			max = x
		}
	}
	out := make([]float64, len(v))
	if max == min {
		for i := range out {
			out[i] = 0.5
		}
		return out
	}
	for i, x := range v {
		out[i] = (x - min) / (max - min)
	}
	return out
}

// sparkline draws series as a line chart filling a w×h dp box.
func sparkline(gtx layout.Context, series []float64, col color.NRGBA, w, h int) layout.Dimensions {
	sz := image.Pt(gtx.Dp(unit.Dp(w)), gtx.Dp(unit.Dp(h)))
	n := normalize(series)
	if len(n) >= 2 {
		var p clip.Path
		p.Begin(gtx.Ops)
		xf := func(i int) float32 { return float32(i) / float32(len(n)-1) * float32(sz.X) }
		yf := func(v float64) float32 { return float32(sz.Y) - float32(v)*float32(sz.Y) }
		p.MoveTo(f32.Pt(xf(0), yf(n[0])))
		for i := 1; i < len(n); i++ {
			p.LineTo(f32.Pt(xf(i), yf(n[i])))
		}
		paint.FillShape(gtx.Ops, col, clip.Stroke{Path: p.End(), Width: float32(gtx.Dp(unit.Dp(1.5)))}.Op())
	}
	return layout.Dimensions{Size: sz}
}
