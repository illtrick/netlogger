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

## When adding a control

Pick the helper whose role matches the *consequence* of the click, not its
appearance. If none fits, add a new helper to `buttons.go` and a row to this table —
don't fork a bespoke style at the call site.
