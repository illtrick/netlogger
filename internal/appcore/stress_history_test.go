package appcore

import (
	"path/filepath"
	"strings"
	"testing"

	"netlogger/internal/store"
)

func TestFmtCap(t *testing.T) {
	if fmtCap(200) != "200 Mb/s" || fmtCap(1500) != "1.5 Gb/s" || fmtCap(2000) != "2 Gb/s" {
		t.Fatalf("fmtCap: %q %q %q", fmtCap(200), fmtCap(1500), fmtCap(2000))
	}
}

func TestMmssStr(t *testing.T) {
	if mmssStr(600) != "10:00" || mmssStr(125) != "2:05" || mmssStr(-3) != "0:00" {
		t.Fatalf("mmssStr: %q %q %q", mmssStr(600), mmssStr(125), mmssStr(-3))
	}
}

func TestRecordStressRunPersists(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	a := &App{store: st}
	a.RecordStressRun(600, 6, 200, "tcp", "ryzen", 38, 0)
	rows, _ := st.TestResults("stress", 10)
	if len(rows) != 1 {
		t.Fatalf("want 1 stress row, got %d", len(rows))
	}
	r := rows[0]
	if r.Label != "6 links · 200 Mb/s cap · TCP" {
		t.Fatalf("label = %q", r.Label)
	}
	if !strings.Contains(r.Detail, "worst +38 ms on ryzen") || !strings.Contains(r.Detail, "10:00") || !strings.Contains(r.Detail, "0 aborts") {
		t.Fatalf("detail = %q", r.Detail)
	}
	if r.DownMbit != 0 || r.UpMbit != 0 {
		t.Fatalf("stress row must carry no rates: %+v", r)
	}
}

func TestRecordStressRunNoWorstHost(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	a := &App{store: st}
	a.RecordStressRun(120, 2, 1500, "tcp", "", 0, 1)
	rows, _ := st.TestResults("stress", 10)
	if len(rows) != 1 {
		t.Fatalf("want 1 row")
	}
	if rows[0].Label != "2 links · 1.5 Gb/s cap · TCP" {
		t.Fatalf("label = %q", rows[0].Label)
	}
	if strings.Contains(rows[0].Detail, "worst") || !strings.Contains(rows[0].Detail, "2:00") || !strings.Contains(rows[0].Detail, "1 aborts") {
		t.Fatalf("no-host detail = %q", rows[0].Detail)
	}
}
