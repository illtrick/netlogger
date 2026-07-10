// Package ui renders the portable app's native window with Gio. For N1 it shows
// a minimal live-status panel read from appcore.Snapshot.
package ui

import (
	"context"
	"fmt"
	"image/color"
	"path/filepath"
	"strings"
	"time"

	"gioui.org/app"
	"gioui.org/font/gofont"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"netlogger/internal/appcore"
	"netlogger/internal/datadir"
)

// Run opens the window and renders until it is closed.
func Run(a *appcore.App) error {
	w := new(app.Window)
	w.Option(app.Title("NetLogger"), app.Size(unit.Dp(880), unit.Dp(720)),
		app.MinSize(unit.Dp(760), unit.Dp(520)), // keep the layout from squishing into overlap
		app.Decorated(nativeDecorations))        // win+mac: the app bar IS the title bar (mac re-adds traffic lights)
	applyDarkTitleBar("NetLogger") // window icon, rounded corners, close-to-tray hook
	stopTray := startTray("NetLogger")
	defer stopTray()

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
	// One scroll list per tab so switching tabs never inherits a stale offset.
	var tabLists [3]widget.List
	for i := range tabLists {
		tabLists[i].Axis = layout.Vertical
	}
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
	var chrome chromeState

	var ops op.Ops
	for {
		switch e := w.Event().(type) {
		case app.DestroyEvent:
			return e.Err
		case app.ViewEvent:
			nativeViewChanged(e) // darwin: re-show traffic lights over the app bar
		case app.ConfigEvent:
			chrome.maximized = e.Config.Mode == app.Maximized
			nativeConfigChanged() // darwin: Configure re-hides the buttons; re-assert
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			paint.Fill(gtx.Ops, colBg)
			snap := a.Snapshot()

			if resetBtn.Clicked(gtx) {
				go a.ResetAll()
				statusMsg = "resetting mesh… (awaiting peer acks)"
			}
			if exportBtn.Clicked(gtx) {
				// Beside the exe on portable platforms; the data dir when the
				// exe is inside a .app bundle (never write into the bundle).
				dir := datadir.SidecarDir(a.DataDir())
				if p, werr := appcore.WriteExport(dir, a.Export(time.Now().Unix())); werr == nil {
					statusMsg = "exported " + filepath.Base(p)
				} else {
					statusMsg = "export failed: " + werr.Error()
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
			if customChrome {
				// Caption buttons exist only under the hand-drawn title bar;
				// native chrome (macOS) supplies its own. Gated on the same
				// constant that controls their layout in chrome.go so the
				// Windows-only semantics (close-to-tray, ActionMaximize) can
				// never re-arm on a decorated platform.
				if chrome.minBtn.Clicked(gtx) {
					w.Perform(system.ActionMinimize)
				}
				if chrome.maxBtn.Clicked(gtx) {
					if chrome.maximized {
						w.Perform(system.ActionUnmaximize)
					} else {
						w.Perform(system.ActionMaximize)
					}
				}
				if chrome.closeBtn.Clicked(gtx) {
					// Close-to-tray. Asynchronous on purpose: ShowWindow from the frame
					// handler would deadlock the window's message thread.
					go hideMainWindow("NetLogger")
				}
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
				var ctx context.Context
				if !alreadyRunning {
					tst.running = true
					tst.status = "running matrix…"
					ctx, tst.sweepCancel = context.WithCancel(context.Background())
				}
				tst.mu.Unlock()
				if !alreadyRunning {
					self, peers := snap.SelfPeer, snap.Peers
					req := tst.sweepReq() // read the controls on the frame thread
					tst.mu.Lock()
					tst.lastReq = req
					tst.mu.Unlock()
					go func() {
						done := a.BeginSpeedTestNote()
						m := a.SpeedSweep(ctx, self, peers, req,
							func(p appcore.SweepProgress) { // live matrix + per-second rates
								tst.mu.Lock()
								tst.sweep = p
								tst.mu.Unlock()
								w.Invalidate()
							})
						done()
						status := "completed " + time.Now().Format("15:04") + " · " + appcore.SweepConfigLine(req)
						if ctx.Err() != nil {
							status = "stopped — partial results"
						}
						tst.mu.Lock()
						tst.matrix = m
						tst.haveMatrix = true
						tst.running = false
						tst.status = status
						tst.sweep = appcore.SweepProgress{}
						tst.sweepCancel = nil
						tst.mu.Unlock()
						w.Invalidate()
					}()
				}
			}
			if tst.sweepStop.Clicked(gtx) {
				tst.mu.Lock()
				if tst.sweepCancel != nil {
					tst.sweepCancel() // kills local iperf3 + drops remote requests
				}
				tst.mu.Unlock()
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
			if tst.internetRun.Clicked(gtx) {
				tst.internetMu.Lock()
				running := tst.internetOn
				if !running {
					tst.internetOn = true
				}
				tst.internetMu.Unlock()
				if !running {
					// Run on the picked node (self by default). Remote nodes report
					// only the final result, so mark the host for the running view.
					node := snap.SelfPeer
					for _, p := range snap.Peers {
						if p.ID == tst.netNodeID {
							node = p
						}
					}
					remoteHost := ""
					if node.ID != snap.SelfPeer.ID {
						remoteHost = node.Host
					}
					endpoint := tst.epSel // pinned server name, or "" → auto
					if endpoint == "" {
						endpoint = "auto"
					}
					tst.internetMu.Lock()
					tst.internetProg = appcore.InternetProgress{}
					tst.internetHost = remoteHost
					tst.internetMu.Unlock()
					go func() {
						res := a.InternetTest(node, endpoint, func(p appcore.InternetProgress) {
							tst.internetMu.Lock()
							tst.internetProg = p
							tst.internetMu.Unlock()
							w.Invalidate()
						})
						tst.internetMu.Lock()
						tst.internetRes = res
						tst.internetHave = true
						tst.internetOn = false
						tst.internetAt = time.Now()
						tst.internetMu.Unlock()
						w.Invalidate()
					}()
				}
			}
			if tst.capDec.Clicked(gtx) && tst.cap() > stressCapMin {
				tst.capMbit = tst.cap() - stressCapStep
			}
			if tst.capInc.Clicked(gtx) && tst.cap() < stressCapMax {
				tst.capMbit = tst.cap() + stressCapStep
			}
			if tst.startStress.Clicked(gtx) {
				tst.stressMu.Lock()
				already := tst.stressOn
				var gen int
				if !already {
					tst.stressOn = true
					tst.stressGen++
					gen = tst.stressGen
					// Pin the node set for this run so Stop reaches every loaded
					// node even if discovery drops one mid-run.
					tst.stressSelf, tst.stressPeers = snap.SelfPeer, snap.Peers
					tst.stressMsg = ""
					tst.stressNodes = nil
				}
				tst.stressMu.Unlock()
				if !already {
					self, peers := snap.SelfPeer, snap.Peers
					capMbit := tst.cap() // read on the UI thread; goroutine gets the value
					// Fan-out + polling stay off the UI thread: StartStress POSTs to
					// every peer (10s timeout each) and would freeze the frame loop.
					const durS = 120
					go func() {
						_, started := a.StartStress(self, peers, appcore.StressParams{PerLinkCapMbit: capMbit, Proto: "tcp", DurationS: durS})
						if started == 0 {
							tst.stressMu.Lock()
							if tst.stressGen == gen {
								tst.stressOn = false
								tst.stressMsg = "start failed — no node accepted the run (previous run still winding down?)"
							}
							tst.stressMu.Unlock()
							w.Invalidate()
							return
						}
						// Idle baseline per peer at start; peak RTT tracked each tick.
						// These maps are goroutine-local (no other writer), so they
						// need no lock. worst added latency = max(peak − baseline).
						baseRTT := make(map[string]float64, len(peers))
						maxRTT := make(map[string]float64, len(peers))
						hostByID := make(map[string]string, len(peers))
						for _, p := range peers {
							baseRTT[p.ID] = p.RTTms
							hostByID[p.ID] = p.Host
						}
						recorded := false
						for {
							tst.stressMu.Lock()
							on := tst.stressOn && tst.stressGen == gen
							tst.stressMu.Unlock()
							if !on {
								return
							}
							ns := a.PollStress(self, peers)
							// Track each peer's peak RTT under load from a fresh snapshot.
							for _, p := range a.Snapshot().Peers {
								if p.RTTms > maxRTT[p.ID] {
									maxRTT[p.ID] = p.RTTms
								}
							}
							anyRunning := false
							for _, s := range ns {
								if s.Running {
									anyRunning = true
								}
							}
							tst.stressMu.Lock()
							if tst.stressGen != gen { // a newer run owns the state now
								tst.stressMu.Unlock()
								return
							}
							tst.stressNodes = ns
							if !anyRunning {
								tst.stressOn = false
							}
							tst.stressMu.Unlock()
							w.Invalidate()
							if !anyRunning {
								// Natural end: the orchestrator records one summary row.
								if !recorded {
									recorded = true
									worstHost, worstAdd := "", 0.0
									for id, mx := range maxRTT {
										add := mx - baseRTT[id]
										if add < 0 {
											add = 0
										}
										if add > worstAdd {
											worstAdd, worstHost = add, hostByID[id]
										}
									}
									links, aborts := 0, 0
									for _, s := range ns {
										for _, l := range s.Links {
											links++
											if l.Aborted {
												aborts++
											}
										}
									}
									a.RecordStressRun(durS, links, capMbit, "tcp", worstHost, worstAdd, aborts)
									tst.stressMu.Lock()
									recheckGen := tst.stressGen == gen
									tst.stressMu.Unlock()
									if recheckGen {
										w.Invalidate()
									}
								}
								return
							}
							time.Sleep(time.Second)
						}
					}()
				}
			}
			if tst.stopStress.Clicked(gtx) {
				tst.stressMu.Lock()
				self, peers := tst.stressSelf, tst.stressPeers // the run's node set, not this frame's
				tst.stressOn = false
				tst.stressGen++ // invalidate the run's poll goroutine immediately
				tst.stressMu.Unlock()
				go a.StopStress(self, peers, "") // peer POSTs off the UI thread
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
				// Refresh test history at most every 5s while the tab is visible
				// (a tiny indexed LIMIT query; new runs surface within a tick).
				if time.Since(tst.histAt) > 5*time.Second {
					tst.netHist = a.TestHistory("internet", 5)
					tst.sweepHist = a.TestHistory("sweep", 5)
					tst.stressHist = a.TestHistory("stress", 5)
					tst.histAt = time.Now()
				}
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
			// horizontal margin is applied per item instead — and grows on wide
			// windows so the content column stays readable instead of stretching
			// edge to edge.
			layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return titleBar(gtx, th, snap, nav, &navDash, &navTst, &navEvt, &chrome)
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: unit.Dp(16)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						const maxContent = unit.Dp(1080)
						left, right := unit.Dp(20), unit.Dp(18)
						if w := gtx.Metric.PxToDp(gtx.Constraints.Max.X); w > maxContent+left+right {
							left = (w - maxContent) / 2
							right = left - 2
						}
						return material.List(th, &tabLists[nav]).Layout(gtx, len(items), func(gtx layout.Context, i int) layout.Dimensions {
							return layout.Inset{Left: left, Right: right}.Layout(gtx, items[i])
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
	ver := s.Version
	if ver == "" {
		ver = "dev"
	}
	plat := ""
	if s.Platform != "" {
		plat = " · " + s.Platform
	}
	sub := fmt.Sprintf("up %s · v%s%s · build %s", fmtDuration(s.SessionUptimeSec), ver, plat, versionOr(s.Build))
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
	reach := ""
	if s.ReachWarning != "" {
		reach = "⚠ " + s.ReachWarning
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
		caption(reach, colBad),
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

// layoutAdapters renders the local NICs, signal-first: links that are Up (or
// showing fresh faults) get a full line; idle ports collapse into one muted
// summary row instead of a stack of zero-filled lines. A row turns vermillion
// when any discard/error ticked up (direct NIC evidence during a drop) and
// amber when any power-saving property (EEE, Green Ethernet, Gigabit Lite, …)
// is enabled — the suspect lead.
func layoutAdapters(gtx layout.Context, th *material.Theme, s appcore.Snapshot) layout.Dimensions {
	if len(s.NICs) == 0 {
		return material.Body1(th, "Adapters: (none reported)").Layout(gtx)
	}
	var active []appcore.NICInfo
	var idle []string
	for _, n := range s.NICs {
		// A faulting port stays visible even when down: link-training flaps
		// produce errors precisely while not Up.
		if strings.EqualFold(n.Status, "Up") || adapterHasFaults(n) {
			active = append(active, n)
		} else {
			idle = append(idle, n.Name)
		}
	}
	children := make([]layout.FlexChild, 0, len(active)+2)
	children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return material.Body1(th, "Adapters:").Layout(gtx)
	}))
	for _, n := range active {
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
	if len(idle) > 0 {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(3), Left: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				lbl := material.Body1(th, "no link: "+strings.Join(idle, " · "))
				lbl.Color = colTextMut
				return lbl.Layout(gtx)
			})
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

// adapterLine is the pure text rendering of one NIC row. The middle segment
// is per-platform: Windows reports power-saving properties (EEE et al.),
// macOS reports link detail (Wi-Fi radio state, wired duplex); whichever the
// collector filled is shown. Counter deltas print only when something ticked
// up — a healthy line stays quiet instead of trailing zeros.
func adapterLine(n appcore.NICInfo) string {
	speed := n.LinkSpeed
	if !strings.EqualFold(n.Status, "Up") {
		speed = "—" // a down/disconnected link has no meaningful rate
	}
	line := n.Name + "  "
	if speed != "" {
		line += speed + "  "
	}
	line += n.Status
	switch {
	case n.Detail != "":
		line += "  " + n.Detail
	case n.Power != "":
		line += "  power[" + powerText(n.Power) + "]"
	}
	if n.RecentRxDiscards+n.RecentTxDiscards > 0 {
		line += fmt.Sprintf("  discards rx+%d tx+%d", n.RecentRxDiscards, n.RecentTxDiscards)
	}
	if n.RecentRxErrors+n.RecentTxErrors > 0 {
		line += fmt.Sprintf("  errors rx+%d tx+%d", n.RecentRxErrors, n.RecentTxErrors)
	}
	return line
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
	now := time.Now().UnixMicro()
	children := make([]layout.FlexChild, 0, 32)
	children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return material.Body1(th, "Events").Layout(gtx)
			}),
			layout.Rigid(gapX(10)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Caption(th, fmt.Sprintf("mesh-wide · %d recorded", len(s.Events)))
				l.Color = colTextMut
				return l.Layout(gtx)
			}),
		)
	}))
	if len(s.Events) == 0 {
		children = append(children, layout.Rigid(gap(10)), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Caption(th, "none yet — link flaps, loss episodes, and test runs land here")
			l.Color = colTextMut
			return l.Layout(gtx)
		}))
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	}
	children = append(children, layout.Rigid(gap(6)))
	const maxRows = 25
	shown := 0
	for i := len(s.Events) - 1; i >= 0 && shown < maxRows; i-- {
		e := s.Events[i]
		shown++
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return eventRow(gtx, th, e, now)
			})
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

// eventCol lays a fixed-width, vertically-centered column so event rows align
// into a scannable table.
func eventCol(width int, w layout.Widget) layout.FlexChild {
	return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = width
		gtx.Constraints.Max.X = width
		dims := w(gtx)
		dims.Size.X = width
		return dims
	})
}

// eventRow renders one merged event as an aligned table row: severity dot ·
// absolute time · relative age · host · detail. Offline events tint the detail.
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
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(dotWidget(dotCol, 8)),
			layout.Rigid(gapX(10)),
			eventCol(gtx.Dp(unit.Dp(66)), func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, unit.Sp(12), time.UnixMicro(e.UnixMicro).Format("15:04:05"))
				l.Color = colTextSec
				l.Font.Typeface = "Go Mono"
				return l.Layout(gtx)
			}),
			eventCol(gtx.Dp(unit.Dp(64)), func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, unit.Sp(11), coarseAge(now-e.UnixMicro))
				l.Color = colTextMut
				return l.Layout(gtx)
			}),
			eventCol(gtx.Dp(unit.Dp(104)), func(gtx layout.Context) layout.Dimensions {
				return layout.W.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return chipLabel(gtx, th, host, colTextSec, colCard)
				})
			}),
			layout.Rigid(gapX(6)),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, unit.Sp(13), e.Detail)
				l.Color = colTextPri
				if !e.Online {
					l.Color = colBad
				}
				return l.Layout(gtx)
			}),
		)
	})
}

// coarseAge renders an age in a single unit ("42s ago", "5m ago", "3h ago") —
// double-unit precision is noise at a glance.
func coarseAge(ageMicro int64) string {
	sec := ageMicro / 1_000_000
	if sec < 0 {
		sec = 0
	}
	switch {
	case sec < 60:
		return fmt.Sprintf("%ds ago", sec)
	case sec < 3600:
		return fmt.Sprintf("%dm ago", sec/60)
	default:
		return fmt.Sprintf("%dh ago", sec/3600)
	}
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
