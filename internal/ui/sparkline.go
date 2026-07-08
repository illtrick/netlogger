package ui

import (
	"image"
	"image/color"
	"math"

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

// multiSparkline draws several series on ONE shared min/max scale in a w×h dp box,
// so the lines are directly comparable (used for per-link latency-under-load).
func multiSparkline(gtx layout.Context, serieses [][]float64, cols []color.NRGBA, w, h int) layout.Dimensions {
	sz := image.Pt(gtx.Dp(unit.Dp(w)), gtx.Dp(unit.Dp(h)))
	min, max := math.Inf(1), math.Inf(-1)
	for _, s := range serieses {
		for _, x := range s {
			if x < min {
				min = x
			}
			if x > max {
				max = x
			}
		}
	}
	if math.IsInf(min, 1) {
		return layout.Dimensions{Size: sz}
	}
	span := max - min
	yf := func(v float64) float32 {
		if span == 0 {
			return float32(sz.Y) / 2
		}
		return float32(sz.Y) - float32((v-min)/span)*float32(sz.Y)
	}
	for si, s := range serieses {
		if len(s) < 2 {
			continue
		}
		var p clip.Path
		p.Begin(gtx.Ops)
		xf := func(i int) float32 { return float32(i) / float32(len(s)-1) * float32(sz.X) }
		p.MoveTo(f32.Pt(xf(0), yf(s[0])))
		for i := 1; i < len(s); i++ {
			p.LineTo(f32.Pt(xf(i), yf(s[i])))
		}
		paint.FillShape(gtx.Ops, cols[si%len(cols)], clip.Stroke{Path: p.End(), Width: float32(gtx.Dp(unit.Dp(1.5)))}.Op())
	}
	return layout.Dimensions{Size: sz}
}

// multiSparklineRange draws serieses on ONE FIXED scale [min,max] in a w×h dp
// box. markFrac >= 0 draws a dashed vertical marker at that x fraction (used
// for a stress run's start). Unlike multiSparkline it never auto-normalizes,
// so the caller can pin a zero baseline and label the scale honestly.
func multiSparklineRange(gtx layout.Context, serieses [][]float64, cols []color.NRGBA, w, h int, min, max float64, markFrac float64) layout.Dimensions {
	sz := image.Pt(gtx.Dp(unit.Dp(w)), gtx.Dp(unit.Dp(h)))
	if max <= min {
		return layout.Dimensions{Size: sz} // no honest scale to draw on
	}
	span := max - min
	yf := func(v float64) float32 {
		return float32(sz.Y) - float32((v-min)/span)*float32(sz.Y)
	}
	// Dashed vertical marker (e.g. run-start): stacked 3px dash segments.
	if markFrac >= 0 && markFrac <= 1 {
		x := float32(markFrac) * float32(sz.X)
		dash := gtx.Dp(unit.Dp(3))
		for y := 0; y < sz.Y; y += dash * 2 {
			y2 := y + dash
			if y2 > sz.Y {
				y2 = sz.Y
			}
			var p clip.Path
			p.Begin(gtx.Ops)
			p.MoveTo(f32.Pt(x, float32(y)))
			p.LineTo(f32.Pt(x, float32(y2)))
			paint.FillShape(gtx.Ops, colBorder, clip.Stroke{Path: p.End(), Width: float32(gtx.Dp(unit.Dp(1)))}.Op())
		}
	}
	for si, s := range serieses {
		if len(s) < 2 {
			continue
		}
		var p clip.Path
		p.Begin(gtx.Ops)
		xf := func(i int) float32 { return float32(i) / float32(len(s)-1) * float32(sz.X) }
		p.MoveTo(f32.Pt(xf(0), yf(s[0])))
		for i := 1; i < len(s); i++ {
			p.LineTo(f32.Pt(xf(i), yf(s[i])))
		}
		paint.FillShape(gtx.Ops, cols[si%len(cols)], clip.Stroke{Path: p.End(), Width: float32(gtx.Dp(unit.Dp(1.5)))}.Op())
	}
	return layout.Dimensions{Size: sz}
}

// chartBounds returns [0,max] rounded up to a clean ceiling for zero-based
// charts: max is the data max (or floorMax if larger), padded ~5%.
func chartBounds(serieses [][]float64, floorMax float64) (float64, float64) {
	max := floorMax
	for _, s := range serieses {
		for _, v := range s {
			if v > max {
				max = v
			}
		}
	}
	if max <= 0 {
		return 0, 1
	}
	return 0, max * 1.05
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
