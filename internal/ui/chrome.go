package ui

// App chrome — with the window undecorated, this single bar IS the title bar:
// brand · nav · status · caption buttons (– □ ×), with the non-interactive
// stretches acting as the native drag region. See docs/design-guide.md.

import (
	"fmt"
	"image"
	"image/color"

	"gioui.org/f32"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"netlogger/internal/appcore"
)

// chromeState holds the caption-button widgets and the tracked window mode.
type chromeState struct {
	minBtn, maxBtn, closeBtn widget.Clickable
	maximized                bool
}

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

// dragArea layouts w and marks its bounds as a native caption region: the OS
// handles dragging (and double-click maximize) for these stretches of the bar.
func dragArea(gtx layout.Context, w layout.Widget) layout.Dimensions {
	if !customChrome {
		return w(gtx) // native title bar handles dragging
	}
	macro := op.Record(gtx.Ops)
	dims := w(gtx)
	call := macro.Stop()
	defer clip.Rect(image.Rectangle{Max: dims.Size}).Push(gtx.Ops).Pop()
	system.ActionInputOp(system.ActionMove).Add(gtx.Ops)
	call.Add(gtx.Ops)
	return dims
}

// titleBar is the app's one top bar (the window is undecorated): brand on the
// left, nav pills, the node-count status, and the window caption buttons on the
// right. Empty stretches drag the window.
func titleBar(gtx layout.Context, th *material.Theme, s appcore.Snapshot, nav navTab, navDash, navTst, navEvt *widget.Clickable, cs *chromeState) layout.Dimensions {
	barH := gtx.Dp(unit.Dp(44))
	pill := func(b *widget.Clickable, t navTab) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return navTabItem(gtx, th, b, tabLabel(t), nav == t, barH)
			})
		})
	}
	host := s.SelfPeer.Host
	if host == "" {
		host = "this device"
	}
	status := fmt.Sprintf("%s · %d nodes online", host, len(s.Peers)+1)

	row := func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.Y = barH
		gtx.Constraints.Max.Y = barH
		children := []layout.FlexChild{
			// Brand block: draggable (clicking the wordmark does nothing else).
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return dragArea(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(18), Right: unit.Dp(18)}.Layout(gtx,
						func(gtx layout.Context) layout.Dimensions {
							return layout.W.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								gtx.Constraints.Min.Y = barH
								return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return brand(gtx, th)
								})
							})
						})
				})
			}),
			pill(navDash, navDashboard), pill(navTst, navTests), pill(navEvt, navEvents),
			// The big middle stretch: the main drag surface.
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return dragArea(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, barH)}
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return dragArea(gtx, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.Y = barH
					return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						l := material.Label(th, unit.Sp(12), status)
						l.Color = colTextMut
						return l.Layout(gtx)
					})
				})
			}),
		}
		if customChrome {
			children = append(children,
				layout.Rigid(gapX(10)),
				captionBtn(th, &cs.minBtn, glyphMin, false, barH),
				captionBtn(th, &cs.maxBtn, glyphForMax(cs.maximized), false, barH),
				captionBtn(th, &cs.closeBtn, glyphClose, true, barH),
			)
		} else {
			children = append(children, layout.Rigid(gapX(16))) // right margin where captions would be
		}
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, children...)
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

// navTabItem renders one top-level tab, vertically centered in the bar. The
// ACTIVE tab is a raised pill with the grey outline running all the way around
// it — unmistakable without cross-surface tricks (a "connected tab" can't work
// here: the content below the bar is a floating card, not a flush panel).
// Inactive tabs are plain muted labels.
func navTabItem(gtx layout.Context, th *material.Theme, c *widget.Clickable, label string, active bool, barH int) layout.Dimensions {
	return hoverCursor(gtx, func(gtx layout.Context) layout.Dimensions {
		return material.Clickable(gtx, c, func(gtx layout.Context) layout.Dimensions {
			// A full-bar-height box; layout.Center puts the pill on the bar's
			// true midline — same proven primitives as every other control here,
			// no hand-rolled offset math.
			gtx.Constraints.Min.Y = barH
			gtx.Constraints.Max.Y = barH
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				lbl := material.Label(th, unit.Sp(14), label)
				lbl.Color = colTextSec
				if active {
					lbl.Color = colTextPri
					lbl.Font.Weight = 500
				}
				pad := layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6), Left: unit.Dp(16), Right: unit.Dp(16)}
				if !active {
					return pad.Layout(gtx, lbl.Layout)
				}
				// Raised pill with the grey line running all the way around.
				return widget.Border{Color: colOutline, Width: unit.Dp(1), CornerRadius: unit.Dp(8)}.Layout(gtx,
					func(gtx layout.Context) layout.Dimensions {
						return roundedBG(gtx, colCard, unit.Dp(8), unit.Dp(0), func(gtx layout.Context) layout.Dimensions {
							return pad.Layout(gtx, lbl.Layout)
						})
					})
			})
		})
	})
}

// Caption glyph kinds, drawn with vector strokes (font-independent, crisp).
type captionGlyph int

const (
	glyphMin captionGlyph = iota
	glyphMax
	glyphRestore
	glyphClose
)

func glyphForMax(maximized bool) captionGlyph {
	if maximized {
		return glyphRestore
	}
	return glyphMax
}

// captionBtn is one window control: a 46dp-wide hit area with a stroked glyph,
// hover shading (red for close, like the native control), and a hand cursor.
func captionBtn(th *material.Theme, c *widget.Clickable, g captionGlyph, danger bool, barH int) layout.FlexChild {
	return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return hoverCursor(gtx, func(gtx layout.Context) layout.Dimensions {
			return material.Clickable(gtx, c, func(gtx layout.Context) layout.Dimensions {
				size := image.Pt(gtx.Dp(unit.Dp(46)), barH)
				if c.Hovered() {
					bg := colCard
					fg := colTextPri
					if danger {
						bg = colBad
						fg = colBg
					}
					paint.FillShape(gtx.Ops, bg, clip.Rect(image.Rectangle{Max: size}).Op())
					drawCaptionGlyph(gtx, g, size, fg)
				} else {
					drawCaptionGlyph(gtx, g, size, colTextSec)
				}
				return layout.Dimensions{Size: size}
			})
		})
	})
}

// drawCaptionGlyph strokes the –/□/❐/× symbols centered in size.
func drawCaptionGlyph(gtx layout.Context, g captionGlyph, size image.Point, col color.NRGBA) {
	cx, cy := float32(size.X)/2, float32(size.Y)/2
	r := float32(gtx.Dp(unit.Dp(5))) // glyph half-extent
	w := float32(gtx.Dp(unit.Dp(1)))
	if w < 1 {
		w = 1
	}
	stroke := func(segs ...[4]float32) {
		var p clip.Path
		p.Begin(gtx.Ops)
		for _, s := range segs {
			p.MoveTo(f32.Pt(s[0], s[1]))
			p.LineTo(f32.Pt(s[2], s[3]))
		}
		paint.FillShape(gtx.Ops, col, clip.Stroke{Path: p.End(), Width: w}.Op())
	}
	rect := func(x0, y0, x1, y1 float32) {
		stroke([4]float32{x0, y0, x1, y0}, [4]float32{x1, y0, x1, y1},
			[4]float32{x1, y1, x0, y1}, [4]float32{x0, y1, x0, y0})
	}
	switch g {
	case glyphMin:
		stroke([4]float32{cx - r, cy, cx + r, cy})
	case glyphMax:
		rect(cx-r, cy-r, cx+r, cy+r)
	case glyphRestore:
		o := float32(gtx.Dp(unit.Dp(2)))
		rect(cx-r, cy-r+o, cx+r-o, cy+r)                               // front pane
		stroke([4]float32{cx - r + o, cy - r + o, cx - r + o, cy - r}, // back pane hint
			[4]float32{cx - r + o, cy - r, cx + r, cy - r},
			[4]float32{cx + r, cy - r, cx + r, cy + r - o},
			[4]float32{cx + r, cy + r - o, cx + r - o, cy + r - o})
	case glyphClose:
		stroke([4]float32{cx - r, cy - r, cx + r, cy + r}, [4]float32{cx + r, cy - r, cx - r, cy + r})
	}
}

// gapX is a rigid horizontal spacer of n dp.
func gapX(n int) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Dimensions{Size: image.Pt(gtx.Dp(unit.Dp(n)), 0)}
	}
}
