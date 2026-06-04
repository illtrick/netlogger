package svcctl

import (
	"errors"
	"testing"
)

func TestParseState(t *testing.T) {
	cases := []struct {
		out  string
		err  error
		want string
	}{
		{"SERVICE_NAME: NetLogger\n  STATE : 4  RUNNING", nil, "running"},
		{"  STATE : 1  STOPPED", nil, "stopped"},
		{"  STATE : 2  START_PENDING", nil, "starting"},
		{"[SC] EnumQueryServicesStatus:OpenService FAILED 1060:\n\nThe specified service does not exist as an installed service.", errors.New("exit 1"), "not installed"},
		{"", errors.New("exit 1"), "not installed"},
	}
	for _, c := range cases {
		if got := parseState(c.out, c.err); got != c.want {
			t.Fatalf("parseState(%q)=%q, want %q", c.out, got, c.want)
		}
	}
}

func TestValidAction(t *testing.T) {
	for _, a := range []string{"install", "start", "stop", "uninstall"} {
		if !ValidAction(a) {
			t.Fatalf("%q should be valid", a)
		}
	}
	for _, a := range []string{"", "delete", "rm -rf", "run"} {
		if ValidAction(a) {
			t.Fatalf("%q must be rejected", a)
		}
	}
}
