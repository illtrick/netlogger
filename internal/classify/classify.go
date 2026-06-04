// Package classify holds pure decision functions that turn measured series into
// a fault classification (spec §9.4). No I/O — deterministic and testable.
package classify

// rampThresholdMs is how far latency must climb above baseline under load for
// the rise to count as a bufferbloat-style queue buildup.
const rampThresholdMs = 50.0

// BufferbloatVsFault classifies a load window from the latency series under
// load (ms) vs the idle baseline (ms) and whether loss occurred under load.
//   - smooth large latency rise, no loss          -> "bufferbloat"
//   - loss without a large smooth latency rise     -> "fault"
//   - neither                                       -> "inconclusive"
func BufferbloatVsFault(baselineMs float64, underLoadMs []float64, lossDuringLoad bool) string {
	if len(underLoadMs) == 0 {
		return "inconclusive"
	}
	max := underLoadMs[0]
	for _, v := range underLoadMs {
		if v > max {
			max = v
		}
	}
	bigRamp := (max - baselineMs) >= rampThresholdMs
	switch {
	case bigRamp && !lossDuringLoad:
		return "bufferbloat"
	case lossDuringLoad && !bigRamp:
		return "fault"
	case lossDuringLoad && bigRamp:
		// loss AND a big queue: lean fault (loss is the harder symptom)
		return "fault"
	default:
		return "inconclusive"
	}
}

// LANvsWAN classifies where a drop sits from whether the gateway and/or an
// external target also failed in the same window.
func LANvsWAN(gatewayFailed, externalFailed bool) string {
	switch {
	case gatewayFailed:
		return "lan"
	case externalFailed:
		return "wan"
	default:
		return "unknown"
	}
}
