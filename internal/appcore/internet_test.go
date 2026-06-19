package appcore

import (
	"context"
	"testing"
)

func TestGradeBufferbloat(t *testing.T) {
	cases := []struct {
		idle, loaded float64
		grade        string
	}{
		{12, 20, "A"},  // +8
		{12, 50, "B"},  // +38
		{12, 89, "C"},  // +77 (matches the mockup)
		{12, 180, "D"}, // +168
		{12, 400, "F"}, // +388
	}
	for _, c := range cases {
		g, _, rpm := gradeBufferbloat(c.idle, c.loaded)
		if g != c.grade {
			t.Fatalf("idle %.0f loaded %.0f → grade %s, want %s", c.idle, c.loaded, g, c.grade)
		}
		if rpm <= 0 {
			t.Fatalf("rpm should be positive for loaded %.0f", c.loaded)
		}
	}
	// negative added latency clamps to grade A.
	if g, added, _ := gradeBufferbloat(50, 40); g != "A" || added != 0 {
		t.Fatalf("loaded<idle should clamp to A/0, got %s/%v", g, added)
	}
}

func TestRunInternetAssembles(t *testing.T) {
	d := internetDeps{
		endpoint: "FakeNet",
		idle:     func() float64 { return 12 },
		download: func(ctx context.Context) (float64, float64, error) { return 487, 89, nil },
		upload:   func(ctx context.Context) (float64, float64, error) { return 21, 60, nil },
	}
	res := runInternet(d)
	if res.DownMbit != 487 || res.UpMbit != 21 {
		t.Fatalf("throughput = %v/%v, want 487/21", res.DownMbit, res.UpMbit)
	}
	if res.IdleMs != 12 || res.LoadedMs != 89 { // loaded = max(89, 60)
		t.Fatalf("latency = %v/%v, want 12/89", res.IdleMs, res.LoadedMs)
	}
	if res.Grade != "C" {
		t.Fatalf("grade = %s, want C", res.Grade)
	}
	if res.Endpoint != "FakeNet" {
		t.Fatalf("endpoint not preserved")
	}
}

func TestInternetTestLocalVsRemote(t *testing.T) {
	a := &App{nodeID: "self"}
	a.localInternet = func(endpoint string) InternetResult { return InternetResult{DownMbit: 100, Endpoint: endpoint} }
	var gotURL string
	a.FetchInternet = func(baseURL, endpoint string) (InternetResult, error) {
		gotURL = baseURL
		return InternetResult{DownMbit: 200}, nil
	}
	local := a.InternetTest(PeerInfo{ID: "self"}, "Cloudflare")
	if local.DownMbit != 100 {
		t.Fatalf("self should run locally, got %v", local.DownMbit)
	}
	remote := a.InternetTest(PeerInfo{ID: "p", Addr: "10.0.0.2:8088"}, "Cloudflare")
	if remote.DownMbit != 200 || gotURL != "http://10.0.0.2:8088" {
		t.Fatalf("peer should run remotely, got %v url=%q", remote.DownMbit, gotURL)
	}
}
