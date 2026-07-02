package ui

// Internet sub-view: device→internet down/up, idle vs loaded latency, and an A–F
// bufferbloat grade. See docs/design-guide.md (metric cards, grade badge).

import (
	"fmt"
	"image"
	"image/color"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"netlogger/internal/appcore"
)

// tint returns c with its alpha replaced (for low-alpha fills behind badges).
func tint(c color.NRGBA, a uint8) color.NRGBA { c.A = a; return c }

func (st *testsState) internetSnapshot() (bool, bool, appcore.InternetResult, appcore.InternetProgress) {
	st.internetMu.Lock()
	defer st.internetMu.Unlock()
	return st.internetOn, st.internetHave, st.internetRes, st.internetProg
}

// gradeColor maps an A–F bufferbloat grade to the severity palette.
func gradeColor(grade string) color.NRGBA {
	switch grade {
	case "A", "B":
		return colGood
	case "C":
		return colWatch
	default:
		return colBad
	}
}

var upGreen = color.NRGBA{R: 0x7e, G: 0xe0, B: 0xa0, A: 0xff}

// layoutInternet renders the Internet sub-view.
func layoutInternet(gtx layout.Context, th *material.Theme, st *testsState, snap appcore.Snapshot) layout.Dimensions {
	on, have, res, prog := st.internetSnapshot()
	host := snap.SelfPeer.Host
	if host == "" {
		host = "this device"
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					lbl := material.Caption(th, "Endpoint")
					lbl.Color = colTextSec
					return lbl.Layout(gtx)
				}),
				layout.Rigid(gapX(8)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					ep := "LibreSpeed · auto"
					switch {
					case on && prog.Endpoint != "":
						ep = prog.Endpoint
					case res.Endpoint != "":
						ep = res.Endpoint
					}
					return chipLabel(gtx, th, ep, colTextPri, colCardAlt)
				}),
				layout.Rigid(gapX(10)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					lbl := material.Caption(th, "from "+host)
					lbl.Color = colTextMut
					return lbl.Layout(gtx)
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: gtx.Constraints.Min} }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					label := "Run test"
					if have {
						label = "Run again"
					}
					if on {
						label = "Running…"
					}
					return primaryBtn(gtx, th, &st.internetRun, label)
				}),
			)
		}),
		layout.Rigid(gap(16)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			switch {
			case on:
				// A run in progress always wins — showing a previous run's error
				// here would read as "the retry already failed".
				return internetLive(gtx, th, prog)
			case res.Err != "":
				lbl := material.Body2(th, "error: "+res.Err)
				lbl.Color = colBad
				return lbl.Layout(gtx)
			case !have:
				lbl := material.Caption(th, "no result yet — run a test")
				lbl.Color = colTextMut
				return lbl.Layout(gtx)
			default:
				return internetResults(gtx, th, res)
			}
		}),
	)
}

// internetResults lays out the metric cards, grade badge, and phase strip.
func internetResults(gtx layout.Context, th *material.Theme, res appcore.InternetResult) layout.Dimensions {
	loadedCol := colTextPri
	if res.LoadedMs-res.IdleMs >= 60 {
		loadedCol = colWatch
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				metricCardChild(th, "↓ Download", fmt.Sprintf("%.0f", res.DownMbit), "Mbit/s", colAccent),
				layout.Rigid(gapX(10)),
				metricCardChild(th, "↑ Upload", fmt.Sprintf("%.0f", res.UpMbit), "Mbit/s", upGreen),
				layout.Rigid(gapX(10)),
				metricCardChild(th, "Idle latency", fmt.Sprintf("%.0f", res.IdleMs), "ms", colTextPri),
				layout.Rigid(gapX(10)),
				metricCardChild(th, "Loaded latency", fmt.Sprintf("%.0f", res.LoadedMs), "ms", loadedCol),
			)
		}),
		layout.Rigid(gap(12)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return gradeBadge(gtx, th, res) }),
		layout.Rigid(gap(14)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return phaseStrip(gtx, th, phaseAllDone) }),
	)
}

// metricCardChild is a flexed metric tile: muted label, large value + unit.
func metricCardChild(th *material.Theme, label, value, unitTxt string, valCol color.NRGBA) layout.FlexChild {
	return layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
		return roundedBG(gtx, colCardAlt, unit.Dp(8), unit.Dp(14), func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					l := material.Caption(th, label)
					l.Color = colTextMut
					return l.Layout(gtx)
				}),
				layout.Rigid(gap(4)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							l := material.Label(th, unit.Sp(24), value)
							l.Color = valCol
							l.Font.Weight = 500
							return l.Layout(gtx)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Left: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								l := material.Caption(th, unitTxt)
								l.Color = colTextMut
								return l.Layout(gtx)
							})
						}),
					)
				}),
			)
		})
	})
}

// gradeBadge is the big letter grade + the numeric basis.
func gradeBadge(gtx layout.Context, th *material.Theme, res appcore.InternetResult) layout.Dimensions {
	col := gradeColor(res.Grade)
	return roundedBG(gtx, colCardAlt, unit.Dp(8), unit.Dp(14), func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Caption(th, "Bufferbloat grade")
				l.Color = colTextMut
				return l.Layout(gtx)
			}),
			layout.Rigid(gap(8)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						sz := gtx.Dp(unit.Dp(56))
						paint.FillShape(gtx.Ops, tint(col, 0x22), clip.UniformRRect(image.Rect(0, 0, sz, sz), gtx.Dp(unit.Dp(12))).Op(gtx.Ops))
						gtx.Constraints.Min = image.Pt(sz, sz)
						layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							l := material.Label(th, unit.Sp(32), res.Grade)
							l.Color = col
							l.Font.Weight = 500
							return l.Layout(gtx)
						})
						return layout.Dimensions{Size: image.Pt(sz, sz)}
					}),
					layout.Rigid(gapX(14)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						added := res.LoadedMs - res.IdleMs
						if added < 0 {
							added = 0
						}
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								l := material.Body2(th, fmt.Sprintf("+%.0f ms under load", added))
								l.Color = colTextPri
								return l.Layout(gtx)
							}),
							layout.Rigid(gap(2)),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								l := material.Caption(th, fmt.Sprintf("RPM %d · jitter %.0f ms · loss %.1f%%", res.RPM, res.JitterMs, res.LossPct))
								l.Color = colTextSec
								return l.Layout(gtx)
							}),
						)
					}),
				)
			}),
		)
	})
}

// internetLive is the in-flight view: the live phase strip and, during a
// throughput phase, the climbing rate.
func internetLive(gtx layout.Context, th *material.Theme, prog appcore.InternetProgress) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if prog.Phase != 2 && prog.Phase != 3 {
				lbl := material.Caption(th, [4]string{
					"finding the closest server…",
					"sampling idle latency…",
					"", "",
				}[prog.Phase])
				lbl.Color = colTextSec
				return lbl.Layout(gtx)
			}
			// Live throughput number, styled like the result metric cards.
			label, valCol := "↓ Download", colAccent
			if prog.Phase == 3 {
				label, valCol = "↑ Upload", upGreen
			}
			return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					l := material.Caption(th, label)
					l.Color = colTextMut
					return l.Layout(gtx)
				}),
				layout.Rigid(gapX(10)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					l := material.Label(th, unit.Sp(34), fmt.Sprintf("%.0f", prog.LiveMbit))
					l.Color = valCol
					l.Font.Weight = 500
					return l.Layout(gtx)
				}),
				layout.Rigid(gapX(6)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					l := material.Caption(th, "Mbit/s")
					l.Color = colTextMut
					return l.Layout(gtx)
				}),
			)
		}),
		layout.Rigid(gap(16)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return phaseStrip(gtx, th, prog.Phase) }),
	)
}

// phaseStrip shows the four measurement phases. activeIdx marks the phase in
// flight (accent); earlier phases are done (green ✓), later ones pending
// (muted). Pass phaseAllDone once a result has landed.
const phaseAllDone = 4

func phaseStrip(gtx layout.Context, th *material.Theme, activeIdx int) layout.Dimensions {
	names := []string{"Select server", "Idle latency", "Download", "Upload"}
	ch := make([]layout.FlexChild, 0, len(names)*2)
	for i, n := range names {
		if i > 0 {
			ch = append(ch, layout.Rigid(gapX(8)))
		}
		i, n := i, n
		ch = append(ch, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			border, txt, fg := tint(colGood, 0x40), "✓ "+n, colTextSec
			switch {
			case i == activeIdx:
				border, txt, fg = tint(colAccent, 0x70), "● "+n, colTextPri
			case i > activeIdx:
				border, txt, fg = colOutline, n, colTextMut
			}
			return widget.Border{Color: border, Width: unit.Dp(1), CornerRadius: unit.Dp(7)}.Layout(gtx,
				func(gtx layout.Context) layout.Dimensions {
					return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						l := material.Caption(th, txt)
						l.Color = fg
						return l.Layout(gtx)
					})
				})
		}))
	}
	return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, ch...)
}
