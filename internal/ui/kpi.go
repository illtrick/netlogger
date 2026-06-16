package ui

import (
	"fmt"
	"image/color"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"netlogger/internal/appcore"
)

type kpi struct {
	label string
	value string
	sub   string
	tone  color.NRGBA
}

// kpis is the pure mapping from a snapshot to the four headline tiles.
func kpis(s appcore.Snapshot) []kpi {
	drops := 0
	for _, p := range s.Peers {
		drops += p.DropEpisodes
	}
	dropTone := colTextPri
	if drops > 0 {
		dropTone = colWatch
	}
	gwSub, netSub := s.GatewayIP, s.InternetIP
	if gwSub == "" {
		gwSub = "not detected"
	}
	if netSub == "" {
		netSub = "not detected"
	}
	return []kpi{
		{"Gateway RTT", fmt.Sprintf("%.1f ms", s.GatewayRTTms), gwSub, colTextPri},
		{"Internet RTT", fmt.Sprintf("%.1f ms", s.InternetRTTms), netSub, colTextPri},
		{"Peers online", fmt.Sprintf("%d", len(s.Peers)), "discovered", colTextPri},
		{"Drops (session)", fmt.Sprintf("%d", drops), "across all links", dropTone},
	}
}

func layoutKPIs(gtx layout.Context, th *material.Theme, s appcore.Snapshot) layout.Dimensions {
	tiles := kpis(s)
	children := make([]layout.FlexChild, 0, len(tiles))
	for i := range tiles {
		k := tiles[i]
		last := i == len(tiles)-1
		children = append(children, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			in := layout.Inset{}
			if !last {
				in.Right = unit.Dp(12)
			}
			return in.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return card(gtx, colCard, colBorder, func(gtx layout.Context) layout.Dimensions {
					return kpiTile(gtx, th, k)
				})
			})
		}))
	}
	return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, children...)
}

func kpiTile(gtx layout.Context, th *material.Theme, k kpi) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Label(th, unit.Sp(12), k.label)
			l.Color = colTextSec
			return l.Layout(gtx)
		}),
		layout.Rigid(gap(10)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			v := material.Label(th, unit.Sp(26), k.value)
			v.Color = k.tone
			v.Font.Weight = 500
			v.Font.Typeface = "Go Mono"
			return v.Layout(gtx)
		}),
		layout.Rigid(gap(8)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Label(th, unit.Sp(11), k.sub)
			l.Color = colTextSec
			return l.Layout(gtx)
		}),
	)
}
