package ui

// Internet sub-view: device→internet down/up, idle vs loaded latency, and an A–F
// bufferbloat grade. See docs/design-guide.md (metric cards, grade badge).

import (
	"fmt"
	"image"
	"image/color"
	"time"

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

type internetView struct {
	on   bool
	have bool
	res  appcore.InternetResult
	prog appcore.InternetProgress
	at   time.Time
	host string
}

func (st *testsState) internetSnapshot() internetView {
	st.internetMu.Lock()
	defer st.internetMu.Unlock()
	return internetView{st.internetOn, st.internetHave, st.internetRes, st.internetProg, st.internetAt, st.internetHost}
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

// layoutInternet renders the Internet sub-view. The "from" row is a node picker:
// the test can run on any online device (remote nodes report only the final
// result over the control plane).
func layoutInternet(gtx layout.Context, th *material.Theme, st *testsState, snap appcore.Snapshot) layout.Dimensions {
	v := st.internetSnapshot()
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
					// Dropdown trigger: shows the pinned server, or what the last/
					// current run resolved "auto" to.
					ep := "Auto · nearest server"
					switch {
					case st.epSel != "":
						ep = st.epSel
					case v.on && v.prog.Endpoint != "":
						ep = v.prog.Endpoint
					case v.res.Endpoint != "":
						ep = v.res.Endpoint
					}
					if st.epBtn.Clicked(gtx) {
						st.epOpen = !st.epOpen
					}
					car := "▾"
					if st.epOpen {
						car = "▴"
					}
					return hoverCursor(gtx, func(gtx layout.Context) layout.Dimensions {
						return material.Clickable(gtx, &st.epBtn, func(gtx layout.Context) layout.Dimensions {
							return widget.Border{Color: colOutline, Width: unit.Dp(1), CornerRadius: unit.Dp(7)}.Layout(gtx,
								func(gtx layout.Context) layout.Dimensions {
									return layout.Inset{Top: unit.Dp(5), Bottom: unit.Dp(5), Left: unit.Dp(10), Right: unit.Dp(10)}.Layout(gtx,
										func(gtx layout.Context) layout.Dimensions {
											return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
												layout.Rigid(func(gtx layout.Context) layout.Dimensions {
													l := material.Body2(th, ep)
													l.Color = colTextPri
													return l.Layout(gtx)
												}),
												layout.Rigid(gapX(6)),
												layout.Rigid(func(gtx layout.Context) layout.Dimensions {
													l := material.Caption(th, car)
													l.Color = colTextMut
													return l.Layout(gtx)
												}),
											)
										})
								})
						})
					})
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: gtx.Constraints.Min} }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if v.on {
						return busyBtn(gtx, th, &st.internetRun, "Running…")
					}
					label := "Run test"
					if v.have {
						label = "Run again"
					}
					return primaryBtn(gtx, th, &st.internetRun, label)
				}),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if !st.epOpen {
				return layout.Dimensions{}
			}
			return endpointOptions(gtx, th, st)
		}),
		layout.Rigid(gap(10)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return nodePicker(gtx, th, st, snap)
		}),
		layout.Rigid(gap(16)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			switch {
			case v.on:
				// A run in progress always wins — showing a previous run's error
				// here would read as "the retry already failed".
				return internetLive(gtx, th, v)
			case v.res.Err != "":
				lbl := material.Body2(th, "error: "+v.res.Err)
				lbl.Color = colBad
				return lbl.Layout(gtx)
			case !v.have:
				lbl := material.Caption(th, "no result yet — run a test")
				lbl.Color = colTextMut
				return lbl.Layout(gtx)
			default:
				return internetResults(gtx, th, v, snap.SelfPeer.Host)
			}
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return historyList(gtx, th, st.netHist)
		}),
	)
}

// endpointOptions is the expanded endpoint dropdown: Auto plus every pre-loaded
// LibreSpeed server. Selecting an entry pins it for subsequent runs.
func endpointOptions(gtx layout.Context, th *material.Theme, st *testsState) layout.Dimensions {
	options := append([]string{"Auto · nearest server"}, appcore.SpeedServerNames()...)
	rows := make([]layout.FlexChild, 0, len(options))
	for i, name := range options {
		i, name := i, name
		sel := (i == 0 && st.epSel == "") || st.epSel == name
		c := clickFor(&st.epClicks, name)
		if c.Clicked(gtx) {
			if i == 0 {
				st.epSel = ""
			} else {
				st.epSel = name
			}
			st.epOpen = false
		}
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return hoverCursor(gtx, func(gtx layout.Context) layout.Dimensions {
				return material.Clickable(gtx, c, func(gtx layout.Context) layout.Dimensions {
					bg := color.NRGBA{}
					if sel {
						bg = colCard
					}
					return roundedBG(gtx, bg, unit.Dp(6), unit.Dp(0), func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						return layout.Inset{Top: unit.Dp(7), Bottom: unit.Dp(7), Left: unit.Dp(10), Right: unit.Dp(10)}.Layout(gtx,
							func(gtx layout.Context) layout.Dimensions {
								mark := "   "
								if sel {
									mark = "✓ "
								}
								l := material.Body2(th, mark+name)
								l.Color = colTextPri
								if !sel {
									l.Color = colTextSec
								}
								return l.Layout(gtx)
							})
					})
				})
			})
		}))
	}
	return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return widget.Border{Color: colOutline, Width: unit.Dp(1), CornerRadius: unit.Dp(8)}.Layout(gtx,
			func(gtx layout.Context) layout.Dimensions {
				return roundedBG(gtx, colCardAlt, unit.Dp(8), unit.Dp(4), func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
				})
			})
	})
}

// nodePicker lets the operator choose which device runs the internet test. The
// chips reuse the segmented-control look so the selected node is unambiguous
// (filled active segment), matching the endpoint dropdown's selected style.
func nodePicker(gtx layout.Context, th *material.Theme, st *testsState, snap appcore.Snapshot) layout.Dimensions {
	nodes := append([]appcore.PeerInfo{snap.SelfPeer}, snap.Peers...)
	segs := make([]segSpec, 0, len(nodes))
	for i, n := range nodes {
		selected := st.netNodeID == n.ID || (st.netNodeID == "" && i == 0)
		c := clickFor(&st.nodeClicks, n.ID)
		if c.Clicked(gtx) {
			st.netNodeID = n.ID
		}
		label := n.Host
		if i == 0 {
			label += " · this device"
		}
		segs = append(segs, segSpec{click: c, label: label, active: selected})
	}
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.Caption(th, "from")
			lbl.Color = colTextSec
			return lbl.Layout(gtx)
		}),
		layout.Rigid(gapX(8)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return segControl(gtx, th, segs...)
		}),
	)
}

// gradeSubLine renders the grade context without claiming unmeasured zeros.
func gradeSubLine(rpm int, jitterMs, lossPct float64) string {
	s := fmt.Sprintf("RPM %d", rpm)
	if jitterMs > 0 {
		s += fmt.Sprintf(" · jitter %.0f ms", jitterMs)
	}
	if lossPct > 0 {
		s += fmt.Sprintf(" · loss %.1f%%", lossPct)
	}
	return s
}

// internetProvenance names when + where a result was measured:
// "measured Jan 2 15:04 · on ryzen · Los Angeles (Clouvider)". host is the node
// the test ran on (remote host when set, else this device); server is the
// resolved endpoint.
func internetProvenance(at time.Time, host, server string) string {
	if at.IsZero() {
		return ""
	}
	s := "measured " + at.Format("Jan 2 15:04")
	if host != "" {
		s += " · on " + host
	}
	if server != "" {
		s += " · " + server
	}
	return s
}

// internetResults lays out the throughput cards, one merged latency strip (idle →
// loaded → +Δ + grade + scale legend), and the measurement provenance. selfHost
// names this device for local runs.
func internetResults(gtx layout.Context, th *material.Theme, v internetView, selfHost string) layout.Dimensions {
	res := v.res
	added := res.LoadedMs - res.IdleMs
	if added < 0 {
		added = 0
	}
	loadedCol := colTextPri
	if added >= 60 {
		loadedCol = colWatch
	}
	host := v.host
	if host == "" {
		host = selfHost
	}
	provenance := internetProvenance(v.at, host, res.Endpoint)
	stat := func(label, value string, col color.NRGBA) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: unit.Dp(22)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						l := material.Label(th, unit.Sp(11), label)
						l.Color = colTextMut
						return l.Layout(gtx)
					}),
					layout.Rigid(gap(2)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						l := material.Label(th, unit.Sp(20), value)
						l.Color = col
						l.Font.Weight = 500
						return l.Layout(gtx)
					}),
				)
			})
		})
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				metricCardChild(th, "↓ Download", fmt.Sprintf("%.0f", res.DownMbit), "Mb/s", colAccent),
				layout.Rigid(gapX(10)),
				metricCardChild(th, "↑ Upload", fmt.Sprintf("%.0f", res.UpMbit), "Mb/s", upGreen),
			)
		}),
		layout.Rigid(gap(14)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				stat("idle", fmt.Sprintf("%.0f ms", res.IdleMs), colTextPri),
				stat("loaded", fmt.Sprintf("%.0f ms", res.LoadedMs), loadedCol),
				stat("added", fmt.Sprintf("+%.0f ms", added), loadedCol),
			)
		}),
		layout.Rigid(gap(12)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return gradeBadge(gtx, th, res) }),
		layout.Rigid(gap(6)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.Caption(th, "A <30 · B <60 · C <100 · D <200 ms added")
			lbl.Color = colTextMut
			return lbl.Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if provenance == "" {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				lbl := material.Caption(th, provenance)
				lbl.Color = colTextMut
				return lbl.Layout(gtx)
			})
		}),
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
								l := material.Caption(th, gradeSubLine(res.RPM, res.JitterMs, res.LossPct))
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

// internetLive is the in-flight view: the same metric cards as the results, born
// as "–" placeholders that fill in as each phase completes — the active
// throughput card carries the live climbing rate. Remote runs report no
// intermediate progress, so they get a plain running notice instead.
func internetLive(gtx layout.Context, th *material.Theme, v internetView) layout.Dimensions {
	if v.host != "" {
		lbl := material.Caption(th, "running on "+v.host+" — a remote test reports only the final result")
		lbl.Color = colTextSec
		return lbl.Layout(gtx)
	}
	prog := v.prog
	dash := func(v float64) string {
		if v <= 0 {
			return "–"
		}
		return fmt.Sprintf("%.0f", v)
	}
	// Download card: live rate while measuring, locked value after, dash before.
	downVal, downCol := dash(prog.DownMbit), colAccent
	if prog.Phase == 2 && prog.LiveMbit > 0 {
		downVal = fmt.Sprintf("%.0f", prog.LiveMbit)
	}
	upVal, upCol := dash(prog.UpMbit), upGreen
	if prog.Phase == 3 && prog.LiveMbit > 0 {
		upVal = fmt.Sprintf("%.0f", prog.LiveMbit)
	}
	if downVal == "–" {
		downCol = colTextMut
	}
	if upVal == "–" {
		upCol = colTextMut
	}
	idleVal, idleCol := dash(prog.IdleMs), colTextPri
	if idleVal == "–" {
		idleCol = colTextMut
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				metricCardChild(th, "↓ Download", downVal, "Mb/s", downCol),
				layout.Rigid(gapX(10)),
				metricCardChild(th, "↑ Upload", upVal, "Mb/s", upCol),
				layout.Rigid(gapX(10)),
				metricCardChild(th, "Idle latency", idleVal, "ms", idleCol),
				layout.Rigid(gapX(10)),
				metricCardChild(th, "Loaded latency", "–", "ms", colTextMut),
			)
		}),
		layout.Rigid(gap(14)),
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
