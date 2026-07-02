package ui

// App chrome — the fixed title bar (brand · nav · status). See docs/design-guide.md.

import (
	"fmt"
	"image"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"netlogger/internal/appcore"
)

// brand renders the "NetLogger" wordmark (Net primary, Logger accent).
func brand(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Label(th, unit.Sp(16), "Net")
			l.Color = colTextPri
			l.Font.Weight = 600
			return l.Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Label(th, unit.Sp(16), "Logger")
			l.Color = colAccent
			l.Font.Weight = 600
			return l.Layout(gtx)
		}),
	)
}

// titleBar is the fixed top bar: brand on the left, nav pills in the middle, and
// the node-count status + Tray control on the right, on a colTitleBar surface.
func titleBar(gtx layout.Context, th *material.Theme, s appcore.Snapshot, nav navTab, navDash, navTst, navEvt, trayBtn *widget.Clickable) layout.Dimensions {
	pill := func(b *widget.Clickable, t navTab) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return navPill(gtx, th, b, tabLabel(t), nav == t)
			})
		})
	}
	host := s.SelfPeer.Host
	if host == "" {
		host = "this device"
	}
	status := fmt.Sprintf("%s · %d nodes online", host, len(s.Peers)+1)

	row := func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: unit.Dp(8), Bottom: unit.Dp(8), Left: unit.Dp(18), Right: unit.Dp(18)}.Layout(gtx,
			func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return brand(gtx, th) }),
					layout.Rigid(gapX(18)),
					pill(navDash, navDashboard), pill(navTst, navTests), pill(navEvt, navEvents),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: gtx.Constraints.Min} }),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						l := material.Label(th, unit.Sp(12), status)
						l.Color = colTextMut
						return l.Layout(gtx)
					}),
					layout.Rigid(gapX(12)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return ghostBtn(gtx, th, trayBtn, "Tray")
					}),
				)
			})
	}

	// Record the content, paint the full-width bar surface + hairline behind it, replay.
	macro := op.Record(gtx.Ops)
	dims := row(gtx)
	call := macro.Stop()
	w := gtx.Constraints.Max.X
	paint.FillShape(gtx.Ops, colTitleBar, clip.Rect(image.Rect(0, 0, w, dims.Size.Y)).Op())
	paint.FillShape(gtx.Ops, colBorder, clip.Rect(image.Rect(0, dims.Size.Y-gtx.Dp(unit.Dp(1)), w, dims.Size.Y)).Op())
	call.Add(gtx.Ops)
	dims.Size.X = w
	return dims
}

// gapX is a rigid horizontal spacer of n dp.
func gapX(n int) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Dimensions{Size: image.Pt(gtx.Dp(unit.Dp(n)), 0)}
	}
}
