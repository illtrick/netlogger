package ui

// Button hierarchy — see docs/design-guide.md. Four visual roles so a glance tells
// you the consequence of a click: nav (underline), sub-view switch (segmented),
// primary/destructive action (filled accent/red), and minor utility (ghost outline).

import (
	"image"
	"image/color"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// hoverCursor layouts w and registers a pointing-hand cursor over its bounds —
// the universal "this is clickable" affordance every control here should have.
func hoverCursor(gtx layout.Context, w layout.Widget) layout.Dimensions {
	macro := op.Record(gtx.Ops)
	dims := w(gtx)
	call := macro.Stop()
	defer clip.Rect(image.Rectangle{Max: dims.Size}).Push(gtx.Ops).Pop()
	pointer.CursorPointer.Add(gtx.Ops)
	call.Add(gtx.Ops)
	return dims
}

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
	return hoverCursor(gtx, styledBtn(th, c, label, colAccent, colBg, true).Layout)
}

// busyBtn is a primary action in flight: visibly inert (muted surface + text,
// no pointer cursor) so "Running…" doesn't read as clickable.
func busyBtn(gtx layout.Context, th *material.Theme, c *widget.Clickable, label string) layout.Dimensions {
	return styledBtn(th, c, label, colCardAlt, colTextMut, true).Layout(gtx)
}

// dangerBtn is a prominent destructive action (e.g. Stop): filled red, dark text.
func dangerBtn(gtx layout.Context, th *material.Theme, c *widget.Clickable, label string) layout.Dimensions {
	return hoverCursor(gtx, styledBtn(th, c, label, colBad, colBg, true).Layout)
}

func outlineBtn(gtx layout.Context, th *material.Theme, c *widget.Clickable, label string, fg, border color.NRGBA) layout.Dimensions {
	return hoverCursor(gtx, func(gtx layout.Context) layout.Dimensions {
		return widget.Border{Color: border, Width: unit.Dp(1), CornerRadius: unit.Dp(8)}.Layout(gtx,
			func(gtx layout.Context) layout.Dimensions {
				return styledBtn(th, c, label, color.NRGBA{}, fg, false).Layout(gtx)
			})
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

// navPill renders a top-level nav item: the active tab is a raised neutral surface
// (colCard) with primary text; inactive tabs are plain muted text. A raised pill,
// never the bright accent, so nav is clearly distinct from a primary action.
func navPill(gtx layout.Context, th *material.Theme, c *widget.Clickable, label string, active bool) layout.Dimensions {
	return hoverCursor(gtx, func(gtx layout.Context) layout.Dimensions {
		return navPillInner(gtx, th, c, label, active)
	})
}

func navPillInner(gtx layout.Context, th *material.Theme, c *widget.Clickable, label string, active bool) layout.Dimensions {
	return material.Clickable(gtx, c, func(gtx layout.Context) layout.Dimensions {
		bg := color.NRGBA{}
		fg := colTextSec
		if active {
			bg = colCard
			fg = colTextPri
		}
		return roundedBG(gtx, bg, unit.Dp(8), unit.Dp(0), func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6), Left: unit.Dp(13), Right: unit.Dp(13)}.Layout(gtx,
				func(gtx layout.Context) layout.Dimensions {
					lbl := material.Label(th, unit.Sp(14), label)
					lbl.Color = fg
					if active {
						lbl.Font.Weight = 500
					}
					return lbl.Layout(gtx)
				})
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
			return hoverCursor(gtx, func(gtx layout.Context) layout.Dimensions {
				return segItem(gtx, th, s)
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

func segItem(gtx layout.Context, th *material.Theme, s segSpec) layout.Dimensions {
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
}
