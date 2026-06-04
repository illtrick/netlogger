package mesh

import "sync"

// Offsets is a thread-safe map of agent id -> measured clock Offset.
type Offsets struct {
	mu sync.RWMutex
	m  map[string]Offset
}

// NewOffsets returns an empty Offsets store.
func NewOffsets() *Offsets { return &Offsets{m: make(map[string]Offset)} }

// Set records the offset for an agent.
func (o *Offsets) Set(id string, off Offset) {
	o.mu.Lock()
	o.m[id] = off
	o.mu.Unlock()
}

// Get returns the offset for an agent and whether it is present.
func (o *Offsets) Get(id string) (Offset, bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	off, ok := o.m[id]
	return off, ok
}
