# Tests UX — research-driven redesign proposals

Status: proposal (2026-07-08, against v1.2.0). Method: per-feature visual study
of live screenshots → published user feedback on comparable tools → proposals.
Everything here respects the standing copy rule: plain, factual labels — no
verdict banners, no editorializing. Grades and thresholds are shown as data
with a factual scale legend, never prose judgment.

---

## Cross-cutting (applies to all three sub-views)

**Study findings**

- **No chart in the app has a scale.** All sparklines normalize min→max with
  no axis labels, so a 1 ms wiggle renders identically to an 80 ms spike; at
  idle the stress "latency under load" chart shows dramatic cliffs that are
  actually sub-millisecond ambient noise. Dashboard literature (Few's
  sparkline-scaling paper, IBCS) flags exactly this: unlabeled, independently
  scaled minicharts make unequal changes look identical.
- **Units are inconsistent**: matrix says `Mb/s`, internet cards say `Mbit/s`.
- **Series colors collide with severity colors**: `linkColors` assigns
  `colBad` red to series #1 — a healthy node draws in the palette's alarm
  color. Validated replacement (all six checks pass on our dark surface,
  protan ΔE 58): `#4f8ff7` blue / `#c67718` orange / `#0bab9e` teal; red and
  amber reserved for severity only.
- **The three sub-views share no skeleton**: run-button placement, status
  captions, provenance format, and history rows differ per tab.

**Proposals**

- **X1 — Scale every chart**: min/max labels (throughput charts zero-based),
  last-value annotation, shared scale across series (already true) stated in
  the caption. Small mono captions, plain.
- **X2 — One unit format** (`fmtRate` everywhere, including internet cards).
- **X3 — Validated series palette**, severity colors reserved.
- **X4 — Shared test shell**: every sub-view = header (title · status line ·
  config chips · Run/Stop right-aligned) + body + Recent history with config
  chips. One skeleton, three bodies.

---

## Speed (LAN)

**Study findings**

1. Reading a cell requires four facts (row=client, col=server, big=down,
   ↑=up) keyed by an 11px footnote; the ↓/↑ glyphs are never defined.
2. In "both" mode the matrix nearly duplicates itself; the real diagnostic —
   direction asymmetry (777 vs 942 on the same link) — requires unaided
   eye-jumps across the diagonal.
3. Severity grades absolute Mb/s against a 1 GbE assumption: 777 on a 1 Gb
   NIC ambers at 78% of link, while a 2.5 Gb link degraded to 940 would show
   green. We already collect per-NIC link speed.
4. Pair-detail chart: no scale; in bidir the headline shows ↓ but the chart
   plots only ↑ (headline/chart mismatch).
5. History rows don't record run config — a bidir/8-stream/30s row (442)
   sits beside a both/1-stream/10s row (777) looking like degradation.

**Research**

- iperf3's own tracker documents the sender/receiver ambiguity as its
  longest-standing usability complaint (esnet/iperf #480: users ask for
  explicit "client sending / server receiving"); GUI threads (#1521, #1194)
  ask for "simple graphs and easily human-readable summaries."
- fast.com is praised for leading with one number; Speedtest's labeled
  separate readouts are valued *because each is labeled*. Hierarchy + labels,
  not density.
- Sparkline-scaling criticism as above.

**Proposals**

- **P1 — Directional cells**: cell [row, col] = data flowing row→column, one
  number. "Both" fills the two mirror cells instead of packing two directions
  into one. `▲` marks a cell meaningfully slower (>20%) than its reverse.
  Footnote: "cell = data flowing from row to column."
- **P2 — Grade % of link**: severity = rate ÷ min(endpoint link speeds), from
  NIC data: ≥85% good · 50–85% watch · <50% bad; sub-line "78% of 1 Gb link";
  falls back to absolute thresholds when link speed unknown.
- **P3 — Detail chart honesty**: this-direction (solid) + reverse (dashed),
  scale labels, last value; headline always matches plotted series.
- **P4 — Config chips on history rows** ("both · 10s · 4 streams") and on the
  completed-matrix header.
- **P5 — Live cell keeps v1.2.0 streaming**, unchanged.

---

## Stress

**Study findings**

1. **Idle screen lies**: "Latency under load · last minute" renders ambient
   RTT (normalized, unscaled) with alarming spikes while nothing runs.
2. **A run leaves no artifact.** Speed and Internet persist history; a stress
   run ends and evaporates — no summary, no history row, nothing to compare
   against the previous run. The only trace is heatmap events.
3. No duration control (engine supports it); cap value appears twice
   (stepper + config chip).
4. Charts unscaled; series palette collides with severity (red line = node,
   not fault).
5. Throughput and latency charts (v1.2.0) are adjacent but not visibly
   time-aligned — the whole diagnostic is "same second" correlation.

**Research**

- **Flent/RRUL is the reference presentation**: three vertically stacked,
  time-synchronized plots — download, upload, latency on one shared time
  axis — so engineers see latency spike at the exact second throughput
  saturates ("visually quantify the impact of congestion"; the 60-second RRUL
  run is called the gold standard). Sources: bufferbloat.net RRUL chart
  explanation/spec, flent.org, netbeez.net, tohojo.dk.
- **PingPlotter's most-loved trait is the continuous timeline you can
  correlate with real-world events** and walk back to where a pattern starts
  (pingplotter.com manual/wisdom pages). NetLogger already owns this shape —
  the heatmap — so a stress run should anchor into it, not float beside it.

**Proposals**

- **S1 — RRUL-style panel**: stack throughput-per-link and latency on one
  shared, labeled time axis (run window), start/end tick marks, y-scales
  (`0–cap Mb/s`, `0–max ms`). One glance answers "did latency move when load
  arrived?"
- **S2 — Post-run summary + history**: persist a `stress` row in
  test_results — duration, links loaded, per-node worst added latency
  (loaded − idle median), aborts. Recent list like the other tabs, config
  chips included ("200 Mb/s cap · 10 min · TCP"). Summary line after the run:
  "10:00 · 6 links · worst +38 ms on ryzen · 0 aborts".
- **S3 — Duration control** (1m default / 5m / 10m) beside the cap; drop the
  duplicated cap chip.
- **S4 — Idle = quiet**: no charts at idle; a single "last run 11:58 ·
  6 links · no aborts" line until the next run.
- **S5 — Validated series palette** (X3); red only for aborted links.

---

## Internet

**Study findings**

1. Cleanest screen. Remaining: `Mbit/s` unit mismatch; the phase strip
   becomes four green checkmarks of noise after completion; `jitter 0 ms ·
   loss 0.0%` reports measurements the test doesn't make (phantom zeros);
   idle/loaded/delta — one fact — is scattered across three homes; results
   don't name the node that measured them; node-picker selection state is
   ambiguous.
2. A single run is presented with no context about run-to-run variance.

**Research**

- **Every credible guide says single runs are noise**: run ~3 back-to-back
  and take the **median**, look for patterns not peaks (gokinetic.com,
  testmyspeed.com, internetspeedtest.net, checkmyspeed.org).
- **Scheduled testing with trends is the ISP-accountability feature** users
  self-host whole stacks for (Speedtest Tracker, testmy.net/auto, Netdata
  speedtest monitoring): "ISPs take complaints seriously when presented with
  results collected over time rather than single screenshots." NetLogger is
  already an always-on resident app — it is uniquely positioned to own this.
- Waveform's grade is trusted because the scale is stated (A <5 ms … per its
  thresholds); a factual scale legend beside our grade achieves the same
  without editorial copy.

**Proposals**

- **I1 — One latency story**: idle → loaded → +Δ → grade as a single strip,
  grade scale legend beside it ("A <30 · B <60 · C <100 · D <200" — our
  existing thresholds), phantom jitter/loss dropped until actually measured.
- **I2 — Median of 3**: a "Run ×3" action (sequential runs, ~90 s); the
  stored/displayed result is the median, labeled "median of 3"; individual
  runs land in history.
- **I3 — Scheduled tests (roadmap)**: opt-in interval (e.g. every 6/12/24 h)
  running the pinned-server test from this node; results into the existing
  history + a trend sparkline (scaled, X1) over the last N results; already
  exported in the bundle. This converts the tab from one-shot to monitoring
  and matches the product thesis (value compounds while it runs).
- **I4 — State honesty**: phase strip only while running; provenance names
  the node ("measured 11:56 · on ryzen · Los Angeles (Clouvider)"); node
  picker uses the same selected-chip style as the endpoint dropdown.
- **I5 — Units** via X2.

---

## Suggested order (impact ÷ effort)

| # | Item | Size |
|---|------|------|
| 1 | X1 scale labels + X2 units + X3 palette | S |
| 2 | P1+P2 directional cells + %-of-link grading | M (cell semantics + NIC lookup) |
| 3 | S2 stress history row + post-run summary | S–M (store kind "stress") |
| 4 | S1 RRUL panel + S3 duration + S4 quiet idle | M |
| 5 | P3/P4, I1/I4 detail+provenance polish | S |
| 6 | I2 median of 3 | S |
| 7 | T/X4 shared shell refactor | M |
| 8 | I3 scheduled internet tests | M–L (roadmap) |

## Sources

- iperf usability: github.com/esnet/iperf issues #480, #1194, discussion #1521
- Speed-test hierarchy: guidingtech.com, nucleusnetwork.com (fast.com vs Speedtest)
- Bufferbloat grading: bufferspeed.com/compare/waveform, makeuseof.com,
  bufferbloat.net/projects/bloat/wiki/Tests_for_Bufferbloat
- RRUL presentation: bufferbloat.net RRUL_Chart_Explanation + RRUL_Spec,
  flent.org/intro, netbeez.net/blog/flent, blog.tohojo.dk (Story of Flent)
- Timeline correlation: pingplotter.com/wisdom/common-network-problems,
  pingplotter.com manual (interpreting graphs)
- Run variability: gokinetic.com, testmyspeed.com, internetspeedtest.net,
  checkmyspeed.org
- Scheduled trends: xda-developers.com (Speedtest Tracker), testmy.net/auto,
  netdata.cloud/blog/speedtest-monitoring, akashrajpurohit.com
- Sparkline scaling: perceptualedge.com (Few), graphomate.com (IBCS/Hichert)
