# Debugging stress-test aborts — history + method

Handoff note for whoever is chasing stress aborts (written 2026-07-08 after
fixing two distinct abort bugs on the Windows side). **Read the abort
signature before touching code** — both past bugs looked identical at first
glance ("lots of aborts") and had completely different causes, told apart by
*which* links died and *how fast*.

## How aborts work (mechanics you need first)

- Each node runs one iperf3 client goroutine per target (`loadTarget`,
  internal/appcore/stress.go), in 5-second chunks.
- A chunk that errors increments a consecutive-error counter; **3 consecutive
  errors abort the link** (`stressAbortAfter`). Success resets the counter.
- So abort SPEED tells you the error type:
  - **Aborts within ~1–2 seconds of start** → the client is getting an
    *active, instant* error (connection refused, or iperf3's "the server is
    busy running a test"). Three retries burn in under a second.
  - **Links sit at 0 Mb/s with green dots, abort after ~1–2 minutes** → the
    client is *timing out* — SYNs silently dropped. That's a firewall, not a
    busy server (refusal is instant; a drop waits out the OS connect timeout,
    ~20 s per attempt).

## Bug #1 (fixed in v1.3.1): iperf3 serves ONE test at a time

**Signature:** exactly one inbound link per target aborted almost instantly;
the other inbound link ran fine. First appeared the moment the mesh grew from
2 nodes to 3.

**Cause:** every node has a single always-on `iperf3 -s` on 5201. Full mesh
with N≥3 sends each target two simultaneous clients; iperf3 accepts one test
at a time, so the loser gets "server is busy" instantly → 3-strike abort.

**Fix:** per-link port assignment. The orchestrator (`meshAssignments`) gives
each target's inbound clients distinct ports (5201, 5202, …, deterministic by
node-ID sort); each node spawns ephemeral extra `iperf3 -s -p 520N` listeners
for the run's duration (`StressOpts.TargetPorts` / `ListenPorts`).
**Gate:** `portsSupported` — multi-port engages only when EVERY node reports
the same release. A mixed-version mesh silently falls back to legacy
all-on-5201, which brings these collision aborts back **by design** (better
than dialing ports an old node never opened).

→ So the first check on any abort report: **do all nodes run the same
version?** The header banner tells you. One stale node = legacy mode =
expected aborts.

## Bug #2 (fixed in v1.3.2, Windows-specific mechanics): firewall killed a port

**Signature:** one set of links (all on the same server port) at 0 Mb/s with
healthy green dots, timing out slowly, aborting late in the run. The other
port's links ran at cap. No errors in any log — silence is the tell.

**Cause:** the Windows firewall helper used ONE shared rule name with
delete-then-add; opening the run's extra port DELETED the 5201 allow on every
node. Program-scoped rules existed but pointed at stale binary paths, so the
port rule was load-bearing. Diagnosed by dumping the live firewall state
after a run and finding the rule holding only the last port opened.

**Fix (v1.3.2, then hardened in v1.3.4):** per-port rule names, and no
`netsh delete rule` calls at all (check-then-add / set-in-place).

## If the Mac is aborting now — ordered checklist

1. **Version skew first.** All nodes on the same release? If not, you're in
   legacy single-port mode and bug-#1 collisions are expected until the mesh
   is uniform. This is the most likely cause and needs no code.
2. **Read the signature.** Instant aborts → refusal/busy (missing listener,
   version skew). Slow 0-rate aborts → silent drop (macOS firewall/ALF).
3. **Are the extra listeners actually up during a run?** On the Mac, while a
   stress runs: `ps aux | grep 'iperf3 -s'` and
   `lsof -nP -iTCP -sTCP:LISTEN | grep iperf3` — expect the always-on 5201
   plus `-p 5202`(+) for the run. Missing extras → `iperf.StartServer`
   returned nil (brew iperf3 not resolving? Finder PATH?) — check
   `netlogger.log`.
4. **Probe a failing port by hand** from a peer of the aborting link:
   `iperf3 -c <target-ip> -p 5202 -t 2`. Instant "connection refused" = no
   listener; instant "server is busy" = port collision (assignment bug or
   legacy mode); hang-then-timeout = firewall drop.
5. **macOS ALF** (the bug-#2 analogue): the extra listeners are the same brew
   iperf3 binary, so the user's one-time "Allow incoming connections" should
   cover all ports — but verify: System Settings → Network → Firewall, or
   `/usr/libexec/ApplicationFirewall/socketfilterfw --listapps | grep -i iperf`.
   A Deny click, block-all mode, or a brew upgrade (new binary ⇒ new prompt,
   possibly appearing mid-run and stalling the listener) all produce the
   silent-drop signature. Note darwin's `ensureFirewallPort`/`Program` are
   deliberate no-ops — ALF is per-binary, prompt-driven.
6. **Check who is aborting whom.** Rows are `source → target` since v1.3.3.
   All aborted links sharing one TARGET → that machine's listener/firewall.
   Sharing one SOURCE → that machine's client side (iperf3 binary, outbound
   rules). Spread evenly, one per target → port-collision pattern (see #1).

## Relevant history

`git log --oneline` anchors: `edbc99f` (v1.3.1 per-link ports),
`4ca38b3` (v1.3.2 firewall clobber), `007a068` (v1.3.4 no-delete firewall).
Tests worth reading: `TestMeshAssignments`, `TestPortsSupported`,
`TestStressSpawnsAndStopsExtraListeners` in internal/appcore/stress_test.go.
