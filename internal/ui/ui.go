// Package ui renders the portable app's native window with Gio. For N1 it shows
// a minimal live-status panel read from appcore.Snapshot.
package ui

import (
	"fmt"
	"time"

	"gioui.org/app"
	"gioui.org/font/gofont"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"netlogger/internal/appcore"
)

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

	var ops op.Ops
	for {
		switch e := w.Event().(type) {
		case app.DestroyEvent:
			return e.Err
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			layoutStatus(gtx, th, a.Snapshot())
			e.Frame(gtx.Ops)
		}
	}
}

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
