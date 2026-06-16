package appcore

import "sync"

// histRing is a fixed-capacity rolling buffer of float64 samples (oldest first
// when read), used for per-metric sparklines.
type histRing struct {
	mu  sync.Mutex
	buf []float64
	cap int
}

func newHistRing(capacity int) *histRing { return &histRing{cap: capacity} }

func (r *histRing) push(v float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, v)
	if len(r.buf) > r.cap {
		r.buf = r.buf[len(r.buf)-r.cap:]
	}
}

// reset clears the buffer in place, keeping the same ring identity so callers
// holding this *histRing don't observe a torn pointer across a session reset.
func (r *histRing) reset() {
	r.mu.Lock()
	r.buf = nil
	r.mu.Unlock()
}

func (r *histRing) values() []float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]float64, len(r.buf))
	copy(out, r.buf)
	return out
}
