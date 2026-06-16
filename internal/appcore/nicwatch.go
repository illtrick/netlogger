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
// EEE / Gigabit-Lite / a flaky coupler produce), and any rise in error/discard
// counters. It only reports adapters present in BOTH polls, so the first poll
// (empty prev) and freshly-appeared adapters don't fire spurious events — and a
// discard delta is never confused with an adapter's lifetime total.
func nicEvents(prev map[string]nicstat.NIC, cur []nicstat.NIC) []nicEvent {
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
		if rxd+txd+rxe+txe > 0 {
			evs = append(evs, nicEvent{
				online: true,
				detail: fmt.Sprintf("%s discards rx+%d tx+%d errors rx+%d tx+%d", n.Name, rxd, txd, rxe, txe),
			})
		}
	}
	return evs
}
