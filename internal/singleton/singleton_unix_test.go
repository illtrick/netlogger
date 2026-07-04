//go:build darwin || linux

package singleton

import "testing"

func TestAcquireSecondCallerBlocked(t *testing.T) {
	rel1, ok, err := Acquire("netlogger-singleton-test")
	if err != nil || !ok {
		t.Fatalf("first acquire: ok=%v err=%v", ok, err)
	}
	defer rel1()

	// Same-process flock re-acquire on a NEW fd must be refused.
	rel2, ok2, err2 := Acquire("netlogger-singleton-test")
	if err2 != nil {
		t.Fatalf("second acquire err: %v", err2)
	}
	if ok2 {
		rel2()
		t.Fatal("second acquire succeeded; want blocked")
	}
}

func TestAcquireReleasedThenReacquired(t *testing.T) {
	rel, ok, err := Acquire("netlogger-singleton-test2")
	if err != nil || !ok {
		t.Fatalf("first acquire: ok=%v err=%v", ok, err)
	}
	rel()
	rel2, ok2, err2 := Acquire("netlogger-singleton-test2")
	if err2 != nil || !ok2 {
		t.Fatalf("re-acquire after release: ok=%v err=%v", ok2, err2)
	}
	rel2()
}
