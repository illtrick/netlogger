package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"netlogger/internal/appcore"
	"netlogger/internal/netmodel"
)

// ── Pure config mutators (unit-tested) ──────────────────────────────────────

func cloneConfig(c netmodel.Config) netmodel.Config {
	out := c
	out.Devices = make([]netmodel.Device, len(c.Devices))
	for i, d := range c.Devices {
		d.Interfaces = append([]netmodel.Interface(nil), d.Interfaces...)
		out.Devices[i] = d
	}
	return out
}

// addInterface appends a blank interface of the given medium to a device.
func addInterface(c netmodel.Config, deviceID string, m netmodel.Medium) netmodel.Config {
	c = cloneConfig(c)
	for i := range c.Devices {
		if c.Devices[i].ID == deviceID {
			c.Devices[i].Interfaces = append(c.Devices[i].Interfaces, netmodel.Interface{ID: string(m), Medium: m})
			break
		}
	}
	return c
}

// removeDevice drops a device by id.
func removeDevice(c netmodel.Config, deviceID string) netmodel.Config {
	c = cloneConfig(c)
	out := c.Devices[:0]
	for _, d := range c.Devices {
		if d.ID != deviceID {
			out = append(out, d)
		}
	}
	c.Devices = out
	return c
}

// addBlankDevice appends a new wired pc with a unique id and returns its index.
func addBlankDevice(c netmodel.Config) (netmodel.Config, int) {
	c = cloneConfig(c)
	id := fmt.Sprintf("device-%d", len(c.Devices)+1)
	for taken(c, id) {
		id += "x"
	}
	c.Devices = append(c.Devices, netmodel.Device{
		ID: id, Name: "New device", Type: netmodel.TypePC,
		Interfaces: []netmodel.Interface{{ID: "eth", Medium: netmodel.MediumWired}},
	})
	return c, len(c.Devices) - 1
}

func taken(c netmodel.Config, id string) bool {
	for _, d := range c.Devices {
		if d.ID == id {
			return true
		}
	}
	return false
}

// ── Editor state + layout ───────────────────────────────────────────────────

type ifaceEditors struct {
	ip, via, speed widget.Editor
	monitor        widget.Clickable
	rm             widget.Clickable
}

type editor struct {
	editOpen widget.Clickable // header "Edit network"
	active   bool
	loaded   bool
	cfg      netmodel.Config

	back widget.List // device list scroller
	rows []widget.Clickable
	sel  int

	syncedTo int // device index the field editors reflect (-1 → resync)
	closeBtn widget.Clickable
	addDev   widget.Clickable
	delDev   widget.Clickable
	save     widget.Clickable
	exportB  widget.Clickable
	importB  widget.Clickable
	addWired widget.Clickable
	addWifi  widget.Clickable

	name, model, typ widget.Editor
	ifaces           []ifaceEditors
}

func (e *editor) sel0() int {
	if e.sel < 0 {
		return 0
	}
	if e.sel >= len(e.cfg.Devices) {
		return len(e.cfg.Devices) - 1
	}
	return e.sel
}

func (e *editor) flush() {
	i := e.syncedTo
	if i < 0 || i >= len(e.cfg.Devices) {
		return
	}
	d := &e.cfg.Devices[i]
	d.Name = strings.TrimSpace(e.name.Text())
	d.Model = strings.TrimSpace(e.model.Text())
	d.Type = netmodel.DeviceType(strings.TrimSpace(e.typ.Text()))
	for j := range d.Interfaces {
		if j < len(e.ifaces) {
			d.Interfaces[j].IP = strings.TrimSpace(e.ifaces[j].ip.Text())
			d.Interfaces[j].Via = strings.TrimSpace(e.ifaces[j].via.Text())
			d.Interfaces[j].Speed = strings.TrimSpace(e.ifaces[j].speed.Text())
		}
	}
}

func (e *editor) sync() {
	d := e.cfg.Devices[e.sel0()]
	set := func(ed *widget.Editor, s string) { ed.SingleLine = true; ed.SetText(s) }
	set(&e.name, d.Name)
	set(&e.model, d.Model)
	set(&e.typ, string(d.Type))
	e.ifaces = make([]ifaceEditors, len(d.Interfaces))
	for j, ifc := range d.Interfaces {
		set(&e.ifaces[j].ip, ifc.IP)
		set(&e.ifaces[j].via, ifc.Via)
		set(&e.ifaces[j].speed, ifc.Speed)
	}
	e.syncedTo = e.sel0()
}

// process handles all editor input; returns a status message when one occurs.
func (e *editor) process(gtx layout.Context, a *appcore.App, snap appcore.Snapshot) string {
	if !e.loaded {
		e.cfg = cloneConfig(snap.NetConfig)
		e.loaded = true
		e.syncedTo = -1
	}
	e.flush() // capture current edits before structural changes

	msg := ""
	exeDir := func() string { p, _ := os.Executable(); return filepath.Dir(p) }

	if e.closeBtn.Clicked(gtx) {
		e.active = false
	}
	if e.addDev.Clicked(gtx) {
		e.cfg, e.sel = addBlankDevice(e.cfg)
		e.syncedTo = -1
	}
	if e.delDev.Clicked(gtx) && len(e.cfg.Devices) > 0 {
		e.cfg = removeDevice(e.cfg, e.cfg.Devices[e.sel0()].ID)
		e.sel = 0
		e.syncedTo = -1
	}
	if e.addWired.Clicked(gtx) && len(e.cfg.Devices) > 0 {
		e.cfg = addInterface(e.cfg, e.cfg.Devices[e.sel0()].ID, netmodel.MediumWired)
		e.syncedTo = -1
	}
	if e.addWifi.Clicked(gtx) && len(e.cfg.Devices) > 0 {
		e.cfg = addInterface(e.cfg, e.cfg.Devices[e.sel0()].ID, netmodel.MediumWifi)
		e.syncedTo = -1
	}
	for i := range e.rows {
		if i < len(e.rows) && e.rows[i].Clicked(gtx) {
			e.sel = i
		}
	}
	if e.sel0() == e.syncedTo {
		// interface toggles/removes only valid when editors are in sync
		d := &e.cfg.Devices[e.sel0()]
		for j := range e.ifaces {
			if j < len(d.Interfaces) && e.ifaces[j].monitor.Clicked(gtx) {
				d.Interfaces[j].Monitor = !d.Interfaces[j].Monitor
			}
		}
		for j := range e.ifaces {
			if j < len(d.Interfaces) && e.ifaces[j].rm.Clicked(gtx) {
				d.Interfaces = append(d.Interfaces[:j], d.Interfaces[j+1:]...)
				e.syncedTo = -1
				break
			}
		}
	}
	if e.save.Clicked(gtx) {
		e.flush()
		a.SetNetConfig(e.cfg)
		msg = "network saved"
	}
	if e.exportB.Clicked(gtx) {
		if data, err := netmodel.Marshal(e.cfg); err == nil {
			p := filepath.Join(exeDir(), fmt.Sprintf("network-export-%d.json", time.Now().Unix()))
			if os.WriteFile(p, data, 0o644) == nil {
				msg = "exported " + filepath.Base(p)
			}
		}
	}
	if e.importB.Clicked(gtx) {
		if data, err := os.ReadFile(filepath.Join(exeDir(), "network-import.json")); err == nil {
			if cfg, perr := netmodel.Parse(data); perr == nil {
				e.cfg = cfg
				e.sel = 0
				e.syncedTo = -1
				msg = "imported network-import.json"
			}
		} else {
			msg = "drop a network-import.json beside the exe first"
		}
	}

	if len(e.cfg.Devices) == 0 {
		e.cfg, e.sel = addBlankDevice(e.cfg)
		e.syncedTo = -1
	}
	if e.sel0() != e.syncedTo {
		e.sync()
	}
	return msg
}

func (e *editor) layout(gtx layout.Context, th *material.Theme) layout.Dimensions {
	// keep one clickable per device row
	if len(e.rows) != len(e.cfg.Devices) {
		e.rows = make([]widget.Clickable, len(e.cfg.Devices))
	}
	return card(gtx, colCard, colBorder, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return sectionTitle(gtx, th, "Network setup")
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: gtx.Constraints.Min} }),
					editBtn(th, &e.importB, "Import"),
					editBtn(th, &e.exportB, "Export"),
					editBtn(th, &e.closeBtn, "Done"),
				)
			}),
			layout.Rigid(gap(12)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Start}.Layout(gtx,
					layout.Flexed(0.36, func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Right: unit.Dp(14)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return e.layoutList(gtx, th)
						})
					}),
					layout.Flexed(0.64, func(gtx layout.Context) layout.Dimensions {
						return e.layoutForm(gtx, th)
					}),
				)
			}),
		)
	})
}

func (e *editor) layoutList(gtx layout.Context, th *material.Theme) layout.Dimensions {
	children := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return material.Button(th, &e.addDev, "+ Add device").Layout(gtx)
		}),
		layout.Rigid(gap(10)),
	}
	for i := range e.cfg.Devices {
		i := i
		d := e.cfg.Devices[i]
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(3)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return e.rows[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					bg := colCard
					if i == e.sel0() {
						bg = colCardAlt
					}
					return roundedBG(gtx, bg, unit.Dp(7), unit.Dp(8), func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								l := material.Label(th, unit.Sp(13), d.Name)
								l.Color = colTextPri
								if i == e.sel0() {
									l.Font.Weight = 500
								}
								return l.Layout(gtx)
							}),
							layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: gtx.Constraints.Min} }),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								if !d.Agent {
									return layout.Dimensions{}
								}
								return chipLabel(gtx, th, "auto", colGood, colCard)
							}),
						)
					})
				})
			})
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

func (e *editor) layoutForm(gtx layout.Context, th *material.Theme) layout.Dimensions {
	d := e.cfg.Devices[e.sel0()]
	children := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Right: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return fieldEditor(gtx, th, "Name", &e.name)
					})
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return fieldEditor(gtx, th, "Type (router/switch/pc/nas/modem)", &e.typ)
				}),
			)
		}),
		layout.Rigid(gap(10)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return fieldEditor(gtx, th, "Model", &e.model) }),
		layout.Rigid(gap(14)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return sectionTitle(gtx, th, "Connections · identified by MAC")
		}),
	}
	for j := range d.Interfaces {
		j := j
		ifc := d.Interfaces[j]
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return e.layoutIface(gtx, th, ifc, j)
			})
		}))
	}
	children = append(children,
		layout.Rigid(gap(10)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				editBtn(th, &e.addWired, "+ Wired"),
				editBtn(th, &e.addWifi, "+ Wi-Fi"),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: gtx.Constraints.Min} }),
				editBtn(th, &e.delDev, "Delete device"),
				editBtn(th, &e.save, "Save"),
			)
		}),
	)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

func (e *editor) layoutIface(gtx layout.Context, th *material.Theme, ifc netmodel.Interface, j int) layout.Dimensions {
	if j >= len(e.ifaces) {
		return layout.Dimensions{}
	}
	medium := "Ethernet · wired"
	if ifc.Medium == netmodel.MediumWifi {
		medium = "Wi-Fi · wireless"
	}
	mac := ifc.MAC
	if mac == "" {
		mac = "(auto on launch)"
	}
	monLabel := "monitor: off"
	if ifc.Monitor {
		monLabel = "monitor: on"
	}
	return roundedBG(gtx, colCardAlt, unit.Dp(8), unit.Dp(12), func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						l := material.Label(th, unit.Sp(13), medium)
						l.Color = colTextPri
						l.Font.Weight = 500
						return l.Layout(gtx)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return chipLabel(gtx, th, mac, colTextSec, colCard)
						})
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: gtx.Constraints.Min} }),
					editBtn(th, &e.ifaces[j].monitor, monLabel),
					editBtn(th, &e.ifaces[j].rm, "remove"),
				)
			}),
			layout.Rigid(gap(8)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return fieldEditor(gtx, th, "IP", &e.ifaces[j].ip)
						})
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return fieldEditor(gtx, th, "Connects to (device id)", &e.ifaces[j].via)
						})
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return fieldEditor(gtx, th, "Speed", &e.ifaces[j].speed)
					}),
				)
			}),
		)
	})
}

// ── small UI helpers ────────────────────────────────────────────────────────

func editBtn(th *material.Theme, b *widget.Clickable, label string) layout.FlexChild {
	return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Left: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return material.Button(th, b, label).Layout(gtx)
		})
	})
}

func fieldEditor(gtx layout.Context, th *material.Theme, label string, ed *widget.Editor) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Label(th, unit.Sp(12), label)
			l.Color = colTextSec
			return l.Layout(gtx)
		}),
		layout.Rigid(gap(4)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return widget.Border{Color: colBorder, Width: unit.Dp(1), CornerRadius: unit.Dp(8)}.Layout(gtx,
				func(gtx layout.Context) layout.Dimensions {
					return roundedBG(gtx, colBg, unit.Dp(8), unit.Dp(9), func(gtx layout.Context) layout.Dimensions {
						ce := material.Editor(th, ed, "")
						ce.Color = colTextPri
						ce.HintColor = colTextMut
						return ce.Layout(gtx)
					})
				})
		}),
	)
}
