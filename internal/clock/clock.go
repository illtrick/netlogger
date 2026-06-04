// Package clock provides UTC microsecond timestamps and a fake for tests.
package clock

import "time"

// Clock yields the current time as Unix epoch microseconds (UTC).
type Clock interface {
	NowUnixMicro() int64
}

// System is the real clock. time.Now carries a monotonic reading internally,
// so intervals computed from successive values are immune to wall-clock steps.
type System struct{}

// NowUnixMicro returns the current UTC time in epoch microseconds.
func (System) NowUnixMicro() int64 { return time.Now().UTC().UnixMicro() }

// Fixed is a deterministic clock for tests.
type Fixed struct{ Micros int64 }

// NowUnixMicro returns the fixed value.
func (f Fixed) NowUnixMicro() int64 { return f.Micros }
