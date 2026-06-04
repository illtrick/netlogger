package readiness

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"netlogger/internal/config"
	"netlogger/internal/mesh"
)

// infoServer serves a fixed /api/info payload.
func infoServer(t *testing.T, info mesh.Info) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/info", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(info)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func addrOf(t *testing.T, url string) string {
	t.Helper()
	return strings.TrimPrefix(url, "http://")
}

func checkNamed(res Result, name string) (Check, bool) {
	for _, c := range res.Checks {
		if c.Name == name {
			return c, true
		}
	}
	return Check{}, false
}

func TestCheckAllGood(t *testing.T) {
	srv := infoServer(t, mesh.Info{NodeID: "ncase", Host: "h", TimeUnixUS: time.Now().UTC().UnixMicro(), Iperf3Version: "iperf 3.18", DataWritable: true})
	c := NewChecker()
	res := c.Check(config.Node{ID: "ncase", Address: addrOf(t, srv.URL)})
	if !res.Online {
		t.Fatal("should be online")
	}
	if res.Issues != 0 {
		t.Fatalf("want 0 issues, got %d (%+v)", res.Issues, res.Checks)
	}
}

func TestCheckFlagsMissingIperf3AndUnwritable(t *testing.T) {
	srv := infoServer(t, mesh.Info{NodeID: "ncase", TimeUnixUS: time.Now().UTC().UnixMicro(), Iperf3Version: "", DataWritable: false})
	res := NewChecker().Check(config.Node{ID: "ncase", Address: addrOf(t, srv.URL)})
	if c, _ := checkNamed(res, "iperf3 present"); c.OK {
		t.Fatal("iperf3 should be flagged missing")
	}
	if c, _ := checkNamed(res, "data dir writable"); c.OK {
		t.Fatal("data writable should be flagged false")
	}
	if res.Issues < 2 {
		t.Fatalf("want >=2 issues, got %d", res.Issues)
	}
}

func TestCheckClockOutOfTolerance(t *testing.T) {
	// Agent reports a time 10s in the future -> offset exceeds the 2s tolerance.
	future := time.Now().Add(10 * time.Second).UTC().UnixMicro()
	srv := infoServer(t, mesh.Info{NodeID: "ncase", TimeUnixUS: future, Iperf3Version: "x", DataWritable: true})
	res := NewChecker().Check(config.Node{ID: "ncase", Address: addrOf(t, srv.URL)})
	if c, _ := checkNamed(res, "clock sync within tolerance"); c.OK {
		t.Fatalf("clock should be out of tolerance: %+v", c)
	}
}

func TestCheckUnreachable(t *testing.T) {
	res := NewChecker().Check(config.Node{ID: "dead", Address: "127.0.0.1:1"})
	if res.Online {
		t.Fatal("unreachable node should be offline")
	}
	if c, _ := checkNamed(res, "reachable"); c.OK {
		t.Fatal("reachable check should fail")
	}
}

func TestCheckNoAddressIsOfflineNotPanic(t *testing.T) {
	res := NewChecker().Check(config.Node{ID: "switch1"})
	if res.Online {
		t.Fatal("addressless node cannot be online")
	}
}
