# NetLogger UI design guide

A small, enforced visual system for the Gio app. The goal: a dense "ops dashboard"
where a glance tells you what a control *does* — navigation, a primary action, a
destructive action, or a minor utility — without reading the label.

## Principles

1. **One primary action per view.** Only the main call-to-action is a filled accent
   button. If everything is filled, nothing stands out.
2. **Plain, factual copy.** Labels are terse nouns/verbs. No editorial commentary,
   no verdict banners. Numbers and severity colors carry the meaning.
3. **Color encodes severity, not decoration.** Green = healthy, amber = watch,
   red = bad/destructive, blue = accent/primary. Never use a severity color for
   a neutral element.
4. **Match the surrounding code.** Reuse the helpers below; never hand-roll a
   one-off button or hard-code a hex twice (palette lives in `theme.go`).

## Palette (theme.go — single source of truth)

| Token | Role |
|---|---|
| `colBg` / `colTitleBar` / `colCard` / `colCardAlt` | surfaces, darkest → lightest |
| `colBorder` (faint) / `colOutline` (visible) | hairlines, outlined controls |
| `colTextPri` / `colTextSec` / `colTextMut` | text: primary, secondary, muted |
| `colAccent` (blue) | accent, primary actions, active nav |
| `colGood` / `colWatch` / `colBad` | severity: green / amber / red |

## App chrome

- **Title bar** (`chrome.go` · `titleBar`): a fixed full-width bar on `colTitleBar`
  with a hairline bottom border. Left → the **NetLogger** wordmark (`Net` in
  `colTextPri`, `Logger` in `colAccent`). Center → the primary **nav pills**. Right →
  a muted status string `"<host> · N nodes online"`. The bar gives the nav context,
  so the tabs read as navigation, not actions.
- The title bar is **outside** the scrolling content list (it doesn't scroll).

## Nav vs. actions

- **Nav pills** (`navPill`): the active tab is a raised surface (`colCard`) with
  `colTextPri`; inactive tabs are plain `colTextSec` text. A raised-but-neutral
  pill — never the bright accent — so it's distinct from a primary action.
- **Sub-view segmented control** (`segControl`): for Speed / Stress / Internet
  inside the Tests tab. Disabled/not-yet-built segments render muted and inert.

## Status banner (`statusBanner`)

For a live, ongoing operation (stress running): a full-width tinted strip — pulsing
status dot, a short bold state ("Stress running"), a muted descriptor
("full mesh · N link-pairs"), an elapsed/total **timer** right-aligned, and the
primary control (a `dangerBtn` Stop / kill-switch) at the far right. Tint matches
severity (running load → red-tinted `colBad` at low alpha).

## Chips, metric cards, badges

- **Config chips** (`configChips` over `chipLabel`): small rounded pills stating fixed
  run parameters ("Topology · full mesh", "Per-link cap · 200 Mbit/s"). *Implemented.*
- **Legend** (`matrixLegend`): a row of `color swatch + range` items under any
  severity-colored grid. *Implemented (matrix).*
- **Metric cards** (`metricCardChild`): a `colCardAlt` tile — muted label on top, a
  large value + small unit below. Flexed in a row of 4 (Internet down/up/idle/loaded).
- **Grade badge** (`gradeBadge`): a large A–F letter in a tinted rounded square
  (severity-colored via `gradeColor`) beside the numeric basis (+ms under load, RPM).
- **Phase strip** (`phaseStrip`): equal-width green-outlined chips for the test
  phases (idle ping · download · upload · loaded ping). *All implemented (Internet tab).*

## Tables & grids

- Column headers and row labels are `colTextSec` `Caption`. Directional headers get a
  `↓`/`→` glyph. Below the grid: a legend and a one-line key
  ("row = client → col = server"). Cells may carry a small second line (RTT, "slow",
  loss %) under the primary value.

## Button hierarchy (`buttons.go`)

| Helper | Looks like | Use for |
|---|---|---|
| `navTabBtn` | flat label + accent underline when active | top-level nav (Dashboard / Tests / Events) |
| `segControl` | grouped pill switch | mutually-exclusive sub-views (Speed / Stress) |
| `primaryBtn` | filled accent, dark text | the one main action (Run all pairs, Start) |
| `dangerBtn` | filled red, dark text | destructive / stop (Stop) |
| `dangerGhostBtn` | red outline, red text | less-frequent destructive (Reset mesh) |
| `ghostBtn` | neutral outline, secondary text | minor utilities (Export, Sleep, zoom ±, Now) |

Rules:
- **Never** style a nav item as a filled button, or an action as a nav underline.
- A destructive action is **always** red (filled when prominent/immediate like Stop,
  outlined when occasional like Reset).
- Ghost buttons are the default for anything that isn't the primary action.

## Typography & spacing

- Heading sizes follow `material` defaults; section titles use `Body1`, secondary
  text uses `Caption`/`Body2` in `colTextSec`/`colTextMut`.
- Card padding 16dp (`card`), inter-card gap 12dp, section gap inside a card 8–14dp.
- Buttons: primary/danger 9dp vertical / 16dp horizontal inset; ghost/small
  6dp / 12dp. Corner radius 8dp.

## Severity coloring

- Loss/throughput cells and dots use `colGood`/`colWatch`/`colBad` via the shared
  `sevColor` / `matrixCellColor` / `stressHealthColor` helpers — never inline a hex.

## Device naming & identity

A device has two identifiers with different jobs: the **common name** (hostname,
e.g. `ProjectorPC`) is what a human recognizes; the **IP** (e.g. `192.168.0.127`)
is the precise address. Lead with the name, demote the IP.

- **Primary = the common name**, in the largest/most-readable weight of the context
  (`Body2`+ in `colTextPri`). It's the label the operator scans for.
- **Secondary = the IP**, smaller (`~11sp`) and muted (`colTextMut`), shown *when
  space allows* — beneath the name in a list row, or omitted in tight chips.
- When only an address is known (unresolved peer, raw target), the **IP stands
  alone as the primary** — never show a blank name or a redundant `IP / IP`.
- Resolve names centrally via `Snapshot.DeviceName(hostOrIP)` (maps a bare
  host/IP to its device name, or passes the input through unchanged). Render with
  the shared `deviceLabel(gtx, th, name, ip)` helper — don't format identity
  strings ad-hoc at call sites.

This applies everywhere a device appears: the test matrix, the stress per-link
list, the peers table, and event rows.

## Interaction feedback (HIG basics)

- **Every clickable element shows a pointing-hand cursor** on hover — wrap custom
  controls in `hoverCursor`. The built-in button helpers already do this.
- **A busy action must look inert**: render in-flight primaries with `busyBtn`
  (muted surface + text, no pointer cursor) — "Running…" must never look clickable.
- **Scroll position is per-tab** (`tabLists`); switching tabs never inherits a
  stale offset.

## Window & layout

- **Content column caps at 1080dp** and centers on wider windows — cards and grids
  must not stretch edge-to-edge on a maximized window.
- **Minimum window size 760×520** so the layout can't squish into overlap.
- **Tray mode**: the title bar's Tray control hides the window while monitoring
  continues; the notification-area icon (the exe's own icon) reopens on click and
  offers Open / Quit on right-click.

## When adding a control

Pick the helper whose role matches the *consequence* of the click, not its
appearance. If none fits, add a new helper to `buttons.go` and a row to this table —
don't fork a bespoke style at the call site.
