// Package ui renders the portable app's native window with Gio. For N1 it shows
// a minimal live-status panel read from appcore.Snapshot.
package ui

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gioui.org/app"
	"gioui.org/font/gofont"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"netlogger/internal/appcore"
)

var blue = color.NRGBA{R: 0x4c, G: 0xc2, B: 0xff, A: 0xff}
var orange = color.NRGBA{R: 0xD5, G: 0x5E, B: 0x00, A: 0xff} // vermillion — discards/errors ticking up
var amber = color.NRGBA{R: 0xE6, G: 0x9F, B: 0x00, A: 0xff}  // power-saving enabled (a suspect, not yet an error)

// Run opens the window and renders until it is closed.
func Run(a *appcore.App) error {
	w := new(app.Window)
	w.Option(app.Title("NetLogger"), app.Size(unit.Dp(820), unit.Dp(560)))

	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))

	go func() {
		for {
			time.Sleep(time.Second)
			w.Invalidate()
		}
	}()

	var resetBtn, exportBtn, sleepBtn widget.Clickable
	var statusMsg string

	var ops op.Ops
	for {
		switch e := w.Event().(type) {
		case app.DestroyEvent:
			return e.Err
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
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

			// Outer vertical scroll list — wrap everything in inset then flex.
			layout.UniformInset(unit.Dp(16)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					// ── Header ──────────────────────────────────────────
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layoutHeader(gtx, th, snap, &resetBtn, &exportBtn, &sleepBtn, statusMsg)
					}),
					layout.Rigid(spacer(8)),
					// ── Infrastructure ──────────────────────────────────
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layoutInfra(gtx, th, snap)
					}),
					layout.Rigid(spacer(8)),
					// ── Peers ───────────────────────────────────────────
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layoutPeers(gtx, th, snap)
					}),
					layout.Rigid(spacer(8)),
					// ── Adapters (NIC health / power-saving) ────────────
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layoutAdapters(gtx, th, snap)
					}),
					layout.Rigid(spacer(8)),
					// ── Matrix + legend ─────────────────────────────────
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layoutMatrixSection(gtx, th, snap)
					}),
					layout.Rigid(spacer(8)),
					// ── Recent events (live timeline) ───────────────────
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layoutEvents(gtx, th, snap)
					}),
					layout.Rigid(spacer(8)),
					// ── Compact footer (data dir / iperf) ───────────────
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layoutFooter(gtx, th, snap)
					}),
				)
			})

			e.Frame(gtx.Ops)
		}
	}
}

// spacer returns a rigid flex child that simply advances by h dp vertically.
func spacer(h int) func(layout.Context) layout.Dimensions {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Dimensions{Size: image.Pt(0, gtx.Dp(unit.Dp(h)))}
	}
}

// layoutHeader renders a row: bold "NetLogger" | status chip | uptime | buttons | status message.
func layoutHeader(gtx layout.Context, th *material.Theme, s appcore.Snapshot, resetBtn, exportBtn, sleepBtn *widget.Clickable, statusMsg string) layout.Dimensions {
	statusText, statusColor := overallStatus(s)
	uptime := fmt.Sprintf("up %s", fmtDuration(s.SessionUptimeSec))
	sleepLabel := "Sleep: prevented"
	if !s.PreventSleep {
		sleepLabel = "Sleep: allowed"
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return material.H6(th, "NetLogger").Layout(gtx)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(16), Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						lbl := material.Body1(th, "● "+statusText)
						lbl.Color = statusColor
						return lbl.Layout(gtx)
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return material.Body1(th, uptime).Layout(gtx)
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Dimensions{Size: gtx.Constraints.Min}
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return material.Button(th, sleepBtn, sleepLabel).Layout(gtx)
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return material.Button(th, resetBtn, "Reset all").Layout(gtx)
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return material.Button(th, exportBtn, "Export").Layout(gtx)
					})
				}),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if s.BuildWarning == "" {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				lbl := material.Caption(th, "⚠ "+s.BuildWarning)
				lbl.Color = orange
				return lbl.Layout(gtx)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if statusMsg == "" {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return material.Caption(th, statusMsg).Layout(gtx)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if s.LastReset == "" {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return material.Caption(th, s.LastReset).Layout(gtx)
			})
		}),
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
	return fmt.Sprintf("%s  %s  %s  power[%s]  discards rx+%d tx+%d  errors rx+%d tx+%d",
		n.Name, n.LinkSpeed, n.Status, powerText(n.Power),
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
	if len(s.RecentEvents) == 0 {
		return material.Body1(th, "Recent events: (none yet)").Layout(gtx)
	}
	now := time.Now().UnixMicro()
	const maxRows = 8
	children := make([]layout.FlexChild, 0, maxRows+1)
	children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return material.Body1(th, "Recent events:").Layout(gtx)
	}))
	shown := 0
	for i := len(s.RecentEvents) - 1; i >= 0 && shown < maxRows; i-- {
		e := s.RecentEvents[i]
		shown++
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(2), Left: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				lbl := material.Caption(th, eventLine(e, now))
				if !e.Online {
					lbl.Color = orange
				}
				return lbl.Layout(gtx)
			})
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

// eventLine renders one event as "<age> ago  <detail>" relative to nowMicro.
func eventLine(e appcore.EventInfo, nowMicro int64) string {
	age := (nowMicro - e.UnixMicro) / 1_000_000
	if age < 0 {
		age = 0
	}
	return fmt.Sprintf("%s ago  %s", fmtDuration(age), e.Detail)
}

// layoutMatrixSection renders the link matrix and its legend.
func layoutMatrixSection(gtx layout.Context, th *material.Theme, s appcore.Snapshot) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layoutMatrix(gtx, th, s.Matrix)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return material.Caption(th, "loss: green <0.1%   orange <1%   red ≥1%   ·   rows = source, cols = destination").Layout(gtx)
			})
		}),
	)
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
