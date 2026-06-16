package keepawake

import "testing"

func TestStartStopSafe(t *testing.T) {
	k := Start()
	if k == nil {
		t.Fatal("Start returned nil")
	}
	k.Stop()
	k.Stop() // double-stop must be safe
}
