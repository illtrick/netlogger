//go:build windows

package singleton

import "testing"

func TestAcquireSecondFailsWhileHeld(t *testing.T) {
	name := "NetLoggerTest.SingleInstance.A1"
	release, ok, err := Acquire(name)
	if err != nil {
		t.Fatalf("first acquire err: %v", err)
	}
	if !ok {
		t.Fatalf("first acquire should succeed")
	}
	defer release()

	_, ok2, err := Acquire(name)
	if err != nil {
		t.Fatalf("second acquire err: %v", err)
	}
	if ok2 {
		t.Fatalf("second acquire should fail while first is held")
	}
}

func TestAcquireSucceedsAfterRelease(t *testing.T) {
	name := "NetLoggerTest.SingleInstance.A2"
	release, ok, _ := Acquire(name)
	if !ok {
		t.Fatalf("first acquire should succeed")
	}
	release()
	release2, ok2, err := Acquire(name)
	if err != nil {
		t.Fatalf("acquire after release err: %v", err)
	}
	if !ok2 {
		t.Fatalf("acquire after release should succeed")
	}
	release2()
}
