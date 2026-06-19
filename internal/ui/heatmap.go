package ui

import (
	"fmt"
	"image"
	"image/color"
	"time"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"netlogger/internal/appcore"
)

const (
	heatRowH   = 24  // dp per machine row
	heatCellW  = 10  // dp per time bucket
	heatLabelW = 104 // dp for the fixed left labels
)

// heatHover tracks the pointer over the heatmap cells so a hovered cell's detail
// can be shown and the cell highlighted. It is its own event tag (stable across frames).
type heatHover struct {
	active   bool
	pos      f32.Point
	text     string
	mac, bkt int // resolved hovered cell (-1 = none)
	dragging bool
	lastX    float32
}

// panElements converts a horizontal drag of dxPx pixels into a list ScrollBy
// amount in elements (cells): dragging right reveals earlier time (scroll back).
func panElements(dxPx, cellWpx int) float32 {
	if cellWpx <= 0 {
		return 0
	}
	return -float32(dxPx) / float32(cellWpx)
}

var colHeatNone = color.NRGBA{R: 0x24, G: 0x2E, B: 0x3A, A: 0xFF} // visible "no data" cell

// heatColor maps a bucket's loss% (fraction of that bucket's seconds that saw
// loss) to the severity palette; <0 means no samples → a dim but visible cell.
func heatColor(loss float64) color.NRGBA {
	switch {
	case loss < 0:
		return colHeatNone
	case loss == 0:
		return colGood
	case loss < 5:
		return colWatch
	default:
		return colBad
	}
}

func heatCell(gtx layout.Context, loss float64, hl bool) layout.Dimensions {
	cw, ch := gtx.Dp(unit.Dp(heatCellW)), gtx.Dp(unit.Dp(heatRowH))
	g := gtx.Dp(unit.Dp(1))
	if hl { // bright frame behind an inset cell to highlight the hovered cell
		paint.FillShape(gtx.Ops, colTextPri, clip.Rect(image.Rect(0, 0, cw, ch)).Op())
		paint.FillShape(gtx.Ops, heatColor(loss), clip.Rect(image.Rect(g, g, cw-g, ch-g)).Op())
		return layout.Dimensions{Size: image.Pt(cw, ch)}
	}
	paint.FillShape(gtx.Ops, heatColor(loss), clip.Rect(image.Rect(0, 0, cw-g, ch-g)).Op())
	return layout.Dimensions{Size: image.Pt(cw, ch)}
}

// fixedH forces w to exactly h dp tall (so the label column lines up with the
// cell rows).
func fixedH(h int, w layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.Y = gtx.Dp(unit.Dp(h))
		gtx.Constraints.Max.Y = gtx.Constraints.Min.Y
		d := w(gtx)
		d.Size.Y = gtx.Constraints.Min.Y
		return d
	}
}

// layoutHeatmap renders one row per machine on a shared, scrollable time axis: a
// cell is the worst severity across that machine's links/NIC in that bucket, and
// hovering a cell shows what failed and when.
func layoutHeatmap(gtx layout.Context, th *material.Theme, mh appcore.MeshHeat, list *widget.List, hover *heatHover, zoomOut, zoomIn, now *widget.Clickable) layout.Dimensions {
	firstT := time.Unix(mh.FromUnix+int64(list.Position.First*mh.BucketSec), 0)
	title := "Activity by machine · collecting samples…"
	if mh.Buckets > 0 {
		title = fmt.Sprintf("Activity by machine · from ~%s · %s/cell", firstT.Format("15:04"), bucketLabel(mh.BucketSec))
	}

	// Resolve the hovered cell from last frame's pointer position.
	hover.text, hover.mac, hover.bkt = "", -1, -1
	if hover.active && mh.Buckets > 0 && len(mh.Machines) > 0 {
		cw := float32(gtx.Dp(heatCellW))
		rh := float32(gtx.Dp(heatRowH))
		bkt := list.Position.First + int((hover.pos.X+float32(list.Position.Offset))/cw)
		mac := int(hover.pos.Y / rh)
		if mac >= 0 && mac < len(mh.Machines) && bkt >= 0 && bkt < mh.Buckets {
			hover.mac, hover.bkt = mac, bkt
			m := mh.Machines[mac]
			tt := time.Unix(mh.FromUnix+int64(bkt*mh.BucketSec), 0).Format("15:04")
			d := "clean"
			if bkt < len(m.Sev) && m.Sev[bkt] < 0 {
				d = "no data"
			}
			if bkt < len(m.Detail) && m.Detail[bkt] != "" {
				d = m.Detail[bkt]
			}
			hover.text = tt + " · " + m.Host + " — " + d
		}
	}

	header := func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return sectionTitle(gtx, th, title) }),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: gtx.Constraints.Min} }),
					smallBtn(th, zoomOut, "−"),
					smallBtn(th, zoomIn, "+"),
					smallBtn(th, now, "Now"),
				)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				txt := hover.text
				col := colTextPri
				if txt == "" {
					txt, col = "hover a cell for details", colTextMut
				}
				return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					l := material.Label(th, unit.Sp(12), txt)
					l.Color = col
					l.MaxLines = 1
					return l.Layout(gtx)
				})
			}),
		)
	}

	if mh.Buckets == 0 || len(mh.Machines) == 0 {
		return header(gtx)
	}

	grid := func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Dp(unit.Dp(heatLabelW))
				gtx.Constraints.Max.X = gtx.Constraints.Min.X
				ch := make([]layout.FlexChild, 0, len(mh.Machines))
				for i := range mh.Machines {
					label := mh.Machines[i].Host
					ch = append(ch, layout.Rigid(fixedH(heatRowH, func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Right: unit.Dp(8), Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							l := material.Label(th, unit.Sp(13), label)
							l.Color = colTextSec
							l.Alignment = text.End
							l.MaxLines = 1
							return l.Layout(gtx)
						})
					})))
				}
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				dims := material.List(th, list).Layout(gtx, mh.Buckets, func(gtx layout.Context, i int) layout.Dimensions {
					col := make([]layout.FlexChild, 0, len(mh.Machines))
					for r := range mh.Machines {
						sev := -1.0
						if i < len(mh.Machines[r].Sev) {
							sev = mh.Machines[r].Sev[i]
						}
						s := sev
						hl := r == hover.mac && i == hover.bkt
						col = append(col, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return heatCell(gtx, s, hl) }))
					}
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx, col...)
				})
				// Observe pointer hover over the cells WITHOUT blocking the list's
				// scroll/drag: PassOp passes every event through to the list below.
				pass := pointer.PassOp{}.Push(gtx.Ops)
				area := clip.Rect(image.Rectangle{Max: dims.Size}).Push(gtx.Ops)
				event.Op(gtx.Ops, hover)
				area.Pop()
				pass.Pop()
				cwpx := gtx.Dp(unit.Dp(heatCellW))
				strip := float32(gtx.Dp(unit.Dp(16))) // bottom scrollbar strip — leave it to the scrollbar
				for {
					ev, ok := gtx.Event(pointer.Filter{Target: hover, Kinds: pointer.Move | pointer.Press | pointer.Drag | pointer.Release | pointer.Enter | pointer.Leave | pointer.Cancel})
					if !ok {
						break
					}
					pe, ok := ev.(pointer.Event)
					if !ok {
						continue
					}
					switch pe.Kind {
					case pointer.Leave, pointer.Cancel:
						hover.active, hover.dragging = false, false
					case pointer.Press:
						hover.active, hover.pos = true, pe.Position
						if pe.Buttons == pointer.ButtonPrimary && pe.Position.Y < float32(dims.Size.Y)-strip {
							hover.dragging, hover.lastX = true, pe.Position.X
						}
					case pointer.Drag:
						hover.active, hover.pos = true, pe.Position
						if hover.dragging {
							list.ScrollBy(panElements(int(pe.Position.X-hover.lastX), cwpx))
							hover.lastX = pe.Position.X
						}
					case pointer.Release:
						hover.dragging = false
					default: // Move / Enter
						hover.active, hover.pos = true, pe.Position
					}
				}
				return dims
			}),
		)
	}

	axisRow := func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Dimensions{Size: image.Pt(gtx.Dp(unit.Dp(heatLabelW)), gtx.Dp(unit.Dp(16)))}
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return heatAxis(gtx, th, mh, list) }),
		)
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(header),
		layout.Rigid(gap(10)),
		layout.Rigid(grid),
		layout.Rigid(gap(2)),
		layout.Rigid(axisRow),
		layout.Rigid(gap(8)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return heatLegend(gtx, th) }),
	)
}

// heatAxis draws time labels along the bottom, aligned to the scrolled cells (a
// label every ~58 dp). gtx is constrained to the cells width.
func heatAxis(gtx layout.Context, th *material.Theme, mh appcore.MeshHeat, list *widget.List) layout.Dimensions {
	w := gtx.Constraints.Max.X
	h := gtx.Dp(unit.Dp(16))
	cw := gtx.Dp(unit.Dp(heatCellW))
	if cw == 0 || mh.Buckets == 0 {
		return layout.Dimensions{Size: image.Pt(w, h)}
	}
	tickEvery := (gtx.Dp(unit.Dp(58)) + cw - 1) / cw
	if tickEvery < 1 {
		tickEvery = 1
	}
	first := list.Position.First
	off := list.Position.Offset
	for b := first - (first % tickEvery); b < mh.Buckets; b += tickEvery {
		if b < first {
			continue
		}
		x := (b-first)*cw - off
		if x < 0 {
			continue
		}
		if x > w {
			break
		}
		t := time.Unix(mh.FromUnix+int64(b*mh.BucketSec), 0).Format("15:04")
		st := op.Offset(image.Pt(x, 0)).Push(gtx.Ops)
		g2 := gtx
		g2.Constraints.Min = image.Point{}
		g2.Constraints.Max.X = gtx.Dp(unit.Dp(60))
		l := material.Label(th, unit.Sp(10), t)
		l.Color = colTextMut
		l.MaxLines = 1
		l.Layout(g2)
		st.Pop()
	}
	return layout.Dimensions{Size: image.Pt(w, h)}
}

func bucketLabel(sec int) string {
	if sec < 60 {
		return fmt.Sprintf("%ds", sec)
	}
	return fmt.Sprintf("%dm", sec/60)
}

func smallBtn(th *material.Theme, b *widget.Clickable, label string) layout.FlexChild {
	return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Left: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return ghostBtn(gtx, th, b, label)
		})
	})
}

func heatLegend(gtx layout.Context, th *material.Theme) layout.Dimensions {
	item := func(c color.NRGBA, txt string) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: unit.Dp(14)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						sz := gtx.Dp(unit.Dp(10))
						paint.FillShape(gtx.Ops, c, clip.Rect(image.Rect(0, 0, sz, sz)).Op())
						return layout.Dimensions{Size: image.Pt(sz, sz)}
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: unit.Dp(5)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							l := material.Label(th, unit.Sp(11), txt)
							l.Color = colTextSec
							return l.Layout(gtx)
						})
					}),
				)
			})
		})
	}
	return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
		item(colGood, "0%"),
		item(colWatch, "<5% of window"),
		item(colBad, "≥5% of window"),
		item(colCardAlt, "no data"),
	)
}
