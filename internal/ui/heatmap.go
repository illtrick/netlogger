package ui

import (
	"fmt"
	"image"
	"image/color"
	"time"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"netlogger/internal/appcore"
)

const (
	heatRowH   = 22 // dp per link row
	heatCellW  = 9  // dp per time bucket
	heatLabelW = 96 // dp for the fixed left labels
)

// heatColor maps a bucket's loss% (fraction of that bucket's seconds that saw
// loss) to the severity palette; <0 means no samples → a dim empty cell.
func heatColor(loss float64) color.NRGBA {
	switch {
	case loss < 0:
		return colCardAlt
	case loss == 0:
		return colGood
	case loss < 5:
		return colWatch
	default:
		return colBad
	}
}

func heatCell(gtx layout.Context, loss float64) layout.Dimensions {
	cw, ch := gtx.Dp(unit.Dp(heatCellW)), gtx.Dp(unit.Dp(heatRowH))
	g := gtx.Dp(unit.Dp(1))
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

// layoutHeatmap renders the time-normalized "loss by link" grid: a fixed label
// column + a virtualized, horizontally-scrollable strip of buckets, every link on
// one shared time axis so concurrent problems line up vertically.
func layoutHeatmap(gtx layout.Context, th *material.Theme, v appcore.HeatView, list *widget.List, zoomOut, zoomIn, now *widget.Clickable) layout.Dimensions {
	firstT := time.Unix(v.FromUnix+int64(list.Position.First*v.BucketSec), 0)
	title := "Loss by link · collecting samples…"
	if v.Buckets > 0 {
		title = fmt.Sprintf("Loss by link · viewing ~%s · %s/cell", firstT.Format("15:04"), bucketLabel(v.BucketSec))
	}

	header := layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return sectionTitle(gtx, th, title) }),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: gtx.Constraints.Min} }),
		smallBtn(th, zoomOut, "−"),
		smallBtn(th, zoomIn, "+"),
		smallBtn(th, now, "Now"),
	)

	if v.Buckets == 0 || len(v.Rows) == 0 {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return header }),
		)
	}

	grid := layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Dp(unit.Dp(heatLabelW))
			gtx.Constraints.Max.X = gtx.Constraints.Min.X
			ch := make([]layout.FlexChild, 0, len(v.Rows))
			for i := range v.Rows {
				label := v.Rows[i].Label
				ch = append(ch, layout.Rigid(fixedH(heatRowH, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						l := material.Label(th, unit.Sp(12), label)
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
			return material.List(th, list).Layout(gtx, v.Buckets, func(gtx layout.Context, i int) layout.Dimensions {
				col := make([]layout.FlexChild, 0, len(v.Rows))
				for r := range v.Rows {
					loss := -1.0
					if i < len(v.Rows[r].Loss) {
						loss = v.Rows[r].Loss[i]
					}
					l := loss
					col = append(col, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return heatCell(gtx, l) }))
				}
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, col...)
			})
		}),
	)

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return header }),
		layout.Rigid(gap(10)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return grid }),
		layout.Rigid(gap(8)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return heatLegend(gtx, th) }),
	)
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
			bt := material.Button(th, b, label)
			bt.TextSize = unit.Sp(12)
			bt.Inset = layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(4), Left: unit.Dp(10), Right: unit.Dp(10)}
			return bt.Layout(gtx)
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
