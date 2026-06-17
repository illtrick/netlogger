package ui

import (
	"image"
	"image/color"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// Dark "ops dashboard" palette. Single source of truth — never hardcode a hex
// twice. Mental dark-mode check: primary text light, background near-black.
var (
	colBg       = color.NRGBA{R: 0x0E, G: 0x16, B: 0x20, A: 0xFF}
	colTitleBar = color.NRGBA{R: 0x11, G: 0x1A, B: 0x26, A: 0xFF}
	colCard     = color.NRGBA{R: 0x16, G: 0x1E, B: 0x29, A: 0xFF}
	colCardAlt  = color.NRGBA{R: 0x13, G: 0x1C, B: 0x27, A: 0xFF}
	colBorder   = color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0x12}
	colTextPri  = color.NRGBA{R: 0xEA, G: 0xF1, B: 0xF8, A: 0xFF}
	colTextSec  = color.NRGBA{R: 0x93, G: 0xA1, B: 0xB0, A: 0xFF}
	colTextMut  = color.NRGBA{R: 0x6E, G: 0x7B, B: 0x8A, A: 0xFF}
	colAccent   = color.NRGBA{R: 0x58, G: 0xA6, B: 0xFF, A: 0xFF}
	colGood     = color.NRGBA{R: 0x3F, G: 0xB9, B: 0x50, A: 0xFF}
	colWatch    = color.NRGBA{R: 0xD2, G: 0x99, B: 0x22, A: 0xFF}
	colBad      = color.NRGBA{R: 0xF8, G: 0x51, B: 0x49, A: 0xFF}
)

// Semantic aliases kept so existing call sites read naturally on the new palette.
var (
	blue   = colAccent
	orange = colBad   // discards/errors / offline accents
	amber  = colWatch // power-saving "suspect" highlight
)

// sevColor maps a loss percentage to the severity palette (no-data → muted).
func sevColor(lossPct float64, hasData bool) color.NRGBA {
	if !hasData {
		return colTextMut
	}
	switch {
	case lossPct < 0.1:
		return colGood
	case lossPct < 1.0:
		return colWatch
	default:
		return colBad
	}
}

// gap is a rigid vertical spacer of n dp.
func gap(n int) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Dimensions{Size: image.Pt(0, gtx.Dp(unit.Dp(n)))}
	}
}

// roundedBG paints a filled rounded rect (sized to the padded content) behind w.
func roundedBG(gtx layout.Context, bg color.NRGBA, radius, pad unit.Dp, w layout.Widget) layout.Dimensions {
	macro := op.Record(gtx.Ops)
	dims := layout.UniformInset(pad).Layout(gtx, w)
	call := macro.Stop()
	rect := image.Rectangle{Max: dims.Size}
	paint.FillShape(gtx.Ops, bg, clip.UniformRRect(rect, gtx.Dp(radius)).Op(gtx.Ops))
	call.Add(gtx.Ops)
	return dims
}

// card wraps w in a 12-dp rounded surface (bg + 1px border) with 16-dp padding.
func card(gtx layout.Context, bg, border color.NRGBA, w layout.Widget) layout.Dimensions {
	return widget.Border{Color: border, Width: unit.Dp(1), CornerRadius: unit.Dp(12)}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			return roundedBG(gtx, bg, unit.Dp(12), unit.Dp(16), w)
		})
}

// dotWidget paints a filled status dot of size dp.
func dotWidget(c color.NRGBA, size int) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		r := gtx.Dp(unit.Dp(size))
		rect := image.Rectangle{Max: image.Pt(r, r)}
		paint.FillShape(gtx.Ops, c, clip.Ellipse(rect).Op(gtx.Ops))
		return layout.Dimensions{Size: image.Pt(r, r)}
	}
}

// chipLabel renders a small rounded pill with fg text on a bg fill.
func chipLabel(gtx layout.Context, th *material.Theme, txt string, fg, bg color.NRGBA) layout.Dimensions {
	macro := op.Record(gtx.Ops)
	dims := layout.Inset{Top: unit.Dp(2), Bottom: unit.Dp(2), Left: unit.Dp(7), Right: unit.Dp(7)}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			l := material.Label(th, unit.Sp(11), txt)
			l.Color = fg
			return l.Layout(gtx)
		})
	call := macro.Stop()
	paint.FillShape(gtx.Ops, bg, clip.UniformRRect(image.Rectangle{Max: dims.Size}, gtx.Dp(unit.Dp(5))).Op(gtx.Ops))
	call.Add(gtx.Ops)
	return dims
}

// darkTheme returns a copy of base with the dark palette applied.
func darkTheme(base *material.Theme) *material.Theme {
	th := *base
	th.Palette = material.Palette{
		Bg:         colBg,
		Fg:         colTextPri,
		ContrastBg: colAccent,
		ContrastFg: colBg,
	}
	return &th
}
