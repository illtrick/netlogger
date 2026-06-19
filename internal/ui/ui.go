// Package ui renders the portable app's native window with Gio. For N1 it shows
// a minimal live-status panel read from appcore.Snapshot.
package ui

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gioui.org/app"
	"gioui.org/font/gofont"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"netlogger/internal/appcore"
)

// Run opens the window and renders until it is closed.
func Run(a *appcore.App) error {
	w := new(app.Window)
	w.Option(app.Title("NetLogger"), app.Size(unit.Dp(880), unit.Dp(720)))

	base := material.NewTheme()
	base.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))
	th := darkTheme(base)

	go func() {
		for {
			time.Sleep(time.Second)
			w.Invalidate()
		}
	}()

	var resetBtn, exportBtn, sleepBtn widget.Clickable
	var statusMsg string
	var mainList widget.List
	mainList.Axis = layout.Vertical
	var heatList widget.List
	heatList.Axis = layout.Horizontal
	heatBucket := 120
	const heatWindowSec = 24 * 3600
	var heatView appcore.MeshHeat
	var heatAt time.Time
	var heatInit bool
	var heatReanchor int64 // unix time to keep at the left edge across a zoom (0 = none)
	var heatHov heatHover
	var hZoomOut, hZoomIn, hNow widget.Clickable
	var nav navTab = navDashboard
	var navDash, navTst, navEvt widget.Clickable
	var tst testsState

	var ops op.Ops
	for {
		switch e := w.Event().(type) {
		case app.DestroyEvent:
			return e.Err
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			paint.Fill(gtx.Ops, colBg)
			snap := a.Snapshot()

			if resetBtn.Clicked(gtx) {
				go a.ResetAll()
				statusMsg = "resetting mesh… (awaiting peer acks)"
			}
			if exportBtn.Clicked(gtx) {
				if exe, err := os.Executable(); err == nil {
					if p, werr := appcore.WriteExport(filepath.Dir(exe), a.Export(time.Now().Unix())); werr == nil {
						statusMsg = "exported " + filepath.Base(p)
					} else {
						statusMsg = "export failed: " + werr.Error()
					}
				}
			}
			if sleepBtn.Clicked(gtx) {
				a.SetPreventSleep(!snap.PreventSleep)
			}
			if hZoomIn.Clicked(gtx) && heatBucket > 15 {
				heatReanchor = heatView.FromUnix + int64(heatList.Position.First*heatView.BucketSec)
				heatBucket /= 2
				heatAt = time.Time{}
			}
			if hZoomOut.Clicked(gtx) && heatBucket < 1800 {
				heatReanchor = heatView.FromUnix + int64(heatList.Position.First*heatView.BucketSec)
				heatBucket *= 2
				heatAt = time.Time{}
			}
			if heatAt.IsZero() || time.Since(heatAt) > 2*time.Second {
				heatView = a.LossHeatByMachine(heatWindowSec, heatBucket)
				heatAt = time.Now()
			}
			if heatReanchor != 0 && heatView.Buckets > 0 { // keep the same left-edge time after a zoom
				nf := int((heatReanchor - heatView.FromUnix) / int64(heatBucket))
				if nf < 0 {
					nf = 0
				}
				if nf > heatView.Buckets {
					nf = heatView.Buckets
				}
				heatList.Position.First = nf
				heatList.Position.Offset = 0
				heatReanchor = 0
			}
			if !heatInit && heatView.Buckets > 0 {
				heatList.Position.First = heatView.Buckets // first open → live edge
				heatInit = true
			}
			if hNow.Clicked(gtx) && heatView.Buckets > 0 { // just scroll to now, no zoom change
				heatList.Position.First = heatView.Buckets
				heatList.Position.Offset = 0
			}
			if navDash.Clicked(gtx) {
				nav = nextTab(nav, navDashboard)
			}
			if navTst.Clicked(gtx) {
				nav = nextTab(nav, navTests)
			}
			if navEvt.Clicked(gtx) {
				nav = nextTab(nav, navEvents)
			}
			if tst.runBtn.Clicked(gtx) {
				tst.mu.Lock()
				alreadyRunning := tst.running
				if !alreadyRunning {
					tst.running = true
					tst.status = "running matrix…"
				}
				tst.mu.Unlock()
				if !alreadyRunning {
					self, peers := snap.SelfPeer, snap.Peers
					go func() {
						done := a.BeginSpeedTestNote()
						m := a.SpeedSweep(self, peers, appcore.SpeedReq{Direction: "both", Streams: 4, DurationS: 10, OmitS: 2})
						done()
						tst.mu.Lock()
						tst.matrix = m
						tst.haveMatrix = true
						tst.running = false
						tst.status = "done"
						tst.mu.Unlock()
					}()
				}
			}
			if tst.speedSeg.Clicked(gtx) {
				tst.sub = 0
			}
			if tst.stressSeg.Clicked(gtx) {
				tst.sub = 1
			}
			if tst.internetSeg.Clicked(gtx) {
				tst.sub = 2
			}
			if tst.startStress.Clicked(gtx) {
				tst.stressMu.Lock()
				already := tst.stressOn
				if !already {
					tst.stressOn = true
				}
				tst.stressMu.Unlock()
				if !already {
					self, peers := snap.SelfPeer, snap.Peers
					a.StartStress(self, peers, appcore.StressParams{PerLinkCapMbit: 200, Proto: "tcp", DurationS: 120})
					go func() {
						for {
							tst.stressMu.Lock()
							on := tst.stressOn
							tst.stressMu.Unlock()
							if !on {
								return
							}
							ns := a.PollStress(self, peers)
							anyRunning := false
							for _, s := range ns {
								if s.Running {
									anyRunning = true
								}
							}
							tst.stressMu.Lock()
							tst.stressNodes = ns
							if !anyRunning {
								tst.stressOn = false
							}
							tst.stressMu.Unlock()
							if !anyRunning {
								return
							}
							time.Sleep(time.Second)
						}
					}()
				}
			}
			if tst.stopStress.Clicked(gtx) {
				self, peers := snap.SelfPeer, snap.Peers
				a.StopStress(self, peers, "")
				tst.stressMu.Lock()
				tst.stressOn = false
				tst.stressMu.Unlock()
			}

			cardSection := func(w layout.Widget) layout.Widget {
				return func(gtx layout.Context) layout.Dimensions {
					return card(gtx, colCard, colBorder, w)
				}
			}
			dashItems := []layout.Widget{
				func(gtx layout.Context) layout.Dimensions {
					return layoutHeader(gtx, th, snap, &resetBtn, &exportBtn, &sleepBtn, statusMsg)
				},
				gap(20),
				func(gtx layout.Context) layout.Dimensions { return layoutKPIs(gtx, th, snap) },
				gap(24),
				cardSection(func(gtx layout.Context) layout.Dimensions {
					return layoutHeatmap(gtx, th, heatView, &heatList, &heatHov, &hZoomOut, &hZoomIn, &hNow)
				}),
				gap(12),
				cardSection(func(gtx layout.Context) layout.Dimensions { return layoutInfra(gtx, th, snap) }),
				gap(12),
				cardSection(func(gtx layout.Context) layout.Dimensions { return layoutPeers(gtx, th, snap) }),
				gap(12),
				cardSection(func(gtx layout.Context) layout.Dimensions { return layoutAdapters(gtx, th, snap) }),
				gap(16),
				func(gtx layout.Context) layout.Dimensions { return layoutFooter(gtx, th, snap) },
			}

			var items []layout.Widget
			switch nav {
			case navTests:
				items = []layout.Widget{
					gap(16),
					cardSection(func(gtx layout.Context) layout.Dimensions { return layoutTests(gtx, th, &tst, snap) }),
				}
			case navEvents:
				items = []layout.Widget{
					gap(16),
					cardSection(func(gtx layout.Context) layout.Dimensions { return layoutEvents(gtx, th, snap) }),
				}
			default:
				items = append([]layout.Widget{gap(4)}, dashItems...)
			}
			// Fixed title bar on top; the scrolling content list fills the rest. The
			// list spans full width so its scrollbar hugs the window's right edge;
			// horizontal margin is applied per item instead.
			layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return titleBar(gtx, th, snap, nav, &navDash, &navTst, &navEvt)
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: unit.Dp(16)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return material.List(th, &mainList).Layout(gtx, len(items), func(gtx layout.Context, i int) layout.Dimensions {
							return layout.Inset{Left: unit.Dp(20), Right: unit.Dp(18)}.Layout(gtx, items[i])
						})
					})
				}),
			)

			e.Frame(gtx.Ops)
		}
	}
}

// layoutHeader renders the status hero: a large colored status line, a muted
// "up <dur> · build <id>" subline, the action buttons, and (when set) the
// build-skew banner + transient status / last-reset messages.
func layoutHeader(gtx layout.Context, th *material.Theme, s appcore.Snapshot, resetBtn, exportBtn, sleepBtn *widget.Clickable, statusMsg string) layout.Dimensions {
	statusText, statusColor := overallStatus(s)
	sub := fmt.Sprintf("up %s · build %s", fmtDuration(s.SessionUptimeSec), versionOr(s.Build))
	sleepLabel := "Sleep: prevented"
	if !s.PreventSleep {
		sleepLabel = "Sleep: allowed"
	}
	hdrBtn := func(render func(gtx layout.Context) layout.Dimensions) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, render)
		})
	}
	caption := func(txt string, col color.NRGBA) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if txt == "" {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				lbl := material.Caption(th, txt)
				lbl.Color = col
				return lbl.Layout(gtx)
			})
		})
	}

	skew := ""
	if s.BuildWarning != "" {
		skew = "⚠ " + s.BuildWarning
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							lbl := material.Label(th, unit.Sp(20), "● "+statusText)
							lbl.Color = statusColor
							lbl.Font.Weight = 500
							return lbl.Layout(gtx)
						}),
						layout.Rigid(gap(2)),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							lbl := material.Label(th, unit.Sp(13), sub)
							lbl.Color = colTextSec
							return lbl.Layout(gtx)
						}),
					)
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Dimensions{Size: gtx.Constraints.Min}
				}),
				hdrBtn(func(gtx layout.Context) layout.Dimensions { return ghostBtn(gtx, th, sleepBtn, sleepLabel) }),
				hdrBtn(func(gtx layout.Context) layout.Dimensions { return dangerGhostBtn(gtx, th, resetBtn, "Reset all") }),
				hdrBtn(func(gtx layout.Context) layout.Dimensions { return ghostBtn(gtx, th, exportBtn, "Export") }),
			)
		}),
		caption(skew, colBad),
		caption(statusMsg, colTextSec),
		caption(s.LastReset, colTextSec),
	)
}

// layoutInfra renders gateway and internet rows with sparklines.
func layoutInfra(gtx layout.Context, th *material.Theme, s appcore.Snapshot) layout.Dimensions {
	gwLabel := "Gateway: (not detected)"
	if s.GatewayIP != "" {
		gwLabel = fmt.Sprintf("Gateway: %s   RTT %.2f ms   loss %.1f%%", s.GatewayIP, s.GatewayRTTms, s.GatewayLossPct)
	}
	netLabel := "Internet: (not detected)"
	if s.InternetIP != "" {
		netLabel = fmt.Sprintf("Internet: %s   RTT %.2f ms   loss %.1f%%", s.InternetIP, s.InternetRTTms, s.InternetLossPct)
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return infraRow(gtx, th, gwLabel, s.GatewayHist)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return infraRow(gtx, th, netLabel, s.InternetHist)
			})
		}),
	)
}

func infraRow(gtx layout.Context, th *material.Theme, label string, hist []float64) layout.Dimensions {
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return material.Body1(th, label).Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return sparkline(gtx, hist, blue, 160, 24)
			})
		}),
	)
}

// layoutPeers renders a block per peer: label line + two sparklines.
func layoutPeers(gtx layout.Context, th *material.Theme, s appcore.Snapshot) layout.Dimensions {
	if len(s.Peers) == 0 {
		return material.Body1(th, "Peers: (none yet — launch NetLogger on another machine on this LAN)").Layout(gtx)
	}
	children := make([]layout.FlexChild, 0, len(s.Peers)*2)
	for _, p := range s.Peers {
		p := p
		children = append(children,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return peerBlock(gtx, th, p)
				})
			}),
		)
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

func peerBlock(gtx layout.Context, th *material.Theme, p appcore.PeerInfo) layout.Dimensions {
	label := fmt.Sprintf("%s   up %s   RTT %.2f ms  jitter %.2f ms  loss %.1f%%  drops %d",
		peerName(p), fmtDuration(p.UpForSec), p.RTTms, p.JitterMs, p.UDPLossPct, p.DropEpisodes)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return material.Body1(th, label).Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(3)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return sparkline(gtx, p.RTTHist, blue, 180, 24)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return sparkline(gtx, p.LossHist, orange, 180, 24)
						})
					}),
				)
			})
		}),
	)
}

// layoutAdapters renders one line per local NIC: link speed/status, every
// power-saving property the adapter exposes, and the discard/error deltas since
// the previous poll. A row turns vermillion when any discard/error ticked up
// (direct NIC evidence during a drop) and amber when any power-saving property
// (EEE, Green Ethernet, Gigabit Lite, …) is enabled — the suspect lead.
func layoutAdapters(gtx layout.Context, th *material.Theme, s appcore.Snapshot) layout.Dimensions {
	if len(s.NICs) == 0 {
		return material.Body1(th, "Adapters: (none reported)").Layout(gtx)
	}
	children := make([]layout.FlexChild, 0, len(s.NICs)+1)
	children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return material.Body1(th, "Adapters:").Layout(gtx)
	}))
	for _, n := range s.NICs {
		n := n
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(3), Left: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				lbl := material.Body1(th, adapterLine(n))
				switch {
				case adapterHasFaults(n):
					lbl.Color = orange
				case powerSavingOn(n.Power):
					lbl.Color = amber
				}
				return lbl.Layout(gtx)
			})
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

// adapterLine is the pure text rendering of one NIC row.
func adapterLine(n appcore.NICInfo) string {
	speed := n.LinkSpeed
	if !strings.EqualFold(n.Status, "Up") {
		speed = "—" // a down/disconnected link has no meaningful rate
	}
	return fmt.Sprintf("%s  %s  %s  power[%s]  discards rx+%d tx+%d  errors rx+%d tx+%d",
		n.Name, speed, n.Status, powerText(n.Power),
		n.RecentRxDiscards, n.RecentTxDiscards, n.RecentRxErrors, n.RecentTxErrors)
}

// adapterHasFaults reports whether any discard/error counter ticked up this poll.
func adapterHasFaults(n appcore.NICInfo) bool {
	return n.RecentRxDiscards+n.RecentTxDiscards+n.RecentRxErrors+n.RecentTxErrors > 0
}

// powerText renders the joined power-saving properties for display ("none" when
// the adapter reports no such properties, e.g. Wi-Fi).
func powerText(v string) string {
	if v == "" {
		return "none"
	}
	return v
}

// powerSavingOn reports whether any of the joined "Name=Value; …" power-saving
// properties is in an enabled state ("Enabled"/"On", case-insensitive).
func powerSavingOn(v string) bool {
	for _, prop := range strings.Split(v, ";") {
		_, val, ok := strings.Cut(prop, "=")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		if strings.EqualFold(val, "Enabled") || strings.EqualFold(val, "On") {
			return true
		}
	}
	return false
}

// layoutEvents renders the most recent connectivity-timeline entries (newest
// first) so faults — link drops, speed renegotiations, discard spikes — are
// visible at a glance without exporting. Offline events are vermillion.
func layoutEvents(gtx layout.Context, th *material.Theme, s appcore.Snapshot) layout.Dimensions {
	if len(s.Events) == 0 {
		return sectionTitle(gtx, th, "Recent events (mesh-wide): (none yet)")
	}
	now := time.Now().UnixMicro()
	const maxRows = 10
	children := make([]layout.FlexChild, 0, maxRows+1)
	children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return sectionTitle(gtx, th, "Recent events (mesh-wide)")
	}))
	shown := 0
	for i := len(s.Events) - 1; i >= 0 && shown < maxRows; i-- {
		e := s.Events[i]
		shown++
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return eventRow(gtx, th, e, now)
			})
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

// eventRow renders one merged event: a severity dot, the relative time (mono),
// a host chip, and the detail.
func eventRow(gtx layout.Context, th *material.Theme, e appcore.MergedEvent, now int64) layout.Dimensions {
	dotCol := colGood
	if !e.Online {
		dotCol = colBad
	}
	host := e.Host
	if host == "" {
		host = "?"
	}
	return roundedBG(gtx, colCardAlt, unit.Dp(6), unit.Dp(8), func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(dotWidget(dotCol, 8)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					l := material.Label(th, unit.Sp(12), eventAge(e, now))
					l.Color = colTextMut
					l.Font.Typeface = "Go Mono"
					return l.Layout(gtx)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return chipLabel(gtx, th, host, colTextSec, colCard)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					l := material.Label(th, unit.Sp(13), e.Detail)
					l.Color = colTextPri
					return l.Layout(gtx)
				})
			}),
		)
	})
}

// eventAge renders an event's age as "<dur> ago" relative to nowMicro.
func eventAge(e appcore.MergedEvent, nowMicro int64) string {
	age := (nowMicro - e.UnixMicro) / 1_000_000
	if age < 0 {
		age = 0
	}
	return fmtDuration(age) + " ago"
}

// eventLine is the plain-text form kept for tests/back-compat.
func eventLine(e appcore.MergedEvent, nowMicro int64) string {
	host := e.Host
	if host == "" {
		host = "?"
	}
	return fmt.Sprintf("%s  %s: %s", eventAge(e, nowMicro), host, e.Detail)
}

// sectionTitle renders a muted section header label.
func sectionTitle(gtx layout.Context, th *material.Theme, txt string) layout.Dimensions {
	l := material.Label(th, unit.Sp(13), txt)
	l.Color = colTextSec
	return l.Layout(gtx)
}

// layoutFooter renders a compact data-dir / iperf status line.
func layoutFooter(gtx layout.Context, th *material.Theme, s appcore.Snapshot) layout.Dimensions {
	line := fmt.Sprintf("data: %s   build: %s   iperf3: %s (%s)   self-probe: %d samples, last RTT %.2f ms, loss %.1f%%",
		s.DataDir, versionOr(s.Build), versionOr(s.Iperf3Version), upDown(s.Iperf3ServerUp), s.Samples, s.LastRTTms, s.LossPct)
	cap := material.Caption(th, line)
	cap.Color = color.NRGBA{R: 0x88, G: 0x88, B: 0x88, A: 0xff}
	return cap.Layout(gtx)
}

// ── Layout kept for test compatibility ──────────────────────────────────────

func layoutStatus(gtx layout.Context, th *material.Theme, s appcore.Snapshot) layout.Dimensions {
	rows := statusLines(s)
	return layout.UniformInset(unit.Dp(20)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, flexChildren(th, rows)...)
	})
}

// statusLines builds the text rows shown in the window. It is a pure function of
// the snapshot, kept separate from Gio layout so the display logic is testable.
func statusLines(s appcore.Snapshot) []string {
	rows := []string{
		"NetLogger — portable diagnostic agent",
		"",
		fmt.Sprintf("Data dir:      %s", s.DataDir),
		fmt.Sprintf("Database:      %s", s.DBPath),
		fmt.Sprintf("iperf3:        %s (server %s)", versionOr(s.Iperf3Version), upDown(s.Iperf3ServerUp)),
		fmt.Sprintf("Self-probe:    %d samples, last RTT %.2f ms, loss %.1f%%", s.Samples, s.LastRTTms, s.LossPct),
		gatewayRow(s),
		"",
		fmt.Sprintf("Discovered peers (%d):", len(s.Peers)),
	}
	if len(s.Peers) == 0 {
		rows = append(rows, "   (none yet — launch NetLogger on another machine on this LAN)")
	}
	for _, p := range s.Peers {
		rows = append(rows, fmt.Sprintf("   - %-12s %-20s  RTT %.2f ms  jitter %.2f ms  loss %.1f%%  drops %d",
			peerName(p), p.Addr, p.RTTms, p.JitterMs, p.UDPLossPct, p.DropEpisodes))
	}
	return rows
}

func gatewayRow(s appcore.Snapshot) string {
	if s.GatewayIP == "" {
		return "Gateway:       (not detected)"
	}
	return fmt.Sprintf("Gateway:       %s   RTT %.2f ms   loss %.1f%%", s.GatewayIP, s.GatewayRTTms, s.GatewayLossPct)
}

func peerName(p appcore.PeerInfo) string {
	if p.Host != "" {
		return p.Host
	}
	return p.ID
}

func flexChildren(th *material.Theme, rows []string) []layout.FlexChild {
	out := make([]layout.FlexChild, 0, len(rows))
	for _, r := range rows {
		r := r
		out = append(out, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(3), Bottom: unit.Dp(3)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return material.Body1(th, r).Layout(gtx)
			})
		}))
	}
	return out
}

func versionOr(v string) string {
	if v == "" {
		return "(not available)"
	}
	return v
}

func upDown(up bool) string {
	if up {
		return "running"
	}
	return "stopped"
}

// ── Pure helpers ────────────────────────────────────────────────────────────

// fmtDuration formats an uptime in seconds as a compact human string.
func fmtDuration(sec int64) string {
	if sec < 60 {
		return fmt.Sprintf("%ds", sec)
	}
	if sec < 3600 {
		return fmt.Sprintf("%dm %ds", sec/60, sec%60)
	}
	return fmt.Sprintf("%dh %dm", sec/3600, (sec%3600)/60)
}

// overallStatus returns the worst-case health label and its associated color.
func overallStatus(s appcore.Snapshot) (string, color.NRGBA) {
	worst := 0.0
	for _, p := range s.Peers {
		if p.UDPLossPct > worst {
			worst = p.UDPLossPct
		}
	}
	if s.InternetLossPct > worst {
		worst = s.InternetLossPct
	}
	if s.GatewayLossPct > worst {
		worst = s.GatewayLossPct
	}
	switch {
	case worst >= 1.0:
		return "DEGRADED", color.NRGBA{R: 0xD5, G: 0x5E, B: 0x00, A: 0xff}
	case worst >= 0.1:
		return "WATCH", color.NRGBA{R: 0xE6, G: 0x9F, B: 0x00, A: 0xff}
	default:
		return "ALL HEALTHY", color.NRGBA{R: 0x00, G: 0x9E, B: 0x73, A: 0xff}
	}
}
