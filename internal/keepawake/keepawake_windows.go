//go:build windows

// Package keepawake prevents the system from sleeping while NetLogger runs, so a
// long logging session is not interrupted. It uses SetThreadExecutionState and
// re-asserts periodically (Go can move goroutines across threads, so a one-shot
// call is not reliable).
package keepawake

import (
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

const (
	esContinuous     = 0x80000000
	esSystemRequired = 0x00000001
)

var procSetThreadExecutionState = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetThreadExecutionState")

func setState(flags uint32) { _, _, _ = procSetThreadExecutionState.Call(uintptr(flags)) }

// Keeper holds the system awake until Stop.
type Keeper struct {
	stop chan struct{}
	done chan struct{}
	once sync.Once
}

// Start begins keeping the system awake.
func Start() *Keeper {
	k := &Keeper{stop: make(chan struct{}), done: make(chan struct{})}
	go k.run()
	return k
}

func (k *Keeper) run() {
	defer close(k.done)
	setState(esContinuous | esSystemRequired)
	t := time.NewTicker(50 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-k.stop:
			setState(esContinuous) // clear the system-required flag
			return
		case <-t.C:
			setState(esContinuous | esSystemRequired) // re-assert
		}
	}
}

// Stop releases the keep-awake state. Safe to call more than once.
func (k *Keeper) Stop() {
	k.once.Do(func() {
		close(k.stop)
		<-k.done
	})
}
