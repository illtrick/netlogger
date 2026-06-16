# NetLogger UI Overhaul Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the flat, light, text-readout UI with a dark, card-based "ops dashboard"; add a **config-driven network topology** (the user's real gear, edited in-app and saved as JSON) and a **time-series** view (latency bands + a multi-hour loss heatmap), so a general-public user can map their network, monitor any device, and see intermittent faults at a glance.

**Architecture:** Three independently-shippable milestones. **UI-1 (re-skin)** is pure presentation in `internal/ui` — a dark theme palette, a spacing scale, and a `card` layout helper, with the existing sections restyled plus a status hero, KPI tiles, and a polished event feed; no engine changes. **UI-2 (topology + editor)** adds an `internal/netmodel` package (a portable JSON config of typed devices, each with **per-interface** wired/Wi-Fi links uniquely identified by MAC, an optional `monitor` probe flag, and a `via` parent for the tier hierarchy); `appcore` reads it to add monitored devices as ping targets and exposes a topology snapshot; the UI renders a connector-less tiered topology and a two-pane equipment editor that writes the config back. **UI-3 (time-series)** stores a per-poll jitter envelope and queries bucketed per-link loss from the store to render a SmokePing-style band and a 12-hour heatmap.

**Tech Stack:** Go (cgo-free), Gio v0.10.0 (immediate-mode; rendering is hand-drawn — pure helpers are unit-tested, composition is verified at a manual gate, matching the repo's prior UI plans), `modernc.org/sqlite`, `encoding/json`.

---

## Design system (shared reference for all UI tasks)

Add these as the single source of truth in `internal/ui/theme.go` (UI-1 Task 1). All later UI tasks reference these names — never hardcode a hex value twice.

**Palette (dark):**

| Name | RGBA hex | Use |
|---|---|---|
| `colBg` | `#0E1620` | window background |
| `colTitleBar` | `#111A26` | title bar |
| `colCard` | `#161E29` | card / tile surface |
| `colCardAlt` | `#131C27` | nested surface (interface card, toggle row) |
| `colInput` | `#0E1620` | input field bg |
| `colBorder` | `#FFFFFF` @ 0x12 alpha | default card border |
| `colTextPri` | `#EAF1F8` | primary text |
| `colTextSec` | `#93A1B0` | secondary text / labels |
| `colTextMut` | `#6E7B8A` | tertiary / "via" hints, neutral dot |
| `colAccent` | `#58A6FF` | primary buttons, sparklines, "this machine" |
| `colGood` | `#3FB950` | healthy |
| `colWatch` | `#D29922` | watch (0.1–1% loss) |
| `colBad` | `#F85149` | degraded (≥1% loss) |
| `colBadText` | `#F8918C` | degraded text on dark |

**Spacing scale (dp):** `4, 8, 12, 16, 20, 24, 28`. Window content inset `20`. Gap between major sections `24`. Card inner padding `16`. Gap between cards `12`. Label-to-content `12`.

**Type (sp):** KPI value `26`; node/peer name `15`; body `14`; section label `13` (`colTextSec`); metric/mono numbers `12`; chips/sub `11`. Two weights only (regular / medium-`500`). Numbers use the existing mono face (`gofont` mono) for column alignment.

**Severity mapping (reuse existing `sevColor` in `internal/ui/matrix.go`):** loss `<0.1%`→`colGood`, `<1%`→`colWatch`, `≥1%`→`colBad`, no-data→`colTextMut`.

---

## File structure

| Path | Responsibility | Milestone |
|---|---|---|
| `internal/ui/theme.go` | palette + spacing constants + `card()` layout helper + dark `material.Theme` | UI-1 |
| `internal/ui/ui.go` | restructured layout: title bar, status hero, KPI tiles, sections, event feed | UI-1 |
| `internal/ui/kpi.go` | `layoutKPIs` + `kpiTile` | UI-1 |
| `internal/netmodel/netmodel.go` | `Config`, `Device`, `Interface`, `Medium`, JSON `Load`/`Save`/`Default` | UI-2 |
| `internal/netmodel/topology.go` | pure `Tiers()` + `MonitorTargets()` derivation | UI-2 |
| `internal/netmodel/discover.go` | merge auto-discovered agents into a config | UI-2 |
| `internal/netmodel/*_test.go` | parse/identity/tier/monitor/merge tests | UI-2 |
| `internal/appcore/appcore.go` | load config; add monitored targets to probing; `Snapshot.Topology` | UI-2 |
| `internal/ui/topology.go` | connector-less tiered topology view | UI-2 |
| `internal/ui/editor.go` | two-pane equipment editor (list + per-interface form) | UI-2 |
| `internal/store/store.go` | `LossBuckets()` query for the heatmap | UI-3 |
| `internal/appcore/history.go` | per-poll jitter envelope (min/max) ring | UI-3 |
| `internal/ui/timeseries.go` | latency band + 12h heatmap rendering | UI-3 |

---

# Milestone UI-1 — dark re-skin + spacing + cards

No engine changes. Restyle `internal/ui` only. End state: the existing data renders on a dark, card-based layout with a status hero, four KPI tiles, and a polished mesh-wide event feed.

## Task 1: theme palette + card helper

**Files:**
- Create: `internal/ui/theme.go`
- Test: `internal/ui/theme_test.go`

- [ ] **Step 1: Write the failing test**

```go
package ui

import (
	"image/color"
	"testing"
)

func TestSevColorBands(t *testing.T) {
	if sevColor(0.0, true) != colGood {
		t.Fatalf("0%% should be good")
	}
	if sevColor(0.5, true) != colWatch {
		t.Fatalf("0.5%% should be watch")
	}
	if sevColor(2.0, true) != colBad {
		t.Fatalf("2%% should be bad")
	}
	if sevColor(0.0, false) != colTextMut {
		t.Fatalf("no-data should be muted")
	}
}

func TestPaletteIsDark(t *testing.T) {
	// primary text must be light and the background dark (mental dark-mode check)
	if colTextPri.R < 0xC0 || colBg.R > 0x40 {
		t.Fatalf("palette is not dark: text=%v bg=%v", colTextPri, colBg)
	}
	_ = color.NRGBA{}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/ui/ -run 'TestSevColorBands|TestPaletteIsDark'` → FAIL (undefined `colGood`, etc.).

- [ ] **Step 3: Implement `internal/ui/theme.go`** — define every palette constant from the Design-System table as `color.NRGBA`, the spacing constants, and rework `sevColor` to return the palette colors. Also add the `card` helper and a dark theme builder:

```go
package ui

import (
	"image"
	"image/color"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

var (
	colBg       = color.NRGBA{R: 0x0E, G: 0x16, B: 0x20, A: 0xFF}
	colTitleBar = color.NRGBA{R: 0x11, G: 0x1A, B: 0x26, A: 0xFF}
	colCard     = color.NRGBA{R: 0x16, G: 0x1E, B: 0x29, A: 0xFF}
	colCardAlt  = color.NRGBA{R: 0x13, G: 0x1C, B: 0x27, A: 0xFF}
	colBorder   = color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0x12}
	colTextPri  = color.NRGBA{R: 0xEA, G: 0xF1, B: 0xF8, A: 0xFF}
	colTextSec  = color.NRGBA{R: 0x93, G: 0xA1, B: 0xB0, A: 0xFF}
	colTextMut  = color.NRGBA{R: 0x6E, G: 0x7B, B: 0x8A, A: 0xFF}
	colAccent   = color.NRGBA{R: 0x58, G: 0xA6, B: 0xFF, A: 0xFF}
	colGood     = color.NRGBA{R: 0x3F, G: 0xB9, B: 0x50, A: 0xFF}
	colWatch    = color.NRGBA{R: 0xD2, G: 0x99, B: 0x22, A: 0xFF}
	colBad      = color.NRGBA{R: 0xF8, G: 0x51, B: 0x49, A: 0xFF}
	colBadText  = color.NRGBA{R: 0xF8, G: 0x91, B: 0x8C, A: 0xFF}
)

// gap returns a rigid vertical spacer of n dp (replaces the old spacer()).
func gap(n int) layout.Spacer { return layout.Spacer{Height: unit.Dp(n)} }

// fillRoundRect paints a filled rounded rectangle of the given color behind w.
func card(gtx layout.Context, bg, border color.NRGBA, w layout.Widget) layout.Dimensions {
	return widgetWithBG(gtx, bg, border, unit.Dp(12), unit.Dp(16), w)
}

func widgetWithBG(gtx layout.Context, bg, border color.NRGBA, radius, pad unit.Dp, w layout.Widget) layout.Dimensions {
	macro := op.Record(gtx.Ops)
	dims := layout.UniformInset(pad).Layout(gtx, w)
	call := macro.Stop()
	rr := clip.RRect{Rect: image.Rectangle{Max: dims.Size}, SE: gtx.Dp(radius), SW: gtx.Dp(radius), NE: gtx.Dp(radius), NW: gtx.Dp(radius)}
	paint.FillShape(gtx.Ops, bg, rr.Op(gtx.Ops))
	// 1px border
	paint.FillShape(gtx.Ops, border, clip.Stroke{Path: rr.Path(gtx.Ops), Width: float32(gtx.Dp(1))}.Op())
	call.Add(gtx.Ops)
	return dims
}

func darkTheme(base *material.Theme) *material.Theme {
	th := *base
	th.Palette = material.Palette{
		Bg:         colBg,
		Fg:         colTextPri,
		ContrastBg: colAccent,
		ContrastFg: colBg,
	}
	return &th
}
```

Replace the old `sevColor` body in `matrix.go` to return `colGood/colWatch/colBad/colTextMut` (remove the duplicate literals there). Delete the now-unused `spacer()` if present, or keep it as a thin wrapper over `gap`.

- [ ] **Step 4: Run to verify pass** — `go test ./internal/ui/ -run 'TestSevColorBands|TestPaletteIsDark'` → PASS. `go build ./...`, `go vet ./internal/ui/`, `gofmt -w internal/ui/`.

- [ ] **Step 5: Commit** — `git add internal/ui/ && git commit -m "feat(ui): dark theme palette + card helper (ui-1)"`.

## Task 2: paint the window background + dark theme + title bar

**Files:** Modify `internal/ui/ui.go` (`Run`).

- [ ] **Step 1:** In `Run`, build the theme with `th := darkTheme(material.NewTheme())` (keep the existing shaper assignment). At the top of the `FrameEvent` case, fill the whole frame with `colBg` before laying out:

```go
paint.Fill(gtx.Ops, colBg)
```

- [ ] **Step 2:** Replace the outer `layout.UniformInset(unit.Dp(16))` with `unit.Dp(20)` and replace every `layout.Rigid(spacer(8))` between sections with `layout.Rigid(gap(24).Layout)` (section rhythm). Within multi-row sections use `gap(12)`.

- [ ] **Step 3:** Add a title-bar row as the first child of the outer flex: a `colTitleBar`-filled strip (use `widgetWithBG` with radius 0) containing a small `colAccent` dot + "NetLogger" in `material.Body2` `colTextSec`. (Cosmetic; the OS already draws the real title bar — this is the in-app header band.)

- [ ] **Step 4: Verify + commit** — `go build ./...`; `git add internal/ui/ && git commit -m "feat(ui): dark window background + theme + header band (ui-1)"`. **Manual gate:** relaunch; the window is dark, text readable, sections have clear breathing room.

## Task 3: status hero + KPI tiles

**Files:** Create `internal/ui/kpi.go`; modify `internal/ui/ui.go` (header) and `internal/ui/theme.go` if a helper is needed.

- [ ] **Step 1: Write the failing test** (`internal/ui/kpi_test.go`) — KPI formatting is pure:

```go
package ui

import (
	"testing"

	"netlogger/internal/appcore"
)

func TestKPIValues(t *testing.T) {
	s := appcore.Snapshot{GatewayRTTms: 0.5, InternetRTTms: 4.0, Peers: []appcore.PeerInfo{{}, {}}}
	k := kpis(s)
	if len(k) != 4 {
		t.Fatalf("want 4 KPI tiles, got %d", len(k))
	}
	if k[0].value != "0.5 ms" || k[2].value != "2" {
		t.Fatalf("kpi values wrong: %+v", k)
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/ui/ -run TestKPIValues` → FAIL.

- [ ] **Step 3: Implement `kpis` + `kpiTile` + `layoutKPIs`** in `internal/ui/kpi.go`:

```go
package ui

import (
	"fmt"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget/material"
	"image/color"

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
	return []kpi{
		{"Gateway RTT", fmt.Sprintf("%.1f ms", s.GatewayRTTms), s.GatewayIP, colGood},
		{"Internet RTT", fmt.Sprintf("%.1f ms", s.InternetRTTms), s.InternetIP, colGood},
		{"Peers online", fmt.Sprintf("%d", len(s.Peers)), "discovered", colTextPri},
		{"Drops (session)", fmt.Sprintf("%d", drops), "across all links", dropTone},
	}
}

func layoutKPIs(gtx layout.Context, th *material.Theme, s appcore.Snapshot) layout.Dimensions {
	tiles := kpis(s)
	children := make([]layout.FlexChild, 0, len(tiles))
	for i := range tiles {
		k := tiles[i]
		children = append(children, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			inset := layout.Inset{}
			if i < len(tiles)-1 {
				inset.Right = unit.Dp(12)
			}
			return inset.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
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
		layout.Rigid(gap(10).Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			v := material.Label(th, unit.Sp(26), k.value)
			v.Color = k.tone
			v.Font.Weight = 500
			return v.Layout(gtx)
		}),
		layout.Rigid(gap(8).Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Label(th, unit.Sp(11), k.sub)
			l.Color = colTextSec
			return l.Layout(gtx)
		}),
	)
}
```

- [ ] **Step 4:** Rework `layoutHeader` into a status hero: a 13-dp status dot tinted by `overallStatus`, the status text at `unit.Sp(20)`, and a second line `colTextSec` with "session up <dur>"; keep the `Sleep/Reset all/Export` buttons right-aligned (restyle with `colCard` bg via a small button helper, accent the Export). Insert `layoutKPIs` as a new section between the header and Infrastructure in `Run`'s flex (with `gap(24)` around it).

- [ ] **Step 5: Run to verify pass + build** — `go test ./internal/ui/ -run TestKPIValues` → PASS; `go build ./...`, `go vet`, `gofmt -w internal/ui/`.

- [ ] **Step 6: Commit** — `git commit -am "feat(ui): status hero + KPI tiles (ui-1)"`. **Manual gate:** relaunch; hero shows overall status + uptime; four tiles show gateway/internet RTT, peer count, drops.

## Task 4: restyle infra/peer/matrix sections as cards + polish event feed

**Files:** Modify `internal/ui/ui.go`.

- [ ] **Step 1:** Wrap `layoutInfra`, each peer block in `layoutPeers`, `layoutAdapters`, `layoutMatrixSection`, and `layoutEvents` bodies in `card(gtx, colCard, colBorder, …)`. Set all `material.Body1/Caption` colors explicitly to `colTextPri`/`colTextSec`; switch every numeric/model string to the mono font (`lbl.Font.Typeface = "Go Mono"` equivalent already in the shaper, or reuse a `monoLabel` helper). Replace the footer's gray with `colTextMut`.

- [ ] **Step 2:** Event feed: in `layoutEvents`, give each row a left accent bar (`colBad` when `!e.Online`, else `colGood`) using `widgetWithBG` with only a left inset + a 3-dp filled rect, a `colCardAlt` row bg, the host as a small chip (`colCardAlt`, `colTextSec`), `12`-dp row padding, `6`-dp gaps; bump `maxRows` to 10.

- [ ] **Step 3: Verify + commit** — `go build ./...`, `go test ./internal/ui/`, `gofmt -w internal/ui/`; `git commit -am "feat(ui): card-wrap sections + polished event feed (ui-1)"`. **Manual gate:** relaunch; every section is a dark card; the event feed rows have severity accents and host chips. **UI-1 ships here.**

---

# Milestone UI-2 — config-driven topology + equipment editor

Introduce the portable network config (typed devices, per-interface wired/Wi-Fi links keyed by MAC, `monitor` probe flag), wire monitored devices into the engine, render the tiered topology, and add the editor. **Depends on UI-1.**

## Task 1: `internal/netmodel` types + JSON load/save/default

**Files:** Create `internal/netmodel/netmodel.go`, `internal/netmodel/netmodel_test.go`.

- [ ] **Step 1: Write the failing test**

```go
package netmodel

import "testing"

func TestRoundTripAndIdentity(t *testing.T) {
	cfg := Default()
	// ryzen is multi-homed: two interfaces, distinct media + MACs
	ryzen, ok := cfg.Device("ryzen")
	if !ok || len(ryzen.Interfaces) != 2 {
		t.Fatalf("ryzen should have 2 interfaces, got %+v", ryzen)
	}
	var wired, wifi int
	macs := map[string]bool{}
	for _, ifc := range ryzen.Interfaces {
		if ifc.MAC == "" || macs[ifc.MAC] {
			t.Fatalf("interface MAC must be unique+nonempty: %+v", ifc)
		}
		macs[ifc.MAC] = true
		switch ifc.Medium {
		case MediumWired:
			wired++
		case MediumWifi:
			wifi++
		default:
			t.Fatalf("interface medium must be wired|wifi: %q", ifc.Medium)
		}
	}
	if wired != 1 || wifi != 1 {
		t.Fatalf("ryzen want 1 wired + 1 wifi, got %d/%d", wired, wifi)
	}

	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := Parse(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got.Devices) != len(cfg.Devices) {
		t.Fatalf("round-trip device count: %d != %d", len(got.Devices), len(cfg.Devices))
	}
}

func TestParseEmptyIsDefault(t *testing.T) {
	cfg, err := Parse(nil)
	if err != nil {
		t.Fatalf("nil parse: %v", err)
	}
	if len(cfg.Devices) == 0 {
		t.Fatalf("empty config should fall back to Default()")
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/netmodel/` → FAIL.

- [ ] **Step 3: Implement `internal/netmodel/netmodel.go`**

```go
// Package netmodel is the portable description of the user's network: typed
// devices, each with one or more interfaces (wired/Wi-Fi) uniquely identified by
// MAC, an optional monitor flag (ping this link), and a `via` parent for the
// tier hierarchy. Persisted as JSON beside the executable.
package netmodel

import (
	"bytes"
	"encoding/json"
)

type Medium string

const (
	MediumWired Medium = "wired"
	MediumWifi  Medium = "wifi"
)

type DeviceType string

const (
	TypeInternet DeviceType = "internet"
	TypeModem    DeviceType = "modem"
	TypeRouter   DeviceType = "router"
	TypeSwitch   DeviceType = "switch"
	TypeAP       DeviceType = "ap"
	TypeNAS      DeviceType = "nas"
	TypePC       DeviceType = "pc"
	TypeOther    DeviceType = "other"
)

// Interface is one physical/logical link on a device. MAC is the unique id; an
// IP is an attribute (DHCP-churn, multi-homed reuse), never the identity.
type Interface struct {
	ID      string `json:"id"`
	Medium  Medium `json:"medium"`
	Adapter string `json:"adapter,omitempty"`
	MAC     string `json:"mac,omitempty"`
	IP      string `json:"ip,omitempty"`
	Speed   string `json:"speed,omitempty"`
	Via     string `json:"via,omitempty"`  // parent device id
	Link    string `json:"link,omitempty"` // inline annotation, e.g. "UGREEN coupler"
	SSID    string `json:"ssid,omitempty"` // wifi only
	Band    string `json:"band,omitempty"` // wifi only
	Monitor bool   `json:"monitor,omitempty"`
}

type Device struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	Type       DeviceType  `json:"type"`
	Model      string      `json:"model,omitempty"`
	Role       string      `json:"role,omitempty"`      // "core" | "access"
	Target     string      `json:"target,omitempty"`    // internet probe host
	NodeUUID   string      `json:"node_uuid,omitempty"` // agent identity (ties interfaces together)
	Agent      bool        `json:"agent,omitempty"`     // runs NetLogger (auto-discovered)
	Interfaces []Interface `json:"interfaces,omitempty"`
}

type Config struct {
	Network struct {
		Name string `json:"name"`
	} `json:"network"`
	Devices []Device `json:"devices"`
}

func (c Config) Device(id string) (Device, bool) {
	for _, d := range c.Devices {
		if d.ID == id {
			return d, true
		}
	}
	return Device{}, false
}

func Marshal(c Config) ([]byte, error) { return json.MarshalIndent(c, "", "  ") }

// Parse decodes a config; empty/blank input returns Default().
func Parse(data []byte) (Config, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return Default(), nil
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, err
	}
	if len(c.Devices) == 0 {
		return Default(), nil
	}
	return c, nil
}

// Default is the prepopulated home-LAN config used for this project.
func Default() Config {
	c := Config{}
	c.Network.Name = "Home LAN"
	c.Devices = []Device{
		{ID: "internet", Name: "Internet", Type: TypeInternet, Target: "8.8.8.8",
			Interfaces: []Interface{{ID: "net", Medium: MediumWired, IP: "8.8.8.8", Monitor: true}}},
		{ID: "modem", Name: "Modem", Type: TypeModem, Model: "Calix C5500XK",
			Interfaces: []Interface{{ID: "up", Medium: MediumWired, Via: "internet"}}},
		{ID: "router", Name: "Router", Type: TypeRouter, Model: "TP-Link BE9300",
			Interfaces: []Interface{{ID: "lan", Medium: MediumWired, IP: "192.168.0.1", Via: "modem", Monitor: true}}},
		{ID: "sw1", Name: "Switch 1", Type: TypeSwitch, Model: "Tenda TEM2010F", Role: "core",
			Interfaces: []Interface{{ID: "up", Medium: MediumWired, Speed: "2.5G", Via: "router"}}},
		{ID: "sw2", Name: "Switch 2", Type: TypeSwitch, Model: "Tenda", Role: "access",
			Interfaces: []Interface{{ID: "up", Medium: MediumWired, Speed: "2.5G", Via: "sw1"}}},
		{ID: "nas", Name: "NAS", Type: TypeNAS, Model: "QNAP TS-563",
			Interfaces: []Interface{{ID: "eth", Medium: MediumWired, Via: "sw1"}}},
		{ID: "projectorpc", Name: "ProjectorPC", Type: TypePC, Agent: true,
			Interfaces: []Interface{{ID: "eth", Medium: MediumWired, Adapter: "Intel I219-V", Speed: "1G", Via: "sw1", Link: "UGREEN coupler", Monitor: true}}},
		{ID: "ryzen", Name: "ryzen", Type: TypePC, Agent: true,
			Interfaces: []Interface{
				{ID: "eth", Medium: MediumWired, Adapter: "Killer E3100G", MAC: "2C:F0:5D:11:22:33", IP: "192.168.0.154", Speed: "2.5G", Via: "sw2", Monitor: true},
				{ID: "wifi", Medium: MediumWifi, Adapter: "Intel AX1675x", MAC: "2C:F0:5D:44:55:66", IP: "192.168.0.24", Speed: "1.4G", Via: "router", SSID: "HomeNet", Band: "5GHz"},
			}},
		{ID: "ncase", Name: "NCASE", Type: TypePC, Agent: true,
			Interfaces: []Interface{{ID: "eth", Medium: MediumWired, Adapter: "Intel I226-V", Speed: "2.5G", Via: "sw2"}}},
	}
	return c
}
```

(Fill the remaining placeholder MACs with stable dummy values so `TestRoundTripAndIdentity` sees nonempty unique MACs on ryzen — only ryzen's two are asserted; other single-NIC devices may leave MAC empty.)

- [ ] **Step 4: Verify + commit** — `go test ./internal/netmodel/`, `go vet`, `gofmt -w internal/netmodel/`, `go build ./...`; `git add internal/netmodel/ && git commit -m "feat(netmodel): config types + per-interface MAC identity + default (ui-2)"`.

## Task 2: load/save the config file (portable, beside the exe)

**Files:** Modify `internal/netmodel/netmodel.go`; add `internal/netmodel/file_test.go`.

- [ ] **Step 1: Write the failing test**

```go
package netmodel

import (
	"path/filepath"
	"testing"
)

func TestLoadSaveFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "network.json")

	// missing file → Default, no error
	cfg, err := Load(p)
	if err != nil || len(cfg.Devices) == 0 {
		t.Fatalf("missing file should load Default: %v", err)
	}
	if err := Save(p, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(p)
	if err != nil || len(got.Devices) != len(cfg.Devices) {
		t.Fatalf("reload mismatch: %v %d", err, len(got.Devices))
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/netmodel/ -run TestLoadSaveFile` → FAIL.

- [ ] **Step 3: Implement** in `netmodel.go`:

```go
import "os"

// Load reads the config at path; a missing file returns Default() with no error.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, err
	}
	return Parse(data)
}

func Save(path string, c Config) error {
	data, err := Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
```

- [ ] **Step 4: Verify + commit** — `go test ./internal/netmodel/`, `gofmt -w`, `go build ./...`; `git commit -am "feat(netmodel): Load/Save config file (ui-2)"`.

## Task 3: pure topology + monitor-target derivation

**Files:** Create `internal/netmodel/topology.go`, `internal/netmodel/topology_test.go`.

- [ ] **Step 1: Write the failing test**

```go
package netmodel

import "testing"

func TestTiers(t *testing.T) {
	tiers := Tiers(Default())
	if len(tiers) != 4 {
		t.Fatalf("want 4 tiers, got %d", len(tiers))
	}
	if tiers[0].Name != "Internet" || tiers[3].Name != "Devices" {
		t.Fatalf("tier names: %+v", []string{tiers[0].Name, tiers[3].Name})
	}
	// Router lands in Gateway; both switches in Switches; pcs+nas in Devices.
	if len(tiers[1].Devices) != 2 || len(tiers[2].Devices) != 2 || len(tiers[3].Devices) != 4 {
		t.Fatalf("tier sizes: %d/%d/%d", len(tiers[1].Devices), len(tiers[2].Devices), len(tiers[3].Devices))
	}
}

func TestMonitorTargets(t *testing.T) {
	tg := MonitorTargets(Default())
	// internet(8.8.8.8), router(.1), projectorpc(via dhcp - no ip here so skipped),
	// ryzen wired(.154). Wi-Fi (.24) is NOT monitored.
	has := func(ip string) bool {
		for _, x := range tg {
			if x.IP == ip {
				return true
			}
		}
		return false
	}
	if !has("8.8.8.8") || !has("192.168.0.1") || !has("192.168.0.154") {
		t.Fatalf("missing expected monitor target: %+v", tg)
	}
	if has("192.168.0.24") {
		t.Fatalf("unmonitored Wi-Fi must not be a target: %+v", tg)
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/netmodel/ -run 'TestTiers|TestMonitorTargets'` → FAIL.

- [ ] **Step 3: Implement `internal/netmodel/topology.go`**

```go
package netmodel

// Tier is one row of the connector-less topology.
type Tier struct {
	Name    string
	Devices []Device
}

func tierIndex(t DeviceType) int {
	switch t {
	case TypeInternet:
		return 0
	case TypeModem, TypeRouter:
		return 1
	case TypeSwitch, TypeAP:
		return 2
	default:
		return 3
	}
}

// Tiers groups devices into the four display rows, preserving config order.
func Tiers(c Config) []Tier {
	names := []string{"Internet", "Gateway", "Switches", "Devices"}
	tiers := make([]Tier, 4)
	for i, n := range names {
		tiers[i].Name = n
	}
	for _, d := range c.Devices {
		i := tierIndex(d.Type)
		tiers[i].Devices = append(tiers[i].Devices, d)
	}
	return tiers
}

// Target is a device link the engine should ping.
type Target struct {
	DeviceID string
	Name     string
	IP       string
}

// MonitorTargets returns every interface flagged monitor with a non-empty IP.
func MonitorTargets(c Config) []Target {
	var out []Target
	for _, d := range c.Devices {
		for _, ifc := range d.Interfaces {
			if ifc.Monitor && ifc.IP != "" {
				out = append(out, Target{DeviceID: d.ID, Name: d.Name, IP: ifc.IP})
			}
		}
	}
	return out
}
```

- [ ] **Step 4: Verify + commit** — `go test ./internal/netmodel/`, `gofmt -w`, `go build ./...`; `git commit -am "feat(netmodel): Tiers + MonitorTargets derivation (ui-2)"`.

## Task 4: merge auto-discovered agents into the config

**Files:** Create `internal/netmodel/discover.go`, `internal/netmodel/discover_test.go`.

- [ ] **Step 1: Write the failing test** — a discovered agent (node_uuid + host + interfaces) updates the matching device in place (matched by `node_uuid`, else by name), filling IP/MAC/speed without clobbering user-entered `Via`/`Link`/`Model`:

```go
package netmodel

import "testing"

func TestMergeAgentUpdatesInPlace(t *testing.T) {
	cfg := Default()
	agent := Agent{
		NodeUUID: "uuid-ryzen",
		Name:     "ryzen",
		Interfaces: []Interface{
			{ID: "eth", Medium: MediumWired, MAC: "2C:F0:5D:11:22:33", IP: "192.168.0.200", Speed: "2.5G"},
		},
	}
	out := MergeAgent(cfg, agent)
	d, _ := out.Device("ryzen")
	if d.NodeUUID != "uuid-ryzen" {
		t.Fatalf("node_uuid not set")
	}
	var eth Interface
	for _, ifc := range d.Interfaces {
		if ifc.MAC == "2C:F0:5D:11:22:33" {
			eth = ifc
		}
	}
	if eth.IP != "192.168.0.200" { // refreshed from discovery
		t.Fatalf("discovered IP not merged: %+v", eth)
	}
	if eth.Via != "sw2" { // user topology preserved
		t.Fatalf("user Via clobbered: %+v", eth)
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/netmodel/ -run TestMergeAgent` → FAIL.

- [ ] **Step 3: Implement `internal/netmodel/discover.go`** — `Agent{NodeUUID, Name string; Interfaces []Interface}`; `MergeAgent(Config, Agent) Config` matches a device by `NodeUUID` (fallback: case-insensitive `Name`), sets `Agent=true`, copies `NodeUUID`, and for each discovered interface (matched by MAC, else by `Medium`) refreshes `Adapter/MAC/IP/Speed/SSID/Band` while preserving the existing `Via/Link/Monitor`. Unmatched agents are appended as a new `TypePC` device with `Via` unset (user assigns later).

- [ ] **Step 4: Verify + commit** — `go test ./internal/netmodel/`, `gofmt -w`, `go build ./...`; `git commit -am "feat(netmodel): merge discovered agents, preserving user topology (ui-2)"`.

## Task 5: appcore loads config, monitors flagged devices, exposes topology

**Files:** Modify `internal/appcore/appcore.go`; add to `appcore_test.go`.

- [ ] **Step 1: Write the failing test** — with an injected `Ping` and a config whose router is `monitor:true`, the snapshot's monitored-target stats populate and `Snapshot.Topology` is non-empty:

```go
func TestSnapshotMonitorsConfigTargets(t *testing.T) {
	dir := t.TempDir()
	a := New(dir)
	a.CollectNICs = func() []nicstat.NIC { return nil }
	a.StartIperf = func(string) (func(), string) { return func() {}, "" }
	a.FetchLinks = func(string) (LinkReport, error) { return LinkReport{}, nil }
	a.FetchEvents = func(string) ([]EventInfo, error) { return nil, nil }
	pinged := map[string]bool{}
	a.Ping = func(addr string, _ time.Duration) (probe.Result, error) {
		pinged[addr] = true
		return probe.Result{RTT: time.Millisecond}, nil
	}
	a.Discovery = fakeLister{}
	a.tick = 5 * time.Millisecond
	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if pinged["192.168.0.1"] { // router, monitor:true in Default()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("monitored target never pinged; saw %+v", pinged)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(a.Snapshot().Topology) == 0 {
		t.Fatalf("Snapshot.Topology empty")
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/appcore/ -run TestSnapshotMonitorsConfigTargets` → FAIL.

- [ ] **Step 3: Implement:**
  - In `New`, set `a.netCfgPath = filepath.Join(dataDir, "network.json")` and `a.netCfg, _ = netmodel.Load(a.netCfgPath)` (add fields `netCfgPath string`, `netCfg netmodel.Config`, guarded by `a.mu`).
  - Add a `monitorLoop(ctx)` (or fold into the existing `peerLoop`): each tick, for each `netmodel.MonitorTargets(a.netCfg)` call `a.Ping(t.IP, …)` and record into `a.statFor("mon:"+t.IP)` exactly like the gateway/internet probes. `wg.Add(6)` and `go a.monitorLoop(ctx)` in `Start` if a new loop.
  - Add `Snapshot.Topology []netmodel.Tier` plus per-target health: build it from `netmodel.Tiers(a.netCfg)` and overlay each monitored device's loss from `a.statFor("mon:"+ip)`. Expose a `Snapshot.NetConfig netmodel.Config` for the editor and a `SetNetConfig(netmodel.Config)` method that saves to `a.netCfgPath` and swaps `a.netCfg` under `a.mu`.
  - On startup, `MergeAgent` self into the config (this machine's node UUID + NIC interfaces from `nicstat`) and `Save`, so the local agent self-populates.

- [ ] **Step 4: Verify** — `go test ./internal/appcore/ -run TestSnapshotMonitorsConfigTargets -v` → PASS; full `go test ./internal/appcore/`; `go test -count=2 ./internal/appcore/` (no deadlock — the config is read under `a.mu`, monitored stats use the existing `peerMu` leaf maps); `go vet`, `gofmt -w`, `go build ./...`.

- [ ] **Step 5: Commit** — `git commit -am "feat(appcore): load network config, probe monitored devices, expose Topology (ui-2)"`.

## Task 6: tiered topology view

**Files:** Create `internal/ui/topology.go`; modify `internal/ui/ui.go` (insert section).

- [ ] **Step 1: Write the failing test** (pure helpers): `viaLabel(d)` joins the device's interfaces' parents/media into a hint string, and `mediumIcon(m)` maps medium→glyph name:

```go
package ui

import (
	"testing"

	"netlogger/internal/netmodel"
)

func TestViaLabelMultiHomed(t *testing.T) {
	d := netmodel.Device{Interfaces: []netmodel.Interface{
		{Medium: netmodel.MediumWired, Via: "sw2"},
		{Medium: netmodel.MediumWifi, Via: "router"},
	}}
	got := viaLabel(d)
	if got != "wired→sw2 · wifi→router" {
		t.Fatalf("viaLabel = %q", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/ui/ -run TestViaLabel` → FAIL.

- [ ] **Step 3: Implement `topology.go`:** `viaLabel` (pure, tested) + `layoutTopology(gtx, th, snap)` rendering each `snap.Topology` tier as a row: a fixed-width `colTextSec` tier label + a wrapped flex of device cards (`card(colCard,…)`). Each card: a status dot (tier-device health → `sevColor`; infra with no monitored IP → `colTextMut`), the device name (`Sp(13)`, medium-weight), model sub (`Sp(11)`, `colTextSec`), and `viaLabel` (`Sp(10)`, `colTextMut`). Mark the local agent with a `colAccent` "this machine" chip. Use Gio's `layout.Flex` with wrapping via a simple width-accumulating helper (no external lib). Insert the section in `Run` between Adapters and the Matrix, behind a small toggle is **not** required — always shown.

- [ ] **Step 4: Verify + commit** — `go test ./internal/ui/`, `go build ./...`, `gofmt -w internal/ui/`; `git commit -am "feat(ui): connector-less tiered topology view (ui-2)"`. **Manual gate:** relaunch; topology shows Internet/Gateway/Switches/Devices rows with the real gear, medium-aware "via" labels, ryzen tagged "this machine", monitored devices colored by health.

## Task 7: equipment editor (list + per-interface form)

**Files:** Create `internal/ui/editor.go`; modify `internal/ui/ui.go` to add an "Edit network" button that swaps the main content for the editor (a `mode` field in `Run`: `modeDashboard` / `modeEditor`).

- [ ] **Step 1:** Add editor state to `Run`: `var mode int`, a `widget.List` for the device list, `widget.Editor`s for the selected device/interface fields, `widget.Clickable`s for add/save/delete/import/export and per-device list rows. An "Edit network" button in the header sets `mode = modeEditor`; a back button returns.

- [ ] **Step 2:** Implement `layoutEditor(gtx, th, &state, snap)` as the two-pane mockup: left = `+ Add device` + the device list (icon by `Type`, name, `auto` chip when `Agent`, selected-row accent); right = the selected device's fields (Name, Type dropdown, Model, Role) and a **Connections** section listing each `Interface` as a `colCardAlt` card showing medium icon (`ti-plug-connected` wired / `ti-wifi` Wi-Fi — render as glyphs/labels in Gio), Adapter, **MAC (id)**, IP, `Via` (parent picker), Speed, and for Wi-Fi the SSID/Band; each interface has its own **Monitor** toggle. `+ Add connection` appends a blank `Interface`. Save calls `a.SetNetConfig(editedCfg)`; Delete removes the device.

- [ ] **Step 3:** Keep all editing logic in pure helpers where possible: `applyEdits(cfg, deviceID, form) Config`, `addInterface(cfg, deviceID, Medium) Config`, `removeDevice(cfg, deviceID) Config` — each pure and unit-tested in `editor_test.go` (e.g., adding a Wi-Fi interface yields a second interface with `Medium==MediumWifi` and a blank MAC the user fills). Test:

```go
func TestAddInterfaceCreatesDistinctLink(t *testing.T) {
	cfg := netmodel.Default()
	out := addInterface(cfg, "ncase", netmodel.MediumWifi)
	d, _ := out.Device("ncase")
	if len(d.Interfaces) != 2 || d.Interfaces[1].Medium != netmodel.MediumWifi {
		t.Fatalf("wifi interface not added distinctly: %+v", d.Interfaces)
	}
}
```

- [ ] **Step 4: Verify + commit** — `go test ./internal/ui/`, `go build ./...`, `gofmt -w internal/ui/`; `git commit -am "feat(ui): equipment editor with per-interface wired/Wi-Fi (ui-2)"`. **Manual gate:** open the editor; add a switch, set its parent, toggle Monitor; add a Wi-Fi connection to a PC; Save; confirm `network.json` beside the exe updates and the topology re-renders.

## Task 8: import / export the config file

**Files:** Modify `internal/ui/editor.go`; add `internal/ui/portio_test.go` if logic warrants.

- [ ] **Step 1:** Export: write `netmodel.Marshal(cfg)` to `network-export-<unix>.json` beside the exe (reuse the `os.Executable` dir pattern from `Export`); set the status message to the filename. Import: read a `network.json` the user dropped beside the exe (a fixed path `network-import.json`), `netmodel.Parse` it, and on success `a.SetNetConfig`. (A native file picker is out of scope — keep it file-drop-by-convention, matching the portable, no-dialogs ethos.)

- [ ] **Step 2: Verify + commit** — `go build ./...`, `gofmt -w`; `git commit -am "feat(ui): config import/export beside the exe (ui-2)"`. **Manual gate:** Export writes a readable JSON; editing it and Importing reflects the change. **UI-2 ships here.**

---

# Milestone UI-3 — time-series depth (latency bands + 12h heatmap)

Add the jitter envelope and bucketed-loss query, then render the SmokePing band and the heatmap. **Depends on UI-1; independent of UI-2.**

## Task 1: per-poll jitter envelope ring

**Files:** Modify `internal/appcore/history.go` (or add `internal/appcore/envelope.go`); test alongside.

- [ ] **Step 1: Write the failing test** — a `bandRing` stores `(min,med,max)` triples and `values()` returns them oldest-first, capped:

```go
func TestBandRing(t *testing.T) {
	r := newBandRing(3)
	r.push(1, 2, 4)
	r.push(2, 3, 5)
	r.push(3, 4, 6)
	r.push(4, 5, 7) // evicts the first
	v := r.values()
	if len(v) != 3 || v[0].Med != 3 || v[2].Max != 7 {
		t.Fatalf("band ring wrong: %+v", v)
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/appcore/ -run TestBandRing` → FAIL.

- [ ] **Step 3: Implement** a `bandRing` mirroring `histRing` (same lock + in-place `reset`), holding `type band struct{ Min, Med, Max float64 }`. In the per-peer/UDP loop, where RTT samples accumulate, also push `(minRTT, avgRTT, maxRTT)` over the poll window into a per-peer `bandRing`; expose it in `PeerInfo.RTTBand []band` (and clear in `ResetSession` in place, like the other rings).

- [ ] **Step 4: Verify + commit** — `go test ./internal/appcore/`, `go test -count=2 ./internal/appcore/`, `gofmt -w`, `go build ./...`; `git commit -am "feat(appcore): per-peer RTT jitter envelope (min/med/max) (ui-3)"`.

## Task 2: SmokePing-style band rendering

**Files:** Create `internal/ui/timeseries.go`; modify `internal/ui/ui.go` (peer block uses the band when present).

- [ ] **Step 1:** Implement `latencyBand(gtx, band []band, line, fill color.NRGBA, w, h int)` — a closed path of the max envelope (upper) then the min envelope (lower, reversed) filled with `fill` (low-alpha), and a polyline of `Med` in `line`. Reuse the existing `normalize` from `sparkline.go`. Pure geometry — add a `normalizeBands` test:

```go
func TestNormalizeBandsClamps(t *testing.T) {
	pts := normalizeBands([]appcore.Band{{Min: 0, Med: 1, Max: 2}, {Min: 1, Med: 2, Max: 4}}, 100, 40)
	if len(pts) != 2 || pts[1].max < pts[1].min {
		t.Fatalf("band normalize wrong: %+v", pts)
	}
}
```

- [ ] **Step 2:** In `peerBlock`, when `p.RTTBand` is non-empty, replace the plain RTT sparkline with `latencyBand(...)` (blue line/fill); keep the loss sparkline as-is.

- [ ] **Step 3: Verify + commit** — `go test ./internal/ui/`, `go build ./...`, `gofmt -w`; `git commit -am "feat(ui): SmokePing-style latency band (ui-3)"`. **Manual gate:** under jitter, the band widens around the median.

## Task 3: bucketed loss query for the heatmap

**Files:** Modify `internal/store/store.go`; test in `store_test.go`.

- [ ] **Step 1: Write the failing test** — `LossBuckets(sinceUnix, bucketSec)` returns per-target, per-bucket loss% from the persisted `udp_iso` / connectivity rows:

```go
func TestLossBuckets(t *testing.T) {
	st := openTempStore(t) // existing test helper
	// insert two samples in the same bucket: one lost, one ok, for target "ryzen"
	base := int64(1_000_000)
	st.InsertSample(base, "ryzen", "udp_iso", 0, 0, true)   // lost
	st.InsertSample(base+1, "ryzen", "udp_iso", 1.0, 0, false) // ok
	b, err := st.LossBuckets(base-10, 60)
	if err != nil {
		t.Fatalf("LossBuckets: %v", err)
	}
	if got := b["ryzen"][0]; got < 49 || got > 51 {
		t.Fatalf("expected ~50%% loss in bucket, got %v", got)
	}
}
```

(Adjust `InsertSample` to the store's actual sample signature; the point is one lost + one ok ⇒ 50%.)

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/store/ -run TestLossBuckets` → FAIL.

- [ ] **Step 3: Implement** `LossBuckets(sinceUnix int64, bucketSec int) (map[string][]float64, error)` — a single SQL `GROUP BY target, (ts/bucket)` computing `100.0*sum(lost)/count(*)` per bucket, returned as dense per-target slices (zero-filled gaps). Index on `ts` already exists (WAL pragma task); add `target` to the index if missing.

- [ ] **Step 4: Verify + commit** — `go test ./internal/store/`, `gofmt -w`, `go build ./...`; `git commit -am "feat(store): LossBuckets query for the heatmap (ui-3)"`.

## Task 4: 12-hour heatmap rendering

**Files:** Modify `internal/ui/timeseries.go`; expose buckets via `Snapshot`.

- [ ] **Step 1:** Add `Snapshot.LossHeat map[string][]float64` (queried in `Snapshot` via `a.store.LossBuckets(now-12h, 30min)` — guard the store call outside `a.mu`, tolerate nil store in tests). Implement `layoutHeatmap(gtx, th, snap)` — a row per target (label + a strip of `sevColor`-filled rects, one per bucket) with a few time-axis labels and a `green/amber/red` legend. Add the section under the Matrix.

- [ ] **Step 2:** Pure test for the cell→color mapping reusing `sevColor` (already covered) and a `heatRows(snap)` ordering helper (gateway, internet, then peers) with a small test asserting order.

- [ ] **Step 3: Verify + commit** — `go test ./internal/ui/`, `go build ./...`, `gofmt -w`; `git commit -am "feat(ui): 12-hour loss heatmap (ui-3)"`. **Manual gate:** after a multi-hour run, the heatmap shows green with red where outages occurred. **UI-3 ships here.**

---

## Build & manual verification (all milestones)

- Build the portable exe after each milestone: `powershell -ExecutionPolicy Bypass -File scripts/build-app.ps1` → `bin/NetLogger.exe (build <hash>)`.
- Deploy the **same** build to every machine (the build-skew banner will flag mismatches).
- UI-2 gate specifically: confirm `network.json` is created beside the exe on first run, the local machine self-populates via `MergeAgent`, and a multi-homed host (ryzen) shows two interfaces with distinct MACs and media in both the topology and the editor.

---

## Self-Review

**Spec coverage:**
- Dark re-skin + spacing + cards + status hero + KPI tiles + event-feed polish → UI-1 Tasks 1–4. ✓
- Connector-less tiered topology, config-driven → UI-2 Tasks 1,3,6. ✓
- Equipment editor (list + per-interface form, add/delete, import/export) → UI-2 Tasks 7–8. ✓
- **Wi-Fi vs LAN uniquely identified** → `Interface{Medium, MAC}` with MAC as the unique id; asserted in UI-2 Task 1 (`TestRoundTripAndIdentity`) and rendered per-interface in Tasks 6–7. ✓
- Monitor-any-device (probe targets) → UI-2 Tasks 3,5 (`MonitorTargets`, `monitorLoop`). ✓
- Auto-discovered vs manual + prefill → UI-2 Tasks 4,5 (`MergeAgent`, self-populate). ✓
- Prepopulated default config → UI-2 Task 1 (`Default()`). ✓
- Latency bands + 12h heatmap → UI-3 Tasks 1–4. ✓

**Placeholder scan:** Backend/pure logic (netmodel, topology, monitor, merge, band ring, loss buckets, editor mutators) is fully specified with test code. Gio composition tasks (UI-1 T2–4, UI-2 T6–7, UI-3 T2,4) give a concrete layout spec + manual gate rather than line-by-line immediate-mode code — the established pattern in this repo's prior UI plans, with all their testable logic (`kpis`, `viaLabel`, `applyEdits`, `addInterface`, `normalizeBands`, `heatRows`) extracted and unit-tested. The one MAC literal to finish (other devices' dummy MACs) is called out explicitly in UI-2 Task 1 Step 3.

**Type consistency:** `netmodel.{Config,Device,Interface,Medium,DeviceType,Tier,Target,Agent}`, `Marshal/Parse/Load/Save/Default/Device(id)/Tiers/MonitorTargets/MergeAgent` are used identically across tasks. `appcore.Snapshot.{Topology []netmodel.Tier, NetConfig netmodel.Config, LossHeat, …}`, `appcore.SetNetConfig`, `PeerInfo.RTTBand []band`. UI helpers `kpis/kpiTile/layoutKPIs`, `viaLabel`, `latencyBand/normalizeBands`, `layoutHeatmap/heatRows`, `card/gap/darkTheme`, palette `col*`. `Band`/`band` triple `{Min,Med,Max}` consistent between appcore and ui (ui imports `appcore.Band` — export it as `Band`).

---

## Execution — ON HOLD

Per the user's instruction, implementation is **not** to begin yet. When the user gives the go-ahead, offer the two execution options from the writing-plans skill (subagent-driven, recommended; or inline executing-plans) and start at **UI-1 Task 1**.
