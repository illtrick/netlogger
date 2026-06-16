package appcore

import (
	"fmt"
	"strings"

	"netlogger/internal/nicstat"
)

// nicEvent is a connectivity-timeline entry derived from a NIC state change.
type nicEvent struct {
	online bool
	detail string
}

// nicEvents compares the previous NIC poll (keyed by adapter name) against the
// current poll and returns the changes worth recording on the connectivity
// timeline: link status changes, link-speed renegotiation (the PHY-flap that
// EEE / Gigabit-Lite / a flaky coupler produce), and the start/end of an
// error/discard episode. It only reports adapters present in BOTH polls, so the
// first poll (empty prev) and freshly-appeared adapters don't fire spurious
// events — and a discard delta is never confused with an adapter's lifetime
// total.
//
// discarding is per-adapter edge state (mutated): discard/error events are
// edge-triggered like the UDP-loss hysteresis — one "started" on the clean→lossy
// edge and one "cleared" on lossy→clean — so a chronically lossy NIC produces
// two events per episode, not one every poll (which would flood the ring and
// evict the link-flap events this feature exists to surface).
func nicEvents(prev map[string]nicstat.NIC, cur []nicstat.NIC, discarding map[string]bool) []nicEvent {
	var evs []nicEvent
	for _, n := range cur {
		p, ok := prev[n.Name]
		if !ok {
			continue
		}
		if !strings.EqualFold(p.Status, n.Status) {
			evs = append(evs, nicEvent{
				online: strings.EqualFold(n.Status, "Up"),
				detail: fmt.Sprintf("%s link %s (was %s)", n.Name, n.Status, p.Status),
			})
		}
		if n.LinkSpeed != "" && p.LinkSpeed != "" && p.LinkSpeed != n.LinkSpeed {
			evs = append(evs, nicEvent{
				online: true,
				detail: fmt.Sprintf("%s link speed %s → %s", n.Name, p.LinkSpeed, n.LinkSpeed),
			})
		}
		rxd := nonNeg(n.RxDiscards - p.RxDiscards)
		txd := nonNeg(n.TxDiscards - p.TxDiscards)
		rxe := nonNeg(n.RxErrors - p.RxErrors)
		txe := nonNeg(n.TxErrors - p.TxErrors)
		switch lossy := rxd+txd+rxe+txe > 0; {
		case lossy && !discarding[n.Name]:
			discarding[n.Name] = true
			evs = append(evs, nicEvent{
				online: false, // a degradation episode — vermillion in the UI
				detail: fmt.Sprintf("%s discards/errors began (rx+%d tx+%d errors rx+%d tx+%d)", n.Name, rxd, txd, rxe, txe),
			})
		case !lossy && discarding[n.Name]:
			discarding[n.Name] = false
			evs = append(evs, nicEvent{
				online: true,
				detail: fmt.Sprintf("%s discards/errors cleared", n.Name),
			})
		}
	}
	return evs
}
