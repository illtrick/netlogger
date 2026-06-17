package ui

import (
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"netlogger/internal/appcore"
	"netlogger/internal/netmodel"
)

// viaLabel summarizes a device's uplinks as "wired→sw2 · wifi→router".
func viaLabel(d netmodel.Device) string {
	var parts []string
	for _, ifc := range d.Interfaces {
		if ifc.Via == "" {
			continue
		}
		parts = append(parts, string(ifc.Medium)+"→"+ifc.Via)
	}
	return strings.Join(parts, " · ")
}

// deviceHealth returns a device's measured loss and whether any data exists:
// agents are matched to a peer (or self) by node id / hostname, infrastructure
// by a monitored interface IP. Unmeasured devices report hasData=false (muted).
func deviceHealth(d netmodel.Device, s appcore.Snapshot) (float64, bool) {
	if d.NodeUUID != "" && d.NodeUUID == s.SelfNodeID {
		return 0, true // this machine, from its own perspective
	}
	for _, p := range s.Peers {
		if (d.NodeUUID != "" && p.ID == d.NodeUUID) || strings.EqualFold(p.Host, d.Name) {
			return p.LossPct, true
		}
	}
	for _, ifc := range d.Interfaces {
		if ifc.Monitor && ifc.IP != "" {
			if l, ok := s.MonitorLoss[ifc.IP]; ok {
				return l, true
			}
		}
	}
	return 0, false
}

// deviceSkew returns the peer's build when it differs from this machine's, so a
// stale binary shows on its device; "" when matched or unknown.
func deviceSkew(d netmodel.Device, s appcore.Snapshot) string {
	for _, p := range s.Peers {
		if (d.NodeUUID != "" && p.ID == d.NodeUUID) || strings.EqualFold(p.Host, d.Name) {
			if p.Build != "" && p.Build != s.Build {
				return p.Build
			}
		}
	}
	return ""
}

func isSelfDevice(d netmodel.Device, s appcore.Snapshot) bool {
	return d.NodeUUID != "" && d.NodeUUID == s.SelfNodeID
}

// layoutTopology renders the connector-less tiered network map.
func layoutTopology(gtx layout.Context, th *material.Theme, s appcore.Snapshot) layout.Dimensions {
	children := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return sectionTitle(gtx, th, "Network")
		}),
	}
	for i := range s.Topology {
		tier := s.Topology[i]
		if len(tier.Devices) == 0 {
			continue
		}
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return tierRow(gtx, th, s, tier)
			})
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

func tierRow(gtx layout.Context, th *material.Theme, s appcore.Snapshot, tier netmodel.Tier) layout.Dimensions {
	cards := make([]layout.FlexChild, 0, len(tier.Devices))
	for i := range tier.Devices {
		d := tier.Devices[i]
		cards = append(cards, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return topoDevice(gtx, th, s, d)
			})
		}))
	}
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Start}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Dp(unit.Dp(82))
			gtx.Constraints.Max.X = gtx.Dp(unit.Dp(82))
			return layout.Inset{Top: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, unit.Sp(12), tier.Name)
				l.Color = colTextSec
				return l.Layout(gtx)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, cards...)
		}),
	)
}

func topoDevice(gtx layout.Context, th *material.Theme, s appcore.Snapshot, d netmodel.Device) layout.Dimensions {
	loss, hasData := deviceHealth(d, s)
	dotCol := sevColor(loss, hasData)
	via := viaLabel(d)
	skew := deviceSkew(d, s)
	return roundedBG(gtx, colCardAlt, unit.Dp(8), unit.Dp(10), func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(dotWidget(dotCol, 8)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							l := material.Label(th, unit.Sp(13), d.Name)
							l.Color = colTextPri
							l.Font.Weight = 500
							return l.Layout(gtx)
						})
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if !isSelfDevice(d, s) {
							return layout.Dimensions{}
						}
						return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return chipLabel(gtx, th, "this machine", colAccent, colCard)
						})
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if skew == "" {
							return layout.Dimensions{}
						}
						return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return chipLabel(gtx, th, "build "+skew, colWatch, colCard)
						})
					}),
				)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if d.Model == "" {
					return layout.Dimensions{}
				}
				return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					l := material.Label(th, unit.Sp(11), d.Model)
					l.Color = colTextSec
					return l.Layout(gtx)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if via == "" {
					return layout.Dimensions{}
				}
				return layout.Inset{Top: unit.Dp(3)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					l := material.Label(th, unit.Sp(10), via)
					l.Color = colTextMut
					return l.Layout(gtx)
				})
			}),
		)
	})
}
