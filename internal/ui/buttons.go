package ui

// Button hierarchy — see docs/design-guide.md. Four visual roles so a glance tells
// you the consequence of a click: nav (underline), sub-view switch (segmented),
// primary/destructive action (filled accent/red), and minor utility (ghost outline).

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

// styledBtn builds a filled material button with the project's insets/radius.
func styledBtn(th *material.Theme, c *widget.Clickable, label string, bg, fg color.NRGBA, big bool) material.ButtonStyle {
	b := material.Button(th, c, label)
	b.Background = bg
	b.Color = fg
	b.CornerRadius = unit.Dp(8)
	if big {
		b.TextSize = unit.Sp(14)
		b.Inset = layout.Inset{Top: unit.Dp(9), Bottom: unit.Dp(9), Left: unit.Dp(16), Right: unit.Dp(16)}
	} else {
		b.TextSize = unit.Sp(12)
		b.Inset = layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6), Left: unit.Dp(12), Right: unit.Dp(12)}
	}
	return b
}

// primaryBtn is the one main call-to-action per view: filled accent, dark text.
func primaryBtn(gtx layout.Context, th *material.Theme, c *widget.Clickable, label string) layout.Dimensions {
	return styledBtn(th, c, label, colAccent, colBg, true).Layout(gtx)
}

// dangerBtn is a prominent destructive action (e.g. Stop): filled red, dark text.
func dangerBtn(gtx layout.Context, th *material.Theme, c *widget.Clickable, label string) layout.Dimensions {
	return styledBtn(th, c, label, colBad, colBg, true).Layout(gtx)
}

func outlineBtn(gtx layout.Context, th *material.Theme, c *widget.Clickable, label string, fg, border color.NRGBA) layout.Dimensions {
	return widget.Border{Color: border, Width: unit.Dp(1), CornerRadius: unit.Dp(8)}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			return styledBtn(th, c, label, color.NRGBA{}, fg, false).Layout(gtx)
		})
}

// ghostBtn is a minor utility action: neutral outline, secondary text.
func ghostBtn(gtx layout.Context, th *material.Theme, c *widget.Clickable, label string) layout.Dimensions {
	return outlineBtn(gtx, th, c, label, colTextSec, colOutline)
}

// dangerGhostBtn is an occasional destructive action (e.g. Reset): red outline.
func dangerGhostBtn(gtx layout.Context, th *material.Theme, c *widget.Clickable, label string) layout.Dimensions {
	return outlineBtn(gtx, th, c, label, colBad, color.NRGBA{R: 0xF8, G: 0x51, B: 0x49, A: 0x55})
}

// navTabBtn renders a top-level nav item: a flat label, muted when inactive, with
// an accent underline (the label's width) when active. No button chrome.
func navTabBtn(gtx layout.Context, th *material.Theme, c *widget.Clickable, label string, active bool) layout.Dimensions {
	return material.Clickable(gtx, c, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Left: unit.Dp(14), Right: unit.Dp(14), Top: unit.Dp(8), Bottom: unit.Dp(6)}.Layout(gtx,
			func(gtx layout.Context) layout.Dimensions {
				lbl := material.Label(th, unit.Sp(15), label)
				lbl.Color = colTextMut
				if active {
					lbl.Color = colTextPri
				}
				macro := op.Record(gtx.Ops)
				dims := lbl.Layout(gtx)
				call := macro.Stop()
				call.Add(gtx.Ops)
				if active {
					y0 := dims.Size.Y + gtx.Dp(unit.Dp(4))
					y1 := y0 + gtx.Dp(unit.Dp(2))
					rect := image.Rect(0, y0, dims.Size.X, y1)
					paint.FillShape(gtx.Ops, colAccent, clip.Rect(rect).Op())
					dims.Size.Y = y1
				}
				return dims
			})
	})
}

// segSpec is one segment of a segControl.
type segSpec struct {
	click  *widget.Clickable
	label  string
	active bool
}

// segControl renders a grouped pill switch for mutually-exclusive sub-views. The
// active segment gets a lighter surface + primary text; the group sits in a bordered
// container so it reads as a switch, not a row of buttons.
func segControl(gtx layout.Context, th *material.Theme, segs ...segSpec) layout.Dimensions {
	children := make([]layout.FlexChild, 0, len(segs))
	for _, s := range segs {
		s := s
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return material.Clickable(gtx, s.click, func(gtx layout.Context) layout.Dimensions {
				bg := color.NRGBA{}
				fg := colTextSec
				if s.active {
					bg = colCard
					fg = colTextPri
				}
				return roundedBG(gtx, bg, unit.Dp(6), unit.Dp(0), func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: unit.Dp(7), Bottom: unit.Dp(7), Left: unit.Dp(16), Right: unit.Dp(16)}.Layout(gtx,
						func(gtx layout.Context) layout.Dimensions {
							lbl := material.Body2(th, s.label)
							lbl.Color = fg
							return lbl.Layout(gtx)
						})
				})
			})
		}))
	}
	return widget.Border{Color: colOutline, Width: unit.Dp(1), CornerRadius: unit.Dp(8)}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			return roundedBG(gtx, colCardAlt, unit.Dp(8), unit.Dp(3), func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{}.Layout(gtx, children...)
			})
		})
}
