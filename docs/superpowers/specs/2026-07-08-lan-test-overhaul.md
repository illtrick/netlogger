# LAN Speed & Stress Overhaul — live intervals, retransmits, honest units

Status: implementing (2026-07-08). Follows the Tests subsystem spec
(2026-06-18); this revises its Speed/Stress presentation and engine plumbing.

## Why

1. **Bug — loopback self-test.** `Snapshot.SelfPeer.Addr` is `127.0.0.1:8088`;
   the sweep hands that to REMOTE clients as the target, so every remote→self
   cell measured the remote's own loopback (~20–120 Gbit/s). Fix: advertise the
   primary LAN IP as self's address; refuse loopback targets in the speed
   handler and stress target sanitizer (defense in depth).
2. **Units.** Matrix cells showed bare numbers ("2372"). All rates now render
   via one formatter: `< 1 Gbit → "946 Mb/s"`, `≥ 1 Gbit → "2.37 Gb/s"`.
3. **Overhaul.** The features users consistently praise in iperf3/jperf:
   live per-second throughput (jperf's graph), TCP retransmit visibility,
   parallel streams, JSON intervals for graphing, rate-capped runs. We bundle
   iperf3 3.21, whose `--json-stream` (≥3.17) emits one NDJSON event per
   interval — verified against the bundled binary (schema captured below).

## What ships

### Engine (`internal/iperf`)
- `RunClientStream(ctx, target, opts, onInterval)` — runs with `--json-stream`,
  parses `{"event":"interval","data":{"sum":{…}}}` lines live, assembles the
  final `Result` from the `end` event. **Fallback:** if the binary predates
  `--json-stream` (errors with no events), rerun once with plain `--json`.
- Interval fields stay optional: Windows/Cygwin iperf3 emits no
  `retransmits`/`rtt` in TCP sums (no TCP_INFO); macOS/Linux do.

### Wire (`internal/appcore`)
- `SpeedReq.Stream bool` — when set, `/api/speedtest` responds with NDJSON:
  `{"live":{sec,mbit,retr,phase}}` lines then one `{"result":{…}}`. Old peers
  ignore the field and return a single JSON object; the client accepts both
  (envelope-or-bare decode). Additive → still 1.x-compatible, but this is a
  feature release: **v1.2.0**.
- `SpeedResult` gains `DownIvs`/`UpIvs []float64` (per-second Mbit) so the
  detail panel can chart a completed run; retransmit totals already existed.
- `SweepProgress.Live map[pairKey]LivePoint` — the matrix shows the current
  rate in the actively-testing cell, updating every second (the jperf graph,
  as a live-filling matrix).
- Stress: `loadTarget` switches to the streaming runner; `LinkLoad` gains
  live achieved rate, cumulative retransmits, and a 60-slot rate history for
  a per-link throughput chart next to the existing latency-under-load chart.

### UI (`internal/ui`)
- One rate formatter everywhere (matrix cells, sub-lines, detail, history,
  stress bars): Mb/s below 1 Gbit, Gb/s with 2 decimals above.
- Sweep controls (plain copy, segmented): direction Both/Down/Up/Bidir,
  duration 10s/30s, streams 1/4/8. Runs omit the first second (`-O 1`) to
  skip TCP slow-start.
- Active matrix cell shows the live rate; completed-pair detail shows a
  per-second throughput chart (down + up) plus retransmits.
- Stress links: live rate against cap, retransmit count, per-link throughput
  sparkline (shared scale).

### Non-goals
- UDP mode toggle for the sweep: the continuous 200 Hz UDP probes + stress
  already measure loss/jitter under load; a UDP iperf sweep duplicates that.
- Packet-pacing/QoS test modes; per-stream breakdowns (sum only).

## Captured `--json-stream` schema (bundled iperf 3.21, Windows)

```
{"event":"start","data":{…}}
{"event":"interval","data":{"streams":[…],"sum":{"start":0,"end":1.0,"seconds":1.0,"bytes":N,"bits_per_second":R,"omitted":false,"sender":true}}}
{"event":"end","data":{"streams":[…],"sum_sent":{…},"sum_received":{…}}}
```

`end.data` is the normal JSON's `end` object. Interval sums add
`retransmits`/`rtt` on platforms with TCP_INFO, `jitter_ms`/`lost_percent`
for UDP. Unknown events are skipped.

## Compatibility

Mixed 1.1/1.2 meshes: new orchestrator + old peer degrades to non-streamed
(final-only) results per pair; old orchestrator + new peer unchanged. The
version-mismatch banner (correctly) nudges full rollout. macOS needs brew
iperf3 ≥ 3.17 for live streaming; older falls back automatically.
